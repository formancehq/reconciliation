package engine

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
)

// SDKClient is the minimum SDK surface the kernel's Tier-2 resolvers need. Ledger
// (Tier-1) sources no longer go through the SDK — they read the gRPC control-plane
// at a checkpoint (internal/ledgerresolver, ADR-002) — so only the payments-pool
// read remains here. The service layer supplies a real `*sdk.Formance` (or a thin
// adapter) at wiring time.
type SDKClient interface {
	V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error)
}

// SDKPaymentsResolver implements PaymentsResolver against the Formance SDK.
//
// Uses V3.GetPoolBalancesLatest deliberately — the legacy /api/payments/pools/{id}/balances?at=
// route returns silently empty under payments v3 (see baseline check). The latest
// endpoint returns the true current pool balance. This is the Tier-2 side: a pool
// is a heterogeneous source with no shared clock, so cross-source PIT skew is
// absorbed by tolerances in the template, not by a PIT read that doesn't exist.
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
