package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/shared"
)

// SDKClient is the minimum surface the SDK-backed resolvers need from the
// Formance SDK. It's defined locally in this package so the engine doesn't
// take a direct dependency on the broader service layer; the service layer
// supplies a real `*sdk.Formance` (or a thin adapter) at wiring time.
type SDKClient interface {
	V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error)
	V2ListAccounts(ctx context.Context, req operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error)
	V3GetPoolBalances(ctx context.Context, req operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error)
	V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error)
}

// SDKLedgerResolver implements LedgerResolver against the Formance SDK.
type SDKLedgerResolver struct {
	client SDKClient
}

func NewSDKLedgerResolver(client SDKClient) *SDKLedgerResolver {
	return &SDKLedgerResolver{client: client}
}

// AggregateBalance calls V2.GetBalancesAggregated with the supplied PIT and
// metadata-query JSON. The query argument is the raw JSON value the template
// already produced; we deserialize it into a map[string]any for the SDK.
func (r *SDKLedgerResolver) AggregateBalance(ctx context.Context, ledger string, query json.RawMessage, pit time.Time) (map[string]*big.Int, error) {
	queryMap, err := unmarshalQuery(query)
	if err != nil {
		return nil, fmt.Errorf("ledgerSet query: %w", err)
	}

	resp, err := r.client.V2GetBalancesAggregated(ctx, operations.V2GetBalancesAggregatedRequest{
		Ledger:      ledger,
		RequestBody: queryMap,
		Pit:         &pit,
	})
	if err != nil {
		return nil, fmt.Errorf("aggregate balances on %q: %w", ledger, err)
	}
	if resp == nil || resp.V2AggregateBalancesResponse == nil {
		return nil, errors.New("aggregate balances: empty response")
	}
	return resp.V2AggregateBalancesResponse.Data, nil
}

// ListAccounts returns the accounts matched by query at the given PIT, each
// with its per-asset balance. Paginates V2ListAccounts with expand=volumes so
// each account carries its volumes, from which the balance is derived. Aborts
// with an error (never silently truncates) once more than `limit` accounts have
// been collected — that's the evaluation's accounts budget.
func (r *SDKLedgerResolver) ListAccounts(ctx context.Context, ledger string, query json.RawMessage, pit time.Time, limit int) ([]Account, error) {
	queryMap, err := unmarshalQuery(query)
	if err != nil {
		return nil, fmt.Errorf("listAccounts query: %w", err)
	}
	expand := "volumes"
	out := make([]Account, 0, 256)
	var cursor *string
	for {
		req := operations.V2ListAccountsRequest{Ledger: ledger}
		if cursor == nil {
			// First page carries the filter; cursor pages must send only the
			// cursor token (the ledger API rejects mixing them).
			req.RequestBody = queryMap
			req.Pit = &pit
			req.Expand = &expand
		} else {
			req.Cursor = cursor
		}
		resp, err := r.client.V2ListAccounts(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("list accounts on %q: %w", ledger, err)
		}
		if resp == nil || resp.V2AccountsCursorResponse == nil {
			return nil, errors.New("list accounts: empty response")
		}
		cur := resp.V2AccountsCursorResponse.Cursor
		for i := range cur.Data {
			a := cur.Data[i]
			balances := make(map[string]*big.Int, len(a.Volumes))
			for asset, vol := range a.Volumes {
				balances[asset] = volumeBalance(vol)
			}
			out = append(out, Account{
				Address:  a.Address,
				Ledger:   ledger,
				Metadata: a.Metadata,
				Balances: balances,
			})
			if len(out) > limit {
				return nil, fmt.Errorf("listAccounts: matched more than %d accounts on %q (accounts budget)", limit, ledger)
			}
		}
		if !cur.HasMore || cur.Next == nil {
			break
		}
		cursor = cur.Next
	}
	return out, nil
}

// volumeBalance derives an asset balance from a ledger volume: the API-provided
// Balance when present, else input − output (treating absent sides as 0).
func volumeBalance(v shared.V2Volume) *big.Int {
	if v.Balance != nil {
		return v.Balance
	}
	bal := new(big.Int)
	if v.Input != nil {
		bal.Add(bal, v.Input)
	}
	if v.Output != nil {
		bal.Sub(bal, v.Output)
	}
	return bal
}

// SDKPaymentsResolver implements PaymentsResolver against the Formance SDK.
//
// A nil pit reads the current snapshot via V3.GetPoolBalancesLatest; a non-nil
// pit reads point-in-time via V3.GetPoolBalances (GET /v3/pools/{id}/balances?at=).
// Both are genuine payments-v3 reads (verified against v3.3.1). The PIT route is
// used only for explicitly historical reads — see PaymentsResolver on why "as of
// now" stays on latest (the balance-window tail).
type SDKPaymentsResolver struct {
	client SDKClient
}

func NewSDKPaymentsResolver(client SDKClient) *SDKPaymentsResolver {
	return &SDKPaymentsResolver{client: client}
}

func (r *SDKPaymentsResolver) PoolBalance(ctx context.Context, poolID string, pit *time.Time) (map[string]*big.Int, error) {
	var data []shared.V3PoolBalance
	if pit == nil {
		resp, err := r.client.V3GetPoolBalancesLatest(ctx, operations.V3GetPoolBalancesLatestRequest{PoolID: poolID})
		if err != nil {
			return nil, fmt.Errorf("pool balances latest %q: %w", poolID, err)
		}
		if resp == nil || resp.V3PoolBalancesResponse == nil {
			return nil, errors.New("pool balances: empty response")
		}
		data = resp.V3PoolBalancesResponse.Data
	} else {
		resp, err := r.client.V3GetPoolBalances(ctx, operations.V3GetPoolBalancesRequest{PoolID: poolID, At: pit})
		if err != nil {
			return nil, fmt.Errorf("pool balances at %s %q: %w", pit.Format(time.RFC3339), poolID, err)
		}
		if resp == nil || resp.V3PoolBalancesResponse == nil {
			return nil, errors.New("pool balances: empty response")
		}
		data = resp.V3PoolBalancesResponse.Data
	}
	out := make(map[string]*big.Int, len(data))
	for _, b := range data {
		if b.Amount == nil {
			continue
		}
		out[b.Asset] = b.Amount
	}
	return out, nil
}

// unmarshalQuery accepts either a JSON object literal or a string that wraps
// such an object. Template compilers may produce either form depending on how
// they serialize the metadata-query AST.
func unmarshalQuery(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return map[string]any{}, nil
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		raw = json.RawMessage(asString)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("expected JSON object, got %q: %w", string(raw), err)
	}
	return out, nil
}
