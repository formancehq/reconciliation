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
)

// SDKClient is the minimum surface the SDK-backed resolvers need from the
// Formance SDK. It's defined locally in this package so the engine doesn't
// take a direct dependency on the broader service layer; the service layer
// supplies a real `*sdk.Formance` (or a thin adapter) at wiring time.
type SDKClient interface {
	V2GetLedger(ctx context.Context, req operations.V2GetLedgerRequest) (*operations.V2GetLedgerResponse, error)
	V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error)
	V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error)
}

// SDKLedgerResolver implements LedgerResolver against the Formance SDK.
// Features are cached in-process for the lifetime of the resolver because
// they don't change at runtime (a ledger's feature flags are set at creation).
type SDKLedgerResolver struct {
	client       SDKClient
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
func (r *SDKLedgerResolver) Features(ctx context.Context, ledger string) (LedgerFeatures, error) {
	if cached, ok := r.featureCache[ledger]; ok {
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
	r.featureCache[ledger] = out
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

// ListAccounts is unimplemented in V1 GA. The per-account threshold template
// (V1.1) will land it alongside the accounts() CEL builtin.
func (r *SDKLedgerResolver) ListAccounts(_ context.Context, _ string, _ json.RawMessage, _ time.Time, _ int) ([]Account, error) {
	return nil, errors.New("ListAccounts: not implemented in V1 GA — coming with the per-account threshold template")
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
