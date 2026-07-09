package backend

import (
	"context"

	"github.com/formancehq/go-libs/bun/bunpaginate"

	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
)

//go:generate mockgen -source backend.go -destination backend_generated.go -package backend . Service
type Service interface {
	// V1 — Rule
	CreateRule(ctx context.Context, req *service.CreateRuleRequest) (*models.Rule, error)
	GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error)
	ListRules(ctx context.Context, q store.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error)
	PatchRule(ctx context.Context, id uuid.UUID, patch store.RulePatch) error
	DeleteRule(ctx context.Context, id uuid.UUID) error
	EvaluateRule(ctx context.Context, id uuid.UUID, req service.EvaluateRuleRequest) (*models.Evaluation, error)
	ListCaptures(ctx context.Context, ruleID uuid.UUID, q store.GetCapturesQuery) (*bunpaginate.Cursor[models.Capture], error)

	// V1 — Alert
	GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error)
	ListAlerts(ctx context.Context, q store.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error)
	ListAlertEvents(ctx context.Context, alertID uuid.UUID, q store.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error)
	AckAlert(ctx context.Context, id uuid.UUID, req *service.AckAlertRequest) (*models.Alert, error)
	ResolveAlert(ctx context.Context, id uuid.UUID, req *service.ResolveAlertRequest) (*models.Alert, error)
	AcceptAlert(ctx context.Context, id uuid.UUID, req *service.AcceptAlertRequest) (*models.Alert, error)
	SnoozeAlert(ctx context.Context, id uuid.UUID, req *service.SnoozeAlertRequest) (*models.Alert, error)
	UnsnoozeAlert(ctx context.Context, id uuid.UUID, req *service.UnsnoozeAlertRequest) (*models.Alert, error)
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
