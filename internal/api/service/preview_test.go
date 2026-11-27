package service

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/stretchr/testify/require"
)

// requireNothingStored checks that a preview left the store untouched.
func requireNothingStored(t *testing.T, s *fakeV1Store) {
	t.Helper()
	require.Empty(t, s.rules, "a preview creates no rule")
	require.Empty(t, s.evaluations, "a preview records no evaluation")
	require.Empty(t, s.captures, "a preview records no capture")
	require.Empty(t, s.alerts, "a preview opens no alert")
	require.Empty(t, s.events, "a preview writes no alert event")
}

func TestPreviewRule_FailsOnTheCurrentBalancesAndStoresNothing(t *testing.T) {
	ledger := &orchestrationLedger{balances: map[string]map[string]*big.Int{
		"sub":     {"USD/2": big.NewInt(350)},
		"control": {"USD/2": big.NewInt(300)},
	}}
	svc, fakeStore := newOrchestrationService(t, ledger)

	preview, err := svc.PreviewRule(context.Background(), &PreviewRuleRequest{
		TemplateKind: models.TemplateBalanceEquation,
		TemplateSpec: paritySpec(t, "sub", "control", `"q"`, map[string]int64{"USD/2": 20}),
	})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationFail, preview.Result)
	require.False(t, preview.PIT.IsZero())
	outcomes := decodeRetainedOutcomes(t, preview.Evidence)
	require.Len(t, outcomes, 1)
	require.Equal(t, "50", outcomes[0].Evidence.Residual, "the evidence is the capture format")
	requireNothingStored(t, fakeStore)

	ledger.balances["control"]["USD/2"] = big.NewInt(340)
	preview, err = svc.PreviewRule(context.Background(), &PreviewRuleRequest{
		TemplateKind: models.TemplateBalanceEquation,
		TemplateSpec: paritySpec(t, "sub", "control", `"q"`, map[string]int64{"USD/2": 20}),
	})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationPass, preview.Result, "a preview reads the balances of the moment")
	requireNothingStored(t, fakeStore)
}

func TestPreviewRule_ReadFailureIsAnErrorPreview(t *testing.T) {
	ledger := &orchestrationLedger{failErr: errors.New("ledger unavailable")}
	svc, fakeStore := newOrchestrationService(t, ledger)

	preview, err := svc.PreviewRule(context.Background(), &PreviewRuleRequest{
		TemplateKind: models.TemplateBalanceEquation,
		TemplateSpec: paritySpec(t, "sub", "control", `"q"`, nil),
	})
	require.NoError(t, err, "a read failure is the preview's verdict, not a request error")
	require.Equal(t, models.EvaluationError, preview.Result)
	require.Contains(t, preview.Error, "ledger unavailable")
	requireNothingStored(t, fakeStore)
}

func TestPreviewRule_RejectsAnInvalidConfiguration(t *testing.T) {
	svc, fakeStore := newOrchestrationService(t, &orchestrationLedger{})

	for name, req := range map[string]*PreviewRuleRequest{
		"no template":  {TemplateSpec: json.RawMessage(`{}`)},
		"no spec":      {TemplateKind: models.TemplateBalanceEquation},
		"invalid spec": {TemplateKind: models.TemplateBalanceEquation, TemplateSpec: json.RawMessage(`{"sources":[]}`)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.PreviewRule(context.Background(), req)
			require.ErrorIs(t, err, templates.ErrInvalidSpec)
		})
	}
	requireNothingStored(t, fakeStore)
}
