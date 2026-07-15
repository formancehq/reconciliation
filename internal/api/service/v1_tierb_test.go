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
		Cadence:      models.CadenceDaily,
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

// A pool balance that moves between the scout read and the kernel's live
// re-read makes the direct math and the kernel cross-check disagree. That is
// benign pool-`latest` timing, not a contract bug: it must NOT raise a spurious
// engine.error — the direct math (which backs the evidence) wins.
func TestEvaluate_PoolTOCTOU_NoSpuriousEngineError(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	// Scout sees pool -100 → direct drift 0 → PASS; the kernel's later re-read
	// sees -50 → drift 50 → kernel FAIL. Pre-fix this disagreement errored.
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
		t.Fatalf("expected PASS from the scout-based direct math, got %v (error=%q)", ev.Result, ev.Error)
	}
	if store.alertFor(rule.ID, engineErrorFingerprint) != nil {
		t.Fatalf("a benign pool-timing divergence must not open an engine.error alert")
	}
}
