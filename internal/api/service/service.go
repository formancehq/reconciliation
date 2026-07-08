package service

import (
	"context"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"

	sdk "github.com/formancehq/formance-sdk-go/v3"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
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
	// (RFC §4.4.2): the run result is returned from EvaluateRule and its
	// break evidence lives on the alert (Evidence + LastEvaluationID). There
	// is no read surface. CreateEvaluation persists on Postgres today; the
	// ledger-native store treats it as a no-op (step 6a-5).
	CreateEvaluation(ctx context.Context, ev *models.Evaluation) error

	// RecordCapture writes the immutable audit record of an evaluation to the
	// control ledger (ADR-003): a capture transaction carrying the observed
	// snapshot (verdict, evidence, trigger). This is the durable "what reconciled
	// and when" — positive assurance on a pass, break evidence on a fail —
	// recorded independently of the alert lifecycle.
	RecordCapture(ctx context.Context, in store.CaptureInput) error

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

// Service is the orchestrator for the V1 rule/evaluation/alert surface.
type Service struct {
	store     Store
	client    SDKFormance
	engine    *engine.Engine
	templates *templates.Registry
	resolvers engine.Resolvers
}

// NewService constructs the service with all collaborators. V1 work requires
// non-nil engine + templates + resolvers.
func NewService(store Store, client SDKFormance, eng *engine.Engine, reg *templates.Registry, res engine.Resolvers) *Service {
	return &Service{
		store:     store,
		client:    client,
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

// SDKFormance is the SDK surface the service layer + engine consume. It
// intentionally covers BOTH the legacy /policies path (GetPoolBalances on the
// payments v1 namespace) and the V1 engine resolvers (V2GetLedger,
// V2GetBalancesAggregated, V3GetPoolBalancesLatest). Mocks in tests implement
// only the subset they need.
type SDKFormance interface {
	// Legacy reconciliation /policies path
	GetPoolBalances(ctx context.Context, req operations.GetPoolBalancesRequest) (*operations.GetPoolBalancesResponse, error)
	V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error)

	// V1 engine resolvers
	V2GetLedger(ctx context.Context, req operations.V2GetLedgerRequest) (*operations.V2GetLedgerResponse, error)
	V2ListAccounts(ctx context.Context, req operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error)
	V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error)
}

type sdkFormanceClient struct {
	client *sdk.Formance
}

func NewSDKFormance(client *sdk.Formance) *sdkFormanceClient {
	return &sdkFormanceClient{client: client}
}

func (s *sdkFormanceClient) GetPoolBalances(ctx context.Context, req operations.GetPoolBalancesRequest) (*operations.GetPoolBalancesResponse, error) {
	return s.client.Payments.V1.GetPoolBalances(ctx, req)
}

func (s *sdkFormanceClient) V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error) {
	return s.client.Ledger.V2.GetBalancesAggregated(ctx, req)
}

func (s *sdkFormanceClient) V2GetLedger(ctx context.Context, req operations.V2GetLedgerRequest) (*operations.V2GetLedgerResponse, error) {
	return s.client.Ledger.V2.GetLedger(ctx, req)
}

func (s *sdkFormanceClient) V2ListAccounts(ctx context.Context, req operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error) {
	return s.client.Ledger.V2.ListAccounts(ctx, req)
}

func (s *sdkFormanceClient) V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	return s.client.Payments.V3.GetPoolBalancesLatest(ctx, req)
}

var _ SDKFormance = (*sdkFormanceClient)(nil)
