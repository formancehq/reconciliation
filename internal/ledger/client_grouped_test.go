package ledger

import (
	"context"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type groupingBucketClient struct {
	servicepb.BucketServiceClient
	request *servicepb.AggregateVolumesRequest
}

func (c *groupingBucketClient) AggregateVolumes(_ context.Context, request *servicepb.AggregateVolumesRequest, _ ...grpc.CallOption) (*commonpb.AggregateResult, error) {
	c.request = request

	// A grouped aggregate answers on Groups and leaves Volumes empty — the trap
	// this method exists to absorb.
	return &commonpb.AggregateResult{Groups: []*commonpb.GroupedAggregateResult{
		{Prefix: "alert:st:open:rule:a:", Volumes: []*commonpb.AggregatedVolume{
			{Asset: "ALERT", Input: commonpb.NewUint256FromUint64(4), Output: commonpb.NewUint256FromUint64(1)},
		}},
		{Prefix: "alert:st:ack:rule:a:", Volumes: nil},
	}}, nil
}

func TestAggregateVolumesGroupedReadsTheGroupsField(t *testing.T) {
	t.Parallel()
	service := &groupingBucketClient{}
	client := &Client{service: service}

	groups, err := client.AggregateVolumesGrouped(context.Background(), "_recon", nil,
		[]string{"alert:st:open:rule:a:", "alert:st:ack:rule:a:"})

	require.NoError(t, err)
	require.Equal(t, []string{"alert:st:open:rule:a:", "alert:st:ack:rule:a:"}, service.request.GetGroupByPrefixes())
	require.True(t, service.request.GetCollapseColors())
	require.Equal(t, "3", groups["alert:st:open:rule:a:"]["ALERT"].String())
	require.Empty(t, groups["alert:st:ack:rule:a:"], "a group that matched nothing carries no assets")
}

// No prefixes means nothing to group: the call is skipped rather than sent as an
// ungrouped aggregate over the whole ledger.
func TestAggregateVolumesGroupedSkipsTheCallWithoutPrefixes(t *testing.T) {
	t.Parallel()
	service := &groupingBucketClient{}
	client := &Client{service: service}

	groups, err := client.AggregateVolumesGrouped(context.Background(), "_recon", nil, nil)

	require.NoError(t, err)
	require.Empty(t, groups)
	require.Nil(t, service.request, "no request should reach the ledger")
}
