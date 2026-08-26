package service

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
)

func mustCreateDailyDriftRule(t *testing.T, svc *Service) *models.Rule {
	t.Helper()
	rule, err := svc.CreateRule(context.Background(), &CreateRuleRequest{
		Name:         "daily-drift",
		TemplateKind: models.TemplateLedgerVsPoolDrift,
		TemplateSpec: driftSpec(t, "buildr", `"q"`, "pool", nil),
		Severity:     models.SeverityHigh,
		PeriodType:   models.PeriodTypeDaily,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	return rule
}

// A periodic rule's engine.error meta-alert (opened in the continuous period)
// must auto-resolve once the engine runs successfully — the period-scoped sweep
// never reaches the continuous scope, so the success path resolves it
// explicitly. Without that, the meta-alert would stay OPEN forever.
func TestEvaluate_EngineErrorResolvesOnRecovery(t *testing.T) {
	l := &orchestrationLedger{failErr: errors.New("ledger upstream timeout"), current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-100)}} // nets to 0 → PASS once healthy
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateDailyDriftRule(t, svc)

	// Eval 1: engine fails → engine.error meta-alert opens in the continuous period.
	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("eval1: %v", err)
	}
	meta := store.alertFor(rule.ID, engineErrorFingerprint)
	if meta == nil || meta.Status != models.AlertOpen {
		t.Fatalf("expected an OPEN engine.error alert after failure, got %+v", meta)
	}

	// Eval 2: engine recovers and the evaluation passes.
	l.failErr = nil
	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("eval2: %v", err)
	}
	meta = store.alertFor(rule.ID, engineErrorFingerprint)
	if meta == nil || meta.Status != models.AlertResolved {
		t.Fatalf("expected engine.error auto-resolved after recovery, got %+v", meta)
	}
}

// An evaluation just past a period boundary with a positive safety margin reads
// the PREVIOUS period's data, so its alert must be bucketed to that period (the
// margin-adjusted PIT), not the raw PIT — otherwise a later rerun of the real
// period opens a second, disconnected alert.
func TestEvaluate_PeriodIDUsesMarginAdjustedPIT(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-50)}} // drift 50 → FAIL → opens an alert
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateDailyDriftRule(t, svc)

	// 10s past midnight UTC with a 30s margin → resolvers read 2026-03-14 23:59:40.
	pit := time.Date(2026, 3, 15, 0, 0, 10, 0, time.UTC)
	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: pit, SafetyMargin: 30 * time.Second}); err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}

	var alert *models.Alert
	for _, a := range store.alerts {
		if a.Fingerprint == "asset:USD/2" {
			alert = a
			break
		}
	}
	if alert == nil {
		t.Fatalf("expected a drift alert to be opened")
	}
	if alert.PeriodID != "2026-03-14" {
		t.Fatalf("expected alert scoped to margin-adjusted period 2026-03-14, got %q", alert.PeriodID)
	}
}

// CEL evaluates the snapshot already returned by the scout read. A later pool
// movement therefore cannot create a second-read disagreement or a spurious
// engine.error.
func TestEvaluate_PoolTOCTOU_NoSpuriousEngineError(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	// The scout sees pool -100 and snapshot CEL evaluates drift 0 → PASS. The
	// fake would return -50 on a second read, which must never happen.
	p := &orchestrationPayments{
		current:    map[string]*big.Int{"USD/2": big.NewInt(-100)},
		afterFirst: map[string]*big.Int{"USD/2": big.NewInt(-50)},
	}
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))

	ev, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}
	if ev.Result != models.EvaluationPass {
		t.Fatalf("expected PASS from snapshot CEL, got %v (error=%q)", ev.Result, ev.Error)
	}
	if p.calls != 1 {
		t.Fatalf("expected exactly one Payments read, got %d", p.calls)
	}
	if store.alertFor(rule.ID, engineErrorFingerprint) != nil {
		t.Fatalf("a benign pool-timing divergence must not open an engine.error alert")
	}
}
