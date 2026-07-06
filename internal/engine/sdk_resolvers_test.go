package engine

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/shared"
	"github.com/stretchr/testify/require"
)

// fakeSDKClient implements the trimmed engine.SDKClient (pool reads only).
type fakeSDKClient struct {
	resp *operations.V3GetPoolBalancesLatestResponse
	err  error
}

func (f *fakeSDKClient) V3GetPoolBalancesLatest(context.Context, operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	return f.resp, f.err
}

func TestSDKPaymentsResolver_PoolBalanceLatest(t *testing.T) {
	client := &fakeSDKClient{resp: &operations.V3GetPoolBalancesLatestResponse{
		V3PoolBalancesResponse: &shared.V3PoolBalancesResponse{
			Data: []shared.V3PoolBalance{
				{Asset: "USD/2", Amount: big.NewInt(350)},
				{Asset: "EUR/2", Amount: big.NewInt(-100)},
				{Asset: "GBP/2", Amount: nil}, // skipped
			},
		},
	}}
	r := NewSDKPaymentsResolver(client)

	out, err := r.PoolBalanceLatest(context.Background(), "pool_xyz")
	require.NoError(t, err)
	require.Equal(t, "350", out["USD/2"].String())
	require.Equal(t, "-100", out["EUR/2"].String())
	require.NotContains(t, out, "GBP/2", "nil amounts are skipped")
}

func TestSDKPaymentsResolver_SurfacesError(t *testing.T) {
	r := NewSDKPaymentsResolver(&fakeSDKClient{err: errors.New("payments down")})

	_, err := r.PoolBalanceLatest(context.Background(), "pool_xyz")
	require.Error(t, err)
	require.Contains(t, err.Error(), "payments down")
}
