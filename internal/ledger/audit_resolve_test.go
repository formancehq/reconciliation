package ledger

import (
	"context"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/auditpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/signaturepb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	auditLedger = "reconciliation"
	auditAt     = uint64(1_759_400_000_000_000) // the proposal's instant, unix µs
)

// auditServer serves the three reads the resolver chains: a transaction, the
// audit entries at its instant, and the logs their items point at.
type auditServer struct {
	servicepb.UnimplementedBucketServiceServer

	tx      *commonpb.Transaction
	entries []*auditpb.AuditEntry
	logs    map[uint64]*commonpb.Log
}

func (s *auditServer) GetTransaction(_ context.Context, req *servicepb.GetTransactionRequest) (*servicepb.GetTransactionResponse, error) {
	if s.tx == nil || req.GetTransactionId() != s.tx.GetId() {
		return nil, status.Error(codes.NotFound, "transaction not found")
	}

	return &servicepb.GetTransactionResponse{Transaction: s.tx}, nil
}

func (s *auditServer) ListAuditEntries(req *servicepb.ListAuditEntriesRequest, stream grpc.ServerStreamingServer[auditpb.AuditEntry]) error {
	at := req.GetOptions().GetFilter().GetAudit().GetUintCond().GetMin()
	for _, e := range s.entries {
		if e.GetTimestamp().GetData() != at {
			continue
		}

		listed := e.CloneVT()
		listed.Items = nil // the stream omits items, like the ledger
		if err := stream.Send(listed); err != nil {
			return err
		}
	}

	return nil
}

func (s *auditServer) GetAuditEntry(_ context.Context, req *servicepb.GetAuditEntryRequest) (*auditpb.AuditEntry, error) {
	for _, e := range s.entries {
		if e.GetSequence() == req.GetSequence() {
			return e, nil
		}
	}

	return nil, status.Error(codes.NotFound, "audit entry not found")
}

func (s *auditServer) GetLog(_ context.Context, req *servicepb.GetLogRequest) (*commonpb.Log, error) {
	if l, ok := s.logs[req.GetSequence()]; ok {
		return l, nil
	}

	return nil, status.Error(codes.NotFound, "log not found")
}

func metadataValues(m map[string]string) map[string]*commonpb.MetadataValue {
	out := make(map[string]*commonpb.MetadataValue, len(m))
	for k, v := range m {
		out[k] = &commonpb.MetadataValue{Type: &commonpb.MetadataValue_StringValue{StringValue: v}}
	}

	return out
}

// createdTxLog is the bucket-wide log at sequence that created transaction
// txID on ledgerName.
func createdTxLog(sequence uint64, ledgerName string, txID uint64) *commonpb.Log {
	return &commonpb.Log{Sequence: sequence, Payload: &commonpb.LogPayload{Type: &commonpb.LogPayload_Apply{Apply: &commonpb.ApplyLedgerLog{
		LedgerName: ledgerName,
		Log: &commonpb.LedgerLog{Data: &commonpb.LedgerLogPayload{Payload: &commonpb.LedgerLogPayload_CreatedTransaction{
			CreatedTransaction: &commonpb.CreatedTransaction{Transaction: &commonpb.Transaction{Id: txID}},
		}}},
	}}}}
}

// signedEntry is an audit entry at the shared instant whose single item points
// at logSequence, signed over a batch creating a transaction with signedMeta.
func signedEntry(t *testing.T, sequence uint64, ledgerName string, logSequence uint64, signedMeta map[string]string) *auditpb.AuditEntry {
	t.Helper()

	payload, err := (&servicepb.ApplyBatch{
		IdempotencyKey: "transition-key",
		Requests: []*servicepb.Request{{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
			Ledger: ledgerName,
			Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{
				CreateTransaction: &servicepb.CreateTransactionPayload{Metadata: metadataValues(signedMeta)},
			}},
		}}}},
	}).MarshalVT()
	require.NoError(t, err)

	return &auditpb.AuditEntry{
		Sequence:    sequence,
		Timestamp:   &commonpb.Timestamp{Data: auditAt},
		Ledgers:     []string{ledgerName},
		OrderCount:  1,
		Items:       []*auditpb.AuditItem{{LogSequence: logSequence}},
		Idempotency: &commonpb.Idempotency{Key: "transition-key"},
		Signature:   &signaturepb.SignedApplyBatch{KeyId: "k", Signature: []byte("sig"), Payload: payload},
	}
}

// controlTx is control-ledger transaction 7, inserted at the shared instant.
func controlTx(meta map[string]string) *commonpb.Transaction {
	return &commonpb.Transaction{Id: 7, InsertedAt: &commonpb.Timestamp{Data: auditAt}, Metadata: metadataValues(meta)}
}

func TestResolveAuditEntryByTransaction(t *testing.T) {
	t.Parallel()

	alert := map[string]string{"alertId": "a-1"}

	resolve := func(t *testing.T, srv *auditServer) (AuditEntryInfo, bool, error) {
		t.Helper()

		return dialBufconn(t, srv).ResolveAuditEntryByTransaction(context.Background(), auditLedger, 7)
	}

	t.Run("the entry whose log created the transaction resolves", func(t *testing.T) {
		t.Parallel()

		got, found, err := resolve(t, &auditServer{
			tx:      controlTx(alert),
			entries: []*auditpb.AuditEntry{signedEntry(t, 9, auditLedger, 42, alert)},
			logs:    map[uint64]*commonpb.Log{42: createdTxLog(42, auditLedger, 7)},
		})

		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, uint64(9), got.Sequence)
		require.Equal(t, "transition-key", got.IdempotencyKey)
	})

	t.Run("a foreign-ledger entry at the same instant is skipped", func(t *testing.T) {
		t.Parallel()

		got, found, err := resolve(t, &auditServer{
			tx: controlTx(alert),
			entries: []*auditpb.AuditEntry{
				signedEntry(t, 8, "payments", 41, alert),
				signedEntry(t, 9, auditLedger, 42, alert),
			},
			logs: map[uint64]*commonpb.Log{
				41: createdTxLog(41, "payments", 7),
				42: createdTxLog(42, auditLedger, 7),
			},
		})

		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, uint64(9), got.Sequence)
	})

	t.Run("an entry at the instant whose log created another transaction is not a match", func(t *testing.T) {
		t.Parallel()

		_, found, err := resolve(t, &auditServer{
			tx:      controlTx(alert),
			entries: []*auditpb.AuditEntry{signedEntry(t, 9, auditLedger, 42, alert)},
			logs:    map[uint64]*commonpb.Log{42: createdTxLog(42, auditLedger, 8)},
		})

		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("an unknown transaction resolves to nothing", func(t *testing.T) {
		t.Parallel()

		_, found, err := resolve(t, &auditServer{})

		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("a signed payload carrying another transaction is a mismatch, not a proof", func(t *testing.T) {
		t.Parallel()

		_, found, err := resolve(t, &auditServer{
			tx:      controlTx(alert),
			entries: []*auditpb.AuditEntry{signedEntry(t, 9, auditLedger, 42, map[string]string{"alertId": "a-2"})},
			logs:    map[uint64]*commonpb.Log{42: createdTxLog(42, auditLedger, 7)},
		})

		require.ErrorIs(t, err, ErrAuditEntryMismatch)
		require.False(t, found)
	})
}
