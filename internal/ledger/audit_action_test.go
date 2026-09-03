package ledger

import (
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/raftcmdpb"
	"github.com/stretchr/testify/require"
)

// marshalOrder round-trips a raftcmdpb.Order through its business-intent bytes,
// mirroring what the ledger stores on an AuditItem.SerializedOrder.
func marshalOrder(t *testing.T, order *raftcmdpb.Order) []byte {
	t.Helper()
	b, err := order.MarshalVT()
	require.NoError(t, err)
	return b
}

func TestDecodeAuditAction(t *testing.T) {
	t.Parallel()

	ledgerScoped := func(ledger string, ls *raftcmdpb.LedgerScopedOrder) *raftcmdpb.Order {
		ls.Ledger = ledger
		return &raftcmdpb.Order{Type: &raftcmdpb.Order_LedgerScoped{LedgerScoped: ls}}
	}

	t.Run("apply batch is named without decoding its numscript", func(t *testing.T) {
		t.Parallel()
		order := ledgerScoped("reconciliation", &raftcmdpb.LedgerScopedOrder{
			Payload: &raftcmdpb.LedgerScopedOrder_Apply{Apply: &raftcmdpb.LedgerApplyOrder{}},
		})
		got, ok := decodeAuditAction(marshalOrder(t, order))
		require.True(t, ok)
		require.Equal(t, AuditAction{Kind: "Apply batch", Ledger: "reconciliation"}, got)
	})

	t.Run("save numscript carries name and version", func(t *testing.T) {
		t.Parallel()
		order := ledgerScoped("reconciliation", &raftcmdpb.LedgerScopedOrder{
			Payload: &raftcmdpb.LedgerScopedOrder_SaveNumscript{
				SaveNumscript: &raftcmdpb.SaveNumscriptOrder{Name: "activity", Version: "2.0.0"},
			},
		})
		got, ok := decodeAuditAction(marshalOrder(t, order))
		require.True(t, ok)
		require.Equal(t, AuditAction{Kind: "Register numscript", Ledger: "reconciliation", Detail: "activity v2.0.0"}, got)
	})

	t.Run("save numscript without a version omits it", func(t *testing.T) {
		t.Parallel()
		order := ledgerScoped("reconciliation", &raftcmdpb.LedgerScopedOrder{
			Payload: &raftcmdpb.LedgerScopedOrder_SaveNumscript{
				SaveNumscript: &raftcmdpb.SaveNumscriptOrder{Name: "alert_move"},
			},
		})
		got, ok := decodeAuditAction(marshalOrder(t, order))
		require.True(t, ok)
		require.Equal(t, "alert_move", got.Detail)
	})

	t.Run("register signing key is system-scoped, no ledger", func(t *testing.T) {
		t.Parallel()
		order := &raftcmdpb.Order{Type: &raftcmdpb.Order_SystemScoped{
			SystemScoped: &raftcmdpb.SystemScopedOrder{
				Payload: &raftcmdpb.SystemScopedOrder_RegisterSigningKey{
					RegisterSigningKey: &raftcmdpb.RegisterSigningKeyOrder{},
				},
			},
		}}
		got, ok := decodeAuditAction(marshalOrder(t, order))
		require.True(t, ok)
		require.Equal(t, AuditAction{Kind: "Register signing key"}, got)
	})

	t.Run("empty and undecodable bytes are skipped", func(t *testing.T) {
		t.Parallel()
		_, ok := decodeAuditAction(nil)
		require.False(t, ok)
		_, ok = decodeAuditAction([]byte{0xff, 0xff, 0xff})
		require.False(t, ok)
	})
}
