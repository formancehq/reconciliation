package ledger

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/auditpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/signaturepb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/stretchr/testify/require"
)

// signedBatchEntry is an audit entry signed over a batch of requests.
func signedBatchEntry(t *testing.T, sequence uint64, requests ...*servicepb.Request) *auditpb.AuditEntry {
	t.Helper()

	payload, err := (&servicepb.ApplyBatch{Requests: requests}).MarshalVT()
	require.NoError(t, err)

	return &auditpb.AuditEntry{
		Sequence:   sequence,
		Ledgers:    []string{auditLedger},
		OrderCount: uint32(len(requests)),
		Signature:  &signaturepb.SignedApplyBatch{KeyId: "k", Signature: []byte("sig"), Payload: payload},
	}
}

func TestListAuditEntriesLabelsEachWrite(t *testing.T) {
	t.Parallel()

	occurredAt := time.Date(2026, 10, 2, 13, 30, 0, 0, time.UTC)
	ruleID := "28839b29-69b7-4855-b71d-1ffa625ebe3a"
	transition := `{"type":"reconciliation.alert.acknowledged","alertID":"b02f327f-e58c-4a45-b090-9f5b6733c56d","newStatus":"ACKNOWLEDGED"}`

	ack := signedBatchEntry(t, 325, &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
		Ledger: auditLedger,
		Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{
			ScriptReference: &servicepb.ScriptReference{Name: schema.NumscriptAlertMove, Version: schema.NumscriptVersion},
			Metadata: metadataValues(map[string]string{
				schema.ActivityMetaKind:            "alert.acknowledged",
				schema.ActivityMetaRule:            ruleID,
				schema.ActivityMetaContractVersion: "2",
				schema.ActivityMetaAt:              occurredAt.Format(time.RFC3339Nano),
				schema.ActivityMetaPayload:         transition,
			}),
		}}},
	}}})

	provisioning := signedBatchEntry(t, 61,
		&servicepb.Request{Type: &servicepb.Request_CreateIndex{CreateIndex: &servicepb.CreateIndexRequest{
			Ledger: auditLedger,
			Id:     commonpb.TxBuiltinIndexID(commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ADDRESS),
		}}},
		&servicepb.Request{Type: &servicepb.Request_SaveNumscript{SaveNumscript: &servicepb.SaveNumscriptRequest{
			Ledger: auditLedger, Name: "capture", Version: "2.0.0",
		}}},
	)

	unsigned := &auditpb.AuditEntry{Sequence: 3, Ledgers: []string{auditLedger}, OrderCount: 1}

	// An activity written before contract versions were stamped.
	legacy := signedBatchEntry(t, 12, &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
		Ledger: auditLedger,
		Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{
			Metadata: metadataValues(map[string]string{schema.ActivityMetaKind: "rule.created", schema.ActivityMetaRule: ruleID}),
		}}},
	}}})

	entries, err := dialBufconn(t, &auditServer{entries: []*auditpb.AuditEntry{ack, provisioning, unsigned, legacy}}).
		ListAuditEntries(context.Background(), auditLedger, AuditScopeAll, 10)
	require.NoError(t, err)
	require.Len(t, entries, 4)

	t.Run("an action carries its activity", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, &AuditActivity{
			Kind:            "alert.acknowledged",
			RuleID:          ruleID,
			ContractVersion: 2,
			OccurredAt:      occurredAt,
			Payload:         json.RawMessage(transition),
		}, entries[0].Activity)
		require.Equal(t, []AuditAction{{Kind: "Create transaction", Ledger: auditLedger, Detail: "alert_move v2.0.0"}}, entries[0].Actions)
	})

	t.Run("a provisioning write names what it provisioned", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, entries[1].Activity)
		require.Equal(t, []AuditAction{
			{Kind: "Create index", Ledger: auditLedger, Detail: "transaction address"},
			{Kind: "Register numscript", Ledger: auditLedger, Detail: "capture v2.0.0"},
		}, entries[1].Actions)
	})

	t.Run("an activity without a contract version is V1, as the timeline reads it", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, 1, entries[3].Activity.ContractVersion)
	})

	t.Run("an unsigned entry has nothing to decode", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, entries[2].Activity)
		require.Empty(t, entries[2].Actions)
	})
}
