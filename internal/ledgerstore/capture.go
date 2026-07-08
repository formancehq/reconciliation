package ledgerstore

import (
	"context"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/store"
)

// RecordCapture writes an immutable capture transaction to the control ledger for
// one evaluation (ADR-003): a self-describing snapshot (verdict, trigger, evidence)
// on a COMMITTED_TRANSACTION plus a CAPTURE counter unit in the (rule, period)
// bucket. The transaction is receipt-signed and append-only — the durable audit
// record complementing the alert lifecycle (positive assurance on a pass, break
// evidence on a fail). Idempotent per (rule, period, evaluation): a gRPC
// retransmit dedups; a genuinely new evaluation gets a fresh key.
func (s *LedgerStore) RecordCapture(ctx context.Context, in store.CaptureInput) error {
	md := map[string]*commonpb.MetadataValue{
		schema.CaptureMetaType: strVal(schema.CaptureType),
		schema.CaptureMetaRule: strVal(in.RuleID.String()),
		schema.CaptureMetaTmpl: strVal(in.TemplateKind),
		schema.CaptureMetaPer:  strVal(in.PeriodID),
		schema.CaptureMetaEval: strVal(in.EvaluationID.String()),
		schema.CaptureMetaAt:   strVal(in.CapturedAt.UTC().Format(time.RFC3339Nano)),
		schema.CaptureMetaVdt:  strVal(in.Verdict),
		schema.CaptureMetaTrig: strVal(in.Trigger),
	}
	if len(in.Evidence) > 0 {
		md[schema.CaptureMetaEvi] = strVal(string(in.Evidence))
	}

	rule := in.RuleID.String()
	if err := s.client.CreateTransaction(ctx, ledger.CreateTransactionInput{
		Ledger:        s.controlLedger,
		ScriptName:    schema.NumscriptCapture,
		ScriptVersion: schema.NumscriptVersion,
		Vars: map[string]string{
			schema.VarCapturePool: schema.CapturePool(rule),
			schema.VarCapture:     schema.CaptureAccount(rule, in.PeriodID),
		},
		TxMetadata:     md,
		IdempotencyKey: alertActionKey("capture", rule, in.PeriodID, in.EvaluationID.String()),
	}); err != nil {
		return fmt.Errorf("record capture for rule %s period %s: %w", rule, in.PeriodID, err)
	}

	return nil
}
