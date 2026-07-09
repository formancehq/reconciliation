package service

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/stretchr/testify/require"
)

// TestEvaluateRule_RecordsCapture verifies EvaluateRule writes an immutable
// capture (ADR-003) for every run — verdict "pass" on a clean reconcile, "fail"
// on a break — with the trigger propagated from the request (defaulting to manual).
func TestEvaluateRule_RecordsCapture(t *testing.T) {
	ctx := context.Background()

	t.Run("pass with scheduled trigger", func(t *testing.T) {
		l := &orchestrationLedger{balances: map[string]map[string]*big.Int{
			"sub":     {"USD/2": big.NewInt(100)},
			"control": {"USD/2": big.NewInt(100)},
		}}
		svc, store := newOrchestrationService(t, l)
		rule := mustCreateRule(t, svc, paritySpec(t, "sub", "control", `"q"`, nil))

		ev, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now(), Trigger: TriggerScheduled})
		require.NoError(t, err)
		require.Equal(t, models.EvaluationPass, ev.Result)

		require.Len(t, store.captures, 1)
		c := store.captures[0]
		require.Equal(t, rule.ID, c.RuleID)
		require.Equal(t, ev.ID, c.EvaluationID)
		require.Equal(t, string(rule.TemplateKind), c.TemplateKind)
		require.Equal(t, "pass", c.Verdict)
		require.Equal(t, TriggerScheduled, c.Trigger)
	})

	t.Run("fail with default (manual) trigger", func(t *testing.T) {
		l := &orchestrationLedger{balances: map[string]map[string]*big.Int{
			"sub":     {"USD/2": big.NewInt(350)},
			"control": {"USD/2": big.NewInt(300)}, // drift 50
		}}
		svc, store := newOrchestrationService(t, l)
		rule := mustCreateRule(t, svc, paritySpec(t, "sub", "control", `"q"`, nil))

		ev, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()})
		require.NoError(t, err)
		require.Equal(t, models.EvaluationFail, ev.Result)

		require.Len(t, store.captures, 1)
		c := store.captures[0]
		require.Equal(t, "fail", c.Verdict)
		require.Equal(t, TriggerManual, c.Trigger, "an empty trigger defaults to manual")
	})
}
