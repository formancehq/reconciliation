package service

import (
	"context"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/models"
	domain "github.com/formancehq/reconciliation/internal/reconciliation"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

type EvaluateRuleRequest = domain.EvaluateRequest

func (s *Service) EvaluateRule(ctx context.Context, ruleID uuid.UUID, req EvaluateRuleRequest) (*models.Evaluation, error) {
	return s.runner.Evaluate(ctx, ruleID, req)
}

func (s *Service) GetEvaluation(ctx context.Context, id uuid.UUID) (*models.Evaluation, error) {
	return s.store.GetEvaluation(ctx, id)
}

func (s *Service) ListEvaluations(ctx context.Context, q storage.GetEvaluationsQuery) (*bunpaginate.Cursor[models.Evaluation], error) {
	return s.store.ListEvaluations(ctx, q)
}
