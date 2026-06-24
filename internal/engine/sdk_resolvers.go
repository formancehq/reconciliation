package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/shared"
)

// SDKClient is the minimum surface the SDK-backed resolvers need from the
// Formance SDK. It's defined locally in this package so the engine doesn't
// take a direct dependency on the broader service layer; the service layer
// supplies a real `*sdk.Formance` (or a thin adapter) at wiring time.
type SDKClient interface {
	V2GetLedger(ctx context.Context, req operations.V2GetLedgerRequest) (*operations.V2GetLedgerResponse, error)
	V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error)
	V2ListAccounts(ctx context.Context, req operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error)
	V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error)
}

// SDKLedgerResolver implements LedgerResolver against the Formance SDK.
// Features are cached in-process for the lifetime of the resolver because
// they don't change at runtime (a ledger's feature flags are set at creation).
//
// The cache is guarded by mu — Features is called from the engine's
// per-evaluation goroutines and concurrent rule-create/evaluate paths can
// reach it in parallel. An RWMutex matches the access pattern: many reads
// (one per evaluation), few writes (one per ledger, lifetime of the process).
type SDKLedgerResolver struct {
	client       SDKClient
	mu           sync.RWMutex
	featureCache map[string]LedgerFeatures
}

func NewSDKLedgerResolver(client SDKClient) *SDKLedgerResolver {
	return &SDKLedgerResolver{
		client:       client,
		featureCache: map[string]LedgerFeatures{},
	}
}

// Features fetches the ledger's feature flags via V2.GetLedger and translates
// the relevant ones into a LedgerFeatures struct. Cached after first call.
// The engine consults this at rule-create time to refuse metadata-filtered
// rules on history-off ledgers (see ledger#1416).
//
// Two concurrent callers racing on the same uncached ledger may both make the
// SDK call — the second one's write to the cache wins. That's accepted: the
// flags are immutable at the ledger level, so both calls produce identical
// results. The alternative (a singleflight) is not worth the dependency cost
// here.
func (r *SDKLedgerResolver) Features(ctx context.Context, ledger string) (LedgerFeatures, error) {
	r.mu.RLock()
	cached, ok := r.featureCache[ledger]
	r.mu.RUnlock()
	if ok {
		return cached, nil
	}

	resp, err := r.client.V2GetLedger(ctx, operations.V2GetLedgerRequest{Ledger: ledger})
	if err != nil {
		return LedgerFeatures{}, fmt.Errorf("get ledger %q: %w", ledger, err)
	}
	if resp == nil || resp.V2GetLedgerResponse == nil {
		return LedgerFeatures{}, fmt.Errorf("get ledger %q: empty response", ledger)
	}
	flags := resp.V2GetLedgerResponse.Data.Features
	out := LedgerFeatures{
		AccountMetadataHistory:     flags["ACCOUNT_METADATA_HISTORY"],
		TransactionMetadataHistory: flags["TRANSACTION_METADATA_HISTORY"],
	}

	r.mu.Lock()
	r.featureCache[ledger] = out
	r.mu.Unlock()
	return out, nil
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
// Uses V3.GetPoolBalancesLatest deliberately — the legacy /api/payments/pools/{id}/balances?at=
// route the prior SDK call hit returns silently empty under payments v3 (see
// baseline check). The latest endpoint returns the true current pool balance.
// Cross-source PIT consistency is handled by tolerances in the template, not
// by reaching for a PIT endpoint that doesn't exist.
type SDKPaymentsResolver struct {
	client SDKClient
}

func NewSDKPaymentsResolver(client SDKClient) *SDKPaymentsResolver {
	return &SDKPaymentsResolver{client: client}
}

func (r *SDKPaymentsResolver) PoolBalanceLatest(ctx context.Context, poolID string) (map[string]*big.Int, error) {
	resp, err := r.client.V3GetPoolBalancesLatest(ctx, operations.V3GetPoolBalancesLatestRequest{PoolID: poolID})
	if err != nil {
		return nil, fmt.Errorf("pool balances latest %q: %w", poolID, err)
	}
	if resp == nil || resp.V3PoolBalancesResponse == nil {
		return nil, errors.New("pool balances: empty response")
	}
	out := make(map[string]*big.Int, len(resp.V3PoolBalancesResponse.Data))
	for _, b := range resp.V3PoolBalancesResponse.Data {
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
