package ledger

import (
	"context"
	"io"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/auditpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// fakeStream is a minimal ServerStreamingClient that replays a fixed slice then EOF.
type fakeStream[T any] struct {
	grpc.ServerStreamingClient[T]
	items []*T
	idx   int
}

func (s *fakeStream[T]) Recv() (*T, error) {
	if s.idx >= len(s.items) {
		return nil, io.EOF
	}
	it := s.items[s.idx]
	s.idx++
	return it, nil
}

// resolverBucketClient fakes the three RPCs ResolveAuditEntryByTransaction uses:
// ListLogs (hop 1: LogId==txId → bucket log sequence), ListAuditEntries (hop 2:
// AUDIT_FIELD_LOG_SEQUENCE → the entry), and GetAuditEntry (the full re-read).
type resolverBucketClient struct {
	servicepb.BucketServiceClient
	logs          []*commonpb.Log
	auditByLog    []*auditpb.AuditEntry
	fullBySeq     map[uint64]*auditpb.AuditEntry
	lastLogFilter *commonpb.QueryFilter
	getEntryCalls []uint64
}

func (c *resolverBucketClient) ListLogs(_ context.Context, req *servicepb.ListLogsRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[commonpb.Log], error) {
	c.lastLogFilter = req.GetOptions().GetFilter()
	return &fakeStream[commonpb.Log]{items: c.logs}, nil
}

func (c *resolverBucketClient) ListAuditEntries(_ context.Context, _ *servicepb.ListAuditEntriesRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[auditpb.AuditEntry], error) {
	return &fakeStream[auditpb.AuditEntry]{items: c.auditByLog}, nil
}

func (c *resolverBucketClient) GetAuditEntry(_ context.Context, req *servicepb.GetAuditEntryRequest, _ ...grpc.CallOption) (*auditpb.AuditEntry, error) {
	c.getEntryCalls = append(c.getEntryCalls, req.GetSequence())
	return c.fullBySeq[req.GetSequence()], nil
}

func TestResolveAuditEntryByTransaction(t *testing.T) {
	t.Parallel()

	t.Run("two-hop resolves, skipping a foreign entry that shares the log sequence", func(t *testing.T) {
		t.Parallel()
		// tx id 38 → a log whose bucket-wide Sequence is 292 (a different number).
		svc := &resolverBucketClient{
			logs: []*commonpb.Log{{Sequence: 292}},
			auditByLog: []*auditpb.AuditEntry{
				{Sequence: 900, Ledgers: []string{"other-ledger"}}, // foreign — must be skipped
				{Sequence: 228, Ledgers: []string{"reconciliation"}},
			},
			fullBySeq: map[uint64]*auditpb.AuditEntry{
				228: {Sequence: 228, Ledgers: []string{"reconciliation"}},
			},
		}
		client := &Client{service: svc}

		entry, found, err := client.ResolveAuditEntryByTransaction(context.Background(), "reconciliation", 38)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, uint64(228), entry.Sequence, "returns the reconciliation entry, not the foreign one")
		// hop 1 filtered logs by LogId == the transaction id.
		require.Equal(t, uint64(38), svc.lastLogFilter.GetLogId().GetCond().GetMin())
		// hop 2's match was re-read by its audit sequence (not the tx id or log seq).
		require.Equal(t, []uint64{228}, svc.getEntryCalls)
	})

	t.Run("no log for the transaction → not found", func(t *testing.T) {
		t.Parallel()
		client := &Client{service: &resolverBucketClient{logs: nil}}
		_, found, err := client.ResolveAuditEntryByTransaction(context.Background(), "reconciliation", 7)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("log exists but no audit entry carries it → not found", func(t *testing.T) {
		t.Parallel()
		client := &Client{service: &resolverBucketClient{logs: []*commonpb.Log{{Sequence: 55}}, auditByLog: nil}}
		_, found, err := client.ResolveAuditEntryByTransaction(context.Background(), "reconciliation", 12)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("only a foreign-ledger entry matches → not found (no cross-ledger leak)", func(t *testing.T) {
		t.Parallel()
		svc := &resolverBucketClient{
			logs:       []*commonpb.Log{{Sequence: 400}},
			auditByLog: []*auditpb.AuditEntry{{Sequence: 401, Ledgers: []string{"payments"}}},
		}
		_, found, err := (&Client{service: svc}).ResolveAuditEntryByTransaction(context.Background(), "reconciliation", 99)
		require.NoError(t, err)
		require.False(t, found)
		require.Empty(t, svc.getEntryCalls, "a foreign entry is never re-read")
	})
}
