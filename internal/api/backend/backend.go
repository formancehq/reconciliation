package backend

import (
	"context"

	"github.com/formancehq/go-libs/bun/bunpaginate"

	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

//go:generate mockgen -source backend.go -destination backend_generated.go -package backend . Service
type Service interface {
	// Legacy /policies surface — preserved verbatim for backwards compatibility.
	Reconciliation(ctx context.Context, policyID string, req *service.ReconciliationRequest) (*models.Reconciliation, error)
	GetReconciliation(ctx context.Context, id string) (*models.Reconciliation, error)
	ListReconciliations(ctx context.Context, q storage.GetReconciliationsQuery) (*bunpaginate.Cursor[models.Reconciliation], error)

	CreatePolicy(ctx context.Context, req *service.CreatePolicyRequest) (*models.Policy, error)
	DeletePolicy(ctx context.Context, id string) error
	GetPolicy(ctx context.Context, id string) (*models.Policy, error)
	ListPolicies(ctx context.Context, q storage.GetPoliciesQuery) (*bunpaginate.Cursor[models.Policy], error)

	// V1 — Rule
	CreateRule(ctx context.Context, req *service.CreateRuleRequest) (*models.Rule, error)
	GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error)
	ListRules(ctx context.Context, q storage.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error)
	PatchRule(ctx context.Context, id uuid.UUID, patch storage.RulePatch) error
	DeleteRule(ctx context.Context, id uuid.UUID) error
	EvaluateRule(ctx context.Context, id uuid.UUID, req service.EvaluateRuleRequest) (*models.Evaluation, error)

	// V1 — Evaluation
	GetEvaluation(ctx context.Context, id uuid.UUID) (*models.Evaluation, error)
	ListEvaluations(ctx context.Context, q storage.GetEvaluationsQuery) (*bunpaginate.Cursor[models.Evaluation], error)

	// V1 — Incident
	GetIncident(ctx context.Context, id uuid.UUID) (*models.Incident, error)
	ListIncidents(ctx context.Context, q storage.GetIncidentsQuery) (*bunpaginate.Cursor[models.Incident], error)
	AckIncident(ctx context.Context, id uuid.UUID, req *service.AckIncidentRequest) (*models.Incident, error)
	ResolveIncident(ctx context.Context, id uuid.UUID, req *service.ResolveIncidentRequest) (*models.Incident, error)
	AcceptIncident(ctx context.Context, id uuid.UUID, req *service.AcceptIncidentRequest) (*models.Incident, error)
}

type Backend interface {
	GetService() Service
}

type DefaultBackend struct {
	service Service
}

func (d DefaultBackend) GetService() Service {
	return d.service
}

func NewDefaultBackend(service Service) Backend {
	return &DefaultBackend{
		service: service,
	}
}
