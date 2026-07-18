package service

import (
	"context"
	"errors"
	"testing"

	"github.com/formancehq/reconciliation/internal/contractversion"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestContractVersionTemplateCatalogsAreDisjoint(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateTemplateContract(models.ContractVersionV1, models.TemplateSourceParity))
	require.Error(t, validateTemplateContract(models.ContractVersionV1, models.TemplateBalanceEquation))
	require.NoError(t, validateTemplateContract(models.ContractVersionV2, models.TemplateBalanceEquation))
	require.NoError(t, validateTemplateContract(models.ContractVersionV2, models.TemplateExchangeRateBounds))
	require.NoError(t, validateTemplateContract(models.ContractVersionV2, models.TemplateSourceConsensus))
	require.NoError(t, validateTemplateContract(models.ContractVersionV2, models.TemplateCoverageRatioBounds))
	require.Error(t, validateTemplateContract(models.ContractVersionV2, models.TemplateSourceParity))
}

func TestRuleOperationsHideCrossVersionIDs(t *testing.T) {
	t.Parallel()

	svc, st := newOrchestrationService(t, &orchestrationLedger{})
	id := uuid.New()
	st.rules[id] = &models.Rule{ID: id, ContractVersion: models.ContractVersionV2, Name: "v2", Enabled: true}
	ctx := contractversion.WithContext(context.Background(), models.ContractVersionV1)

	_, err := svc.GetRule(ctx, id)
	require.ErrorIs(t, err, store.ErrNotFound)

	name := "mutated"
	err = svc.PatchRule(ctx, id, store.RulePatch{Name: &name})
	require.ErrorIs(t, err, store.ErrNotFound)
	require.Equal(t, "v2", st.rules[id].Name)

	err = svc.DeleteRule(ctx, id)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.Contains(t, st.rules, id)

	_, err = svc.EvaluateRule(ctx, id, EvaluateRuleRequest{})
	require.ErrorIs(t, err, store.ErrNotFound)

	_, err = svc.ListCaptures(ctx, id, store.GetCapturesQuery{})
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestAlertOperationsHideCrossVersionIDs(t *testing.T) {
	t.Parallel()

	svc, st := newOrchestrationService(t, &orchestrationLedger{})
	id := uuid.New()
	st.alerts[id] = &models.Alert{ID: id, ContractVersion: models.ContractVersionV2, Status: models.AlertOpen}
	ctx := contractversion.WithContext(context.Background(), models.ContractVersionV1)

	_, err := svc.GetAlert(ctx, id)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = svc.AckAlert(ctx, id, &AckAlertRequest{By: "ops"})
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = svc.ResolveAlert(ctx, id, &ResolveAlertRequest{By: "ops"})
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = svc.AcceptAlert(ctx, id, &AcceptAlertRequest{By: "ops", Note: "accepted"})
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = svc.UnsnoozeAlert(ctx, id, &UnsnoozeAlertRequest{By: "ops"})
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = svc.ListAlertEvents(ctx, id, store.GetAlertEventsQuery{})
	require.ErrorIs(t, err, store.ErrNotFound)

	require.True(t, errors.Is(err, store.ErrNotFound))
}
