//go:build it

package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestIntegration_ResolveAuditEntryByTransaction proves an alert event's
// transaction id resolves to the audit entry that committed that transaction.
// Metadata-type writes are interleaved before and between the transactions:
// they take ledger-local log ids but no transaction id, so the two counters
// diverge, as they do on the control ledger after provisioning. Each
// transaction is written under its own idempotency key, which names the
// expected entry.
//
//	go test -tags it -run TestIntegration_ResolveAuditEntryByTransaction ./internal/ledger/...
func TestIntegration_ResolveAuditEntryByTransaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	// Unique per run so the test is isolated on the shared dev ledgers.
	l := "recon-it-audit-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, l) }()

	require.NoError(t, client.CreateLedger(ctx, l, nil, nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))
	require.NoError(t, client.SaveNumscript(ctx, l, "mint", "send [USD/2 100] (\n\tsource = @world\n\tdestination = @a\n)", "1.0.0"))

	declare := func(key string) {
		t.Helper()
		require.NoError(t, client.SetMetadataFieldType(ctx, l, &commonpb.SetMetadataFieldTypeCommand{
			TargetType: commonpb.TargetType_TARGET_TYPE_ACCOUNT,
			Key:        key,
			Type:       commonpb.MetadataType_METADATA_TYPE_STRING,
		}))
	}

	keys := map[string]string{} // step marker → idempotency key
	write := func(step string) {
		t.Helper()
		keys[step] = "recon-it-audit-" + step + "-" + uuid.NewString()
		require.NoError(t, client.CreateTransaction(ctx, CreateTransactionInput{
			Ledger:         l,
			ScriptName:     "mint",
			ScriptVersion:  "1.0.0",
			TxMetadata:     map[string]*commonpb.MetadataValue{"step": {Type: &commonpb.MetadataValue_StringValue{StringValue: step}}},
			IdempotencyKey: keys[step],
		}))
	}

	declare("k1")
	declare("k2")
	declare("k3")
	write("first")
	declare("k4")
	write("second")

	txByStep := map[string]uint64{}
	require.Eventually(t, func() bool {
		clear(txByStep)
		_ = client.ListTransactionsFunc(ctx, l, nil, func(tx *commonpb.Transaction) error {
			txByStep[tx.GetMetadata()["step"].GetStringValue()] = tx.GetId()
			return nil
		})

		return len(txByStep) == 2
	}, 10*time.Second, 200*time.Millisecond, "transactions not visible")

	for step, txID := range txByStep {
		got, found, err := client.ResolveAuditEntryByTransaction(ctx, l, txID)
		require.NoError(t, err, step)
		require.True(t, found, "%s: transaction %d resolved to no entry", step, txID)
		require.Equal(t, keys[step], got.IdempotencyKey, "%s: transaction %d resolved to audit entry %d, not the write that committed it", step, txID, got.Sequence)
		require.Equal(t, "success", got.Outcome, step)
		require.Contains(t, got.Ledgers, l, step)
	}

	_, found, err := client.ResolveAuditEntryByTransaction(ctx, l, 999_999)
	require.NoError(t, err)
	require.False(t, found, "an unknown transaction resolves to no entry")
}

