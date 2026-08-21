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

	// V1 — Alert
	GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error)
	ListAlerts(ctx context.Context, q storage.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error)
	ListAlertEvents(ctx context.Context, alertID uuid.UUID, q storage.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error)
	AckAlert(ctx context.Context, id uuid.UUID, req *service.AckAlertRequest) (*models.Alert, error)
	ResolveAlert(ctx context.Context, id uuid.UUID, req *service.ResolveAlertRequest) (*models.Alert, error)
	AcceptAlert(ctx context.Context, id uuid.UUID, req *service.AcceptAlertRequest) (*models.Alert, error)
	SnoozeAlert(ctx context.Context, id uuid.UUID, req *service.SnoozeAlertRequest) (*models.Alert, error)
	UnsnoozeAlert(ctx context.Context, id uuid.UUID, req *service.UnsnoozeAlertRequest) (*models.Alert, error)

	// Audit journal — the tamper-evident record of everything above.
	ListAuditEntries(ctx context.Context, f storage.AuditEntryFilters, afterSeq int64, limit int) ([]models.AuditEntry, int64, error)
	GetAuditEntry(ctx context.Context, sequence int64) (*models.AuditEntry, error)
	ChainHead(ctx context.Context) (int64, []byte, error)
	VerifyChain(ctx context.Context, fromSeq, toSeq int64) (*models.ChainVerification, error)
	ListRuleRevisions(ctx context.Context, ruleID uuid.UUID) ([]models.RuleRevision, error)
	GetRuleRevision(ctx context.Context, ruleID uuid.UUID, revision int64) (*models.RuleRevision, error)
	ListClosures(ctx context.Context) ([]models.Closure, error)
	GetClosure(ctx context.Context, id int64) (*models.Closure, error)
	CloseJournal(ctx context.Context) (*models.Closure, error)
	VerifyClosure(ctx context.Context, id int64) (*models.Closure, bool, string, error)
	AttestationsForPeriod(ctx context.Context, periodID string) ([]storage.PeriodAttestation, error)
	GetClosingSchedule(ctx context.Context) (string, error)
	SetClosingSchedule(ctx context.Context, cron string) error
	ListVerificationKeys(ctx context.Context) ([]storage.VerificationKey, error)
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
