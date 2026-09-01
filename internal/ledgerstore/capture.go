package ledgerstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
)

// RecordCapture writes an immutable capture transaction to the control ledger for
// one evaluation (ADR-003): a self-describing snapshot (verdict, trigger, evidence)
// on a COMMITTED_TRANSACTION plus a CAPTURE counter unit in the (rule, period)
// bucket. The transaction is receipt-signed and append-only — the durable audit
// record complementing the alert lifecycle, with every evaluated outcome.
// Idempotent per (rule, period, evaluation): a gRPC
// retransmit dedups; a genuinely new evaluation gets a fresh key.
func (s *LedgerStore) RecordCapture(ctx context.Context, in store.CaptureInput) error {
	md := map[string]*commonpb.MetadataValue{
		schema.CaptureMetaType:            strVal(schema.CaptureType),
		schema.CaptureMetaRule:            strVal(in.RuleID.String()),
		schema.CaptureMetaTmpl:            strVal(in.TemplateKind),
		schema.CaptureMetaPer:             strVal(in.PeriodID),
		schema.CaptureMetaEval:            strVal(in.EvaluationID.String()),
		schema.CaptureMetaAt:              strVal(in.CapturedAt.UTC().Format(time.RFC3339Nano)),
		schema.CaptureMetaVdt:             strVal(in.Verdict),
		schema.CaptureMetaTrig:            strVal(in.Trigger),
		schema.CaptureMetaContractVersion: strVal(strconv.Itoa(int(in.ContractVersion.Effective()))),
		schema.CaptureMetaRevision:        strVal(in.RuleRevision),
		schema.CaptureMetaStartedAt:       strVal(in.StartedAt.UTC().Format(time.RFC3339Nano)),
		schema.CaptureMetaPIT:             strVal(in.PIT.UTC().Format(time.RFC3339Nano)),
		schema.CaptureMetaResult:          strVal(string(in.Result)),
		schema.CaptureMetaError:           strVal(in.Error),
	}
	if len(in.Evidence) > 0 {
		md[schema.CaptureMetaEvi] = strVal(string(in.Evidence))
	}

	rule := in.RuleID.String()
	activity, err := activityMetadata("evaluation.completed", in.RuleID, in.ContractVersion, in.RuleRevision, in.EvaluationID.String(), in.CapturedAt, in)
	if err != nil {
		return fmt.Errorf("record capture activity: %w", err)
	}
	for k, v := range activity {
		md[k] = v
	}
	if err := s.client.CreateTransaction(ctx, ledger.CreateTransactionInput{
		Ledger:        s.controlLedger,
		ScriptName:    schema.NumscriptCapture,
		ScriptVersion: schema.NumscriptVersion,
		Vars: map[string]string{
			schema.VarCapturePool:  schema.CapturePool(rule),
			schema.VarCapture:      schema.CaptureAccount(rule, in.PeriodID),
			schema.VarActivityPool: schema.ActivityPool(rule),
			schema.VarActivity:     schema.ActivityAccount(rule),
		},
		TxMetadata:     md,
		IdempotencyKey: alertActionKey("capture", rule, in.PeriodID, in.EvaluationID.String()),
	}); err != nil {
		return fmt.Errorf("record capture for rule %s period %s: %w", rule, in.PeriodID, err)
	}

	return nil
}

// ListCaptures returns a rule's evaluation history — the immutable capture
// transactions recorded per evaluation (ADR-003), newest first. Unlike the alert
// transition timeline (ListAlertEvents, which is sink-gated), captures are
// first-class ledger transactions, so they are read live: we list transactions on
// the rule's capture bucket address. An empty q period lists every period of the
// rule; a set period scopes to that (rule, period) bucket.
//
// Like ListAlerts this collects the matching set and offset-slices client-side
// (F23) — bounded to one rule's captures, but a long-lived continuous rule
// accumulates one capture per evaluation, so a native ListTransactions cursor is
// the follow-up if that volume bites.
func (s *LedgerStore) ListCaptures(ctx context.Context, ruleID uuid.UUID, q store.GetCapturesQuery) (*bunpaginate.Cursor[models.Capture], error) {
	prefix := schema.CaptureRulePrefix(ruleID.String())
	if p := q.Options.Options.Period; p != "" {
		prefix = schema.CaptureAccount(ruleID.String(), p)
	}

	captures := make([]models.Capture, 0, 64)
	if err := s.client.ListTransactionsFunc(ctx, s.controlLedger, schema.FilterAddressPrefix(prefix), func(tx *commonpb.Transaction) error {
		if c, ok := captureFromTransaction(tx); ok {
			if version := q.Options.Options.ContractVersion; version != nil && c.ContractVersion.Effective() != *version {
				return nil
			}
			captures = append(captures, c)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("list captures for rule %s: %w", ruleID, err)
	}

	// Newest first: captured_at descending, tx id as a stable tie-breaker.
	slices.SortFunc(captures, func(a, b models.Capture) int {
		if c := b.CapturedAt.Compare(a.CapturedAt); c != 0 {
			return c
		}

		return int(b.TransactionID) - int(a.TransactionID)
	})

	data, hasMore := paginateSlice(captures, q.Offset, q.PageSize)

	return offsetCursor(bunpaginate.OffsetPaginatedQuery[store.PaginatedQueryOptions[store.CapturesFilters]](q), data, hasMore), nil
}

// captureFromTransaction rebuilds a Capture from a capture transaction's
// self-describing metadata. Returns ok=false for a transaction that is not a
// capture (defensive: the bucket only ever holds captures). All capture metadata
// is written as strings, so the round-trip reads cleanly via getStr.
func captureFromTransaction(tx *commonpb.Transaction) (models.Capture, bool) {
	md := tx.GetMetadata()
	if getStr(md, schema.CaptureMetaType) != schema.CaptureType {
		return models.Capture{}, false
	}

	c := models.Capture{
		TransactionID:   tx.GetId(),
		ContractVersion: models.ContractVersionV1,
		PeriodID:        getStr(md, schema.CaptureMetaPer),
		TemplateKind:    getStr(md, schema.CaptureMetaTmpl),
		Verdict:         getStr(md, schema.CaptureMetaVdt),
		Trigger:         getStr(md, schema.CaptureMetaTrig),
		RuleRevision:    getStr(md, schema.CaptureMetaRevision),
		Result:          models.EvaluationResult(getStr(md, schema.CaptureMetaResult)),
		Error:           getStr(md, schema.CaptureMetaError),
	}
	if version, err := strconv.Atoi(getStr(md, schema.CaptureMetaContractVersion)); err == nil && version > 0 {
		c.ContractVersion = models.ContractVersion(version)
	}

	if id, err := uuid.Parse(getStr(md, schema.CaptureMetaRule)); err == nil {
		c.RuleID = id
	}

	if id, err := uuid.Parse(getStr(md, schema.CaptureMetaEval)); err == nil {
		c.EvaluationID = id
	}

	if t, err := time.Parse(time.RFC3339Nano, getStr(md, schema.CaptureMetaAt)); err == nil {
		c.CapturedAt = t.UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, getStr(md, schema.CaptureMetaStartedAt)); err == nil {
		c.StartedAt = t.UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, getStr(md, schema.CaptureMetaPIT)); err == nil {
		c.PIT = t.UTC()
	}

	if ev := getStr(md, schema.CaptureMetaEvi); ev != "" {
		c.Evidence = json.RawMessage(ev)
	}

	return c, true
}
