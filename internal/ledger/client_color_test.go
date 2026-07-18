package ledger

import (
	"context"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type colorAwareBucketClient struct {
	servicepb.BucketServiceClient
	aggregateRequest *servicepb.AggregateVolumesRequest
	getRequest       *servicepb.GetAccountRequest
}

func (c *colorAwareBucketClient) AggregateVolumes(_ context.Context, request *servicepb.AggregateVolumesRequest, _ ...grpc.CallOption) (*commonpb.AggregateResult, error) {
	c.aggregateRequest = request

	return &commonpb.AggregateResult{Volumes: []*commonpb.AggregatedVolume{
		{Asset: "USD/2", Input: commonpb.NewUint256FromUint64(100), Output: commonpb.NewUint256FromUint64(20)},
		{Asset: "USD/2", Color: "RESERVED", Input: commonpb.NewUint256FromUint64(30), Output: commonpb.NewUint256FromUint64(5)},
	}}, nil
}

func (c *colorAwareBucketClient) GetAccount(_ context.Context, request *servicepb.GetAccountRequest, _ ...grpc.CallOption) (*commonpb.Account, error) {
	c.getRequest = request

	return &commonpb.Account{Address: request.GetAddress()}, nil
}

func TestAggregateVolumesCollapsesLedgerColors(t *testing.T) {
	service := &colorAwareBucketClient{}
	client := &Client{service: service}

	balances, err := client.AggregateVolumes(context.Background(), "default", nil)

	require.NoError(t, err)
	require.True(t, service.aggregateRequest.GetCollapseColors())
	require.Equal(t, "105", balances["USD/2"].String())
}

func TestGetAccountRequestsCollapsedLedgerColors(t *testing.T) {
	service := &colorAwareBucketClient{}
	client := &Client{service: service}

	account, err := client.GetAccount(context.Background(), "default", "accounts:1")

	require.NoError(t, err)
	require.Equal(t, "accounts:1", account.GetAddress())
	require.True(t, service.getRequest.GetCollapseColors())
}
