package engine

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/shared"
	"github.com/stretchr/testify/require"
)

// fakeSDKClient implements engine.SDKClient; only V2ListAccounts is meaningful
// here. pages is consumed one per call (simulating cursor pagination).
type fakeSDKClient struct {
	pages []shared.V2AccountsCursorResponseCursor
	calls int
	// Pool-balance stubs: both reads return poolData; the fake records which
	// branch ran and the `at` it received so PoolBalance's routing can be pinned.
	poolData    []shared.V3PoolBalance
	atCalls     int
	latestCalls int
	lastAt      *time.Time
}

func (f *fakeSDKClient) V2GetBalancesAggregated(context.Context, operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error) {
	return nil, nil
}
func (f *fakeSDKClient) V3GetPoolBalances(_ context.Context, req operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error) {
	f.atCalls++
	f.lastAt = req.At
	return &operations.V3GetPoolBalancesResponse{
		StatusCode:             200,
		V3PoolBalancesResponse: &shared.V3PoolBalancesResponse{Data: f.poolData},
	}, nil
}
func (f *fakeSDKClient) V3GetPoolBalancesLatest(context.Context, operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	f.latestCalls++
	return &operations.V3GetPoolBalancesLatestResponse{
		StatusCode:             200,
		V3PoolBalancesResponse: &shared.V3PoolBalancesResponse{Data: f.poolData},
	}, nil
}
func (f *fakeSDKClient) V2ListAccounts(_ context.Context, _ operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error) {
	cur := f.pages[f.calls]
	f.calls++
	return &operations.V2ListAccountsResponse{V2AccountsCursorResponse: &shared.V2AccountsCursorResponse{Cursor: cur}}, nil
}

func vol(input, output int64) shared.V2Volume {
	return shared.V2Volume{Input: big.NewInt(input), Output: big.NewInt(output)}
}

// ListAccounts derives per-asset balances from volumes and walks the cursor.
func TestSDKLedgerResolver_ListAccounts_PaginatesAndDerivesBalance(t *testing.T) {
	next := "page2"
	client := &fakeSDKClient{pages: []shared.V2AccountsCursorResponseCursor{
		{
			HasMore: true,
			Next:    &next,
			Data: []shared.V2Account{
				{Address: "merchant:a", Volumes: map[string]shared.V2Volume{"USD/2": vol(500, 150)}},             // 350
				{Address: "merchant:b", Volumes: map[string]shared.V2Volume{"USD/2": {Balance: big.NewInt(42)}}}, // explicit balance
			},
		},
		{
			HasMore: false,
			Data: []shared.V2Account{
				{Address: "merchant:c", Volumes: map[string]shared.V2Volume{"EUR/2": vol(100, 100)}}, // 0
			},
		},
	}}
	r := NewSDKLedgerResolver(client)

	accts, err := r.ListAccounts(context.Background(), "main", nil, time.Now(), 50_000)
	require.NoError(t, err)
	require.Equal(t, 2, client.calls, "should have walked both pages")
	require.Len(t, accts, 3)

	require.Equal(t, "merchant:a", accts[0].Address)
	require.Equal(t, "350", accts[0].Balances["USD/2"].String(), "input - output")
	require.Equal(t, "42", accts[1].Balances["USD/2"].String(), "explicit Balance wins")
	require.Equal(t, "0", accts[2].Balances["EUR/2"].String())
}

// The accounts budget aborts with an error rather than truncating.
func TestSDKLedgerResolver_ListAccounts_BudgetExceeded(t *testing.T) {
	client := &fakeSDKClient{pages: []shared.V2AccountsCursorResponseCursor{{
		HasMore: false,
		Data: []shared.V2Account{
			{Address: "a", Volumes: map[string]shared.V2Volume{"USD/2": vol(1, 0)}},
			{Address: "b", Volumes: map[string]shared.V2Volume{"USD/2": vol(1, 0)}},
		},
	}}}
	r := NewSDKLedgerResolver(client)

	_, err := r.ListAccounts(context.Background(), "main", nil, time.Now(), 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "budget")
}

// TestSDKPaymentsResolver_PoolBalance pins the routing: a nil pit reads the
// current snapshot (V3GetPoolBalancesLatest), a non-nil pit reads point-in-time
// (V3GetPoolBalances) with the instant forwarded as `at`.
func TestSDKPaymentsResolver_PoolBalance(t *testing.T) {
	data := []shared.V3PoolBalance{{Asset: "USD/2", Amount: big.NewInt(500)}}

	t.Run("nil pit reads latest", func(t *testing.T) {
		c := &fakeSDKClient{poolData: data}
		got, err := NewSDKPaymentsResolver(c).PoolBalance(context.Background(), "pool", nil)
		require.NoError(t, err)
		require.Equal(t, 1, c.latestCalls)
		require.Equal(t, 0, c.atCalls)
		require.Equal(t, big.NewInt(500), got["USD/2"])
	})

	t.Run("non-nil pit reads point-in-time with at", func(t *testing.T) {
		c := &fakeSDKClient{poolData: data}
		at := time.Date(2026, 6, 17, 16, 0, 0, 0, time.UTC)
		got, err := NewSDKPaymentsResolver(c).PoolBalance(context.Background(), "pool", &at)
		require.NoError(t, err)
		require.Equal(t, 1, c.atCalls)
		require.Equal(t, 0, c.latestCalls)
		require.NotNil(t, c.lastAt)
		require.True(t, c.lastAt.Equal(at), "at must be forwarded to V3GetPoolBalances")
		require.Equal(t, big.NewInt(500), got["USD/2"])
	})
}
