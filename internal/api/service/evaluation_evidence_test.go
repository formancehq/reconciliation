package service

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/stretchr/testify/require"
)

// The evidence a two-source parity check retains. The V1 shape carried
// left/right scalars and an int64 tolerance; the balance_equation that replaced
// it carries a signed residual and string amounts throughout (V2 encodes amounts
// as strings so JSON cannot lose precision).
type retainedOutcomeEvidence struct {
	Asset            string `json:"asset"`
	Residual         string `json:"residual"`
	AbsoluteResidual string `json:"absoluteResidual"`
	Tolerance        string `json:"tolerance"`
}

type retainedOutcome struct {
	Fingerprint string                  `json:"fingerprint"`
	Passed      bool                    `json:"passed"`
	Evidence    retainedOutcomeEvidence `json:"evidence"`
}

func decodeRetainedOutcomes(t *testing.T, evidence json.RawMessage) []retainedOutcome {
	t.Helper()
	var outcomes []retainedOutcome
	require.NoError(t, json.Unmarshal(evidence, &outcomes))
	return outcomes
}

func retainedOutcomesByFingerprint(t *testing.T, evidence json.RawMessage) map[string]retainedOutcome {
	t.Helper()
	byFingerprint := map[string]retainedOutcome{}
	for _, outcome := range decodeRetainedOutcomes(t, evidence) {
		_, duplicate := byFingerprint[outcome.Fingerprint]
		require.False(t, duplicate, "duplicate retained outcome for %s", outcome.Fingerprint)
		byFingerprint[outcome.Fingerprint] = outcome
	}
	return byFingerprint
}

func TestEvaluateRule_RetainsFreshSuccessfulResolutionEvidence(t *testing.T) {
	ctx := context.Background()
	ledger := &orchestrationLedger{balances: map[string]map[string]*big.Int{
		"sub":     {"USD/2": big.NewInt(350)},
		"control": {"USD/2": big.NewInt(300)},
	}}
	svc, fakeStore := newOrchestrationService(t, ledger)
	rule := mustCreateRule(t, svc, paritySpec(t, "sub", "control", `"q"`, map[string]int64{"USD/2": 20}))

	failingEvaluation, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationFail, failingEvaluation.Result)
	require.Len(t, fakeStore.captures, 1)

	failing := decodeRetainedOutcomes(t, fakeStore.captures[0].Evidence)
	require.Len(t, failing, 1)
	require.Equal(t, retainedOutcome{
		Fingerprint: "asset:USD/2",
		Passed:      false,
		Evidence: retainedOutcomeEvidence{
			Asset:            "USD/2",
			AbsoluteResidual: "50",
			Residual:         "50",
			Tolerance:        "20",
		},
	}, failing[0])
	require.JSONEq(t, string(failingEvaluation.Evidence), string(fakeStore.captures[0].Evidence))

	alert := fakeStore.activeFor(rule.ID, "asset:USD/2")
	require.NotNil(t, alert)
	alertID := alert.ID

	// Resolve with values observed in a later evaluation. The successful
	// evidence must come from this run, not the alert's older failure evidence.
	ledger.balances["control"]["USD/2"] = big.NewInt(340)
	passingEvaluation, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationPass, passingEvaluation.Result)
	require.Len(t, fakeStore.captures, 2)

	successful := decodeRetainedOutcomes(t, fakeStore.captures[1].Evidence)
	require.Len(t, successful, 1)
	require.Equal(t, retainedOutcome{
		Fingerprint: "asset:USD/2",
		Passed:      true,
		Evidence: retainedOutcomeEvidence{
			Asset:            "USD/2",
			AbsoluteResidual: "10",
			Residual:         "10",
			Tolerance:        "20",
		},
	}, successful[0])
	require.NotEqual(t, failing[0].Evidence.Residual, successful[0].Evidence.Residual)
	require.NotEqual(t, failing[0].Evidence.AbsoluteResidual, successful[0].Evidence.AbsoluteResidual)
	require.JSONEq(t, string(passingEvaluation.Evidence), string(fakeStore.captures[1].Evidence))
	require.Nil(t, fakeStore.activeFor(rule.ID, "asset:USD/2"))
	require.Equal(t, models.AlertResolved, fakeStore.alertFor(rule.ID, "asset:USD/2").Status)
	require.Len(t, fakeStore.eventsFor(alertID), 2)

	// A later successful evaluation sees no active transition to document. Its
	// observation is still retained even though it creates no alert event.
	retryEvaluation, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationPass, retryEvaluation.Result)
	require.Len(t, fakeStore.captures, 3)
	require.Len(t, decodeRetainedOutcomes(t, fakeStore.captures[2].Evidence), 1)
	require.JSONEq(t, string(passingEvaluation.Evidence), string(retryEvaluation.Evidence))
	require.Len(t, fakeStore.eventsFor(alertID), 2)
}

func TestEvaluateRule_MixedFailureAndSuccessfulResolutionEvidence(t *testing.T) {
	ctx := context.Background()
	ledger := &orchestrationLedger{balances: map[string]map[string]*big.Int{
		"sub": {
			"USD/2": big.NewInt(350),
			"EUR/2": big.NewInt(90),
			"GBP/2": big.NewInt(10),
		},
		"control": {
			"USD/2": big.NewInt(300),
			"EUR/2": big.NewInt(50),
			"GBP/2": big.NewInt(10),
		},
	}}
	svc, fakeStore := newOrchestrationService(t, ledger)
	rule := mustCreateRule(t, svc, paritySpec(t, "sub", "control", `"q"`, nil))

	first, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationFail, first.Result)
	firstEvidence := retainedOutcomesByFingerprint(t, fakeStore.captures[0].Evidence)
	require.Len(t, firstEvidence, 3)
	require.Contains(t, firstEvidence, "asset:USD/2")
	require.Contains(t, firstEvidence, "asset:EUR/2")
	require.True(t, firstEvidence["asset:GBP/2"].Passed)

	// USD now reconciles, EUR remains broken with a new observed value, and GBP
	// remains an unrelated pass. The overall verdict stays FAIL.
	ledger.balances["control"]["USD/2"] = big.NewInt(350)
	ledger.balances["control"]["EUR/2"] = big.NewInt(60)
	second, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationFail, second.Result)
	require.Len(t, fakeStore.captures, 2)

	mixed := retainedOutcomesByFingerprint(t, fakeStore.captures[1].Evidence)
	require.Len(t, mixed, 3)
	require.True(t, mixed["asset:USD/2"].Passed)
	require.Equal(t, "0", mixed["asset:USD/2"].Evidence.AbsoluteResidual)
	require.False(t, mixed["asset:EUR/2"].Passed)
	require.Equal(t, "30", mixed["asset:EUR/2"].Evidence.AbsoluteResidual)
	require.True(t, mixed["asset:GBP/2"].Passed)
	require.Nil(t, fakeStore.activeFor(rule.ID, "asset:USD/2"))
	require.NotNil(t, fakeStore.activeFor(rule.ID, "asset:EUR/2"))
}
