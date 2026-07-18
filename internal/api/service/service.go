package service

import (
	"context"
	"errors"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"

	"github.com/formancehq/reconciliation/internal/contractversion"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
)

// Store is the storage surface the Service depends on — the V1
// Rule/Evaluation/Alert methods, one mockable boundary for tests.
type Store interface {
	Ping() error

	// V1 — Rule
	CreateRule(ctx context.Context, rule *models.Rule) error
	GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error)
	DeleteRule(ctx context.Context, id uuid.UUID) error
	PatchRule(ctx context.Context, id uuid.UUID, patch store.RulePatch) error
	ListRules(ctx context.Context, q store.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error)

	// V1 — Evaluation. Evaluations are not a durable, queryable entity
	// (RFC §4.4.2): the run result is returned from EvaluateRule and failing
	// evidence lives on the alert (Evidence + LastEvaluationID). There
	// is no read surface. CreateEvaluation persists on Postgres today; the
	// ledger-native store treats it as a no-op (step 6a-5).
	CreateEvaluation(ctx context.Context, ev *models.Evaluation) error

	// RecordCapture writes the immutable audit record of an evaluation to the
	// control ledger (ADR-003): a capture transaction carrying the observed
	// snapshot (verdict, evidence, trigger). This is the durable "what reconciled
	// and when": all failures plus passes retained to document automatic alert
	// resolution, recorded independently of the alert lifecycle.
	RecordCapture(ctx context.Context, in store.CaptureInput) error

	// ListCaptures returns a rule's evaluation history — the captures recorded by
	// RecordCapture, read back live from the control ledger (they are ledger
	// transactions, so no event sink is needed), newest first.
	ListCaptures(ctx context.Context, ruleID uuid.UUID, q store.GetCapturesQuery) (*bunpaginate.Cursor[models.Capture], error)

	// V1 — Alert
	OpenOrUpdateAlert(ctx context.Context, in store.OpenAlertInput) (*store.OpenAlertResult, error)
	AutoResolveAlert(ctx context.Context, ruleID uuid.UUID, fingerprint, periodID string, evaluationID uuid.UUID, at time.Time) (*models.Alert, error)
	ListActiveAlertFingerprints(ctx context.Context, ruleID uuid.UUID, periodID string) ([]string, error)
	AckAlert(ctx context.Context, id uuid.UUID, ack *models.Ack) (*models.Alert, error)
	ResolveAlertManual(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Alert, error)
	AcceptAlert(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Alert, error)
	SnoozeAlert(ctx context.Context, id uuid.UUID, until time.Time, by, note string) (*models.Alert, error)
	UnsnoozeAlert(ctx context.Context, id uuid.UUID, by string) (*models.Alert, error)
	GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error)
	ListAlerts(ctx context.Context, q store.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error)
	ListAlertEvents(ctx context.Context, alertID uuid.UUID, q store.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error)
}

type ruleActivityStore interface {
	ListRuleActivities(context.Context, uuid.UUID, store.GetRuleActivitiesQuery) (*bunpaginate.Cursor[models.RuleActivity], error)
}

func (s *Service) ListRuleActivities(ctx context.Context, ruleID uuid.UUID, q store.GetRuleActivitiesQuery) (*bunpaginate.Cursor[models.RuleActivity], error) {
	activityStore, ok := s.store.(ruleActivityStore)
	if !ok {
		return nil, errors.New("rule activity history is not supported by this store")
	}
	if version, ok := contractversion.FromContext(ctx); ok {
		q.Options.Options.ContractVersion = &version
	}
	return activityStore.ListRuleActivities(ctx, ruleID, q)
}

// Service is the orchestrator for the V1 rule/evaluation/alert surface.
type Service struct {
	store     Store
	engine    *engine.Engine
	templates *templates.Registry
	resolvers engine.Resolvers
}

// NewService constructs the service with all collaborators. V1 work requires
// non-nil engine + templates + resolvers.
func NewService(store Store, eng *engine.Engine, reg *templates.Registry, res engine.Resolvers) *Service {
	return &Service{
		store:     store,
		engine:    eng,
		templates: reg,
		resolvers: res,
	}
}

// inTx runs fn against the store. The ledger-native store is idempotent and
// has no cross-store transaction boundary, so there is nothing to open or
// commit here — fn runs directly against the shared store. Kept as a seam so
// callers that want all-or-nothing semantics have a single place to express
// it if a transactional store returns.
func (s *Service) inTx(ctx context.Context, fn func(ctx context.Context, store Store) error) error {
	return fn(ctx, s.store)
}
