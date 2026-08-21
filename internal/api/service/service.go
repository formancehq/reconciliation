package service

import (
	"context"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"

	sdk "github.com/formancehq/formance-sdk-go/v3"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	domain "github.com/formancehq/reconciliation/internal/reconciliation"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
)

// Store is the storage surface the Service depends on. Both the legacy
// /policies methods and the V1 Rule/Evaluation/Alert methods live here so
// there's one mockable boundary for tests.
type Store interface {
	Ping(ctx context.Context) error

	// Legacy /policies path
	CreatePolicy(ctx context.Context, policy *models.Policy) error
	DeletePolicy(ctx context.Context, id uuid.UUID) error
	GetPolicy(ctx context.Context, id uuid.UUID) (*models.Policy, error)
	ListPolicies(ctx context.Context, q storage.GetPoliciesQuery) (*bunpaginate.Cursor[models.Policy], error)
	CreateReconciliation(ctx context.Context, reco *models.Reconciliation) error
	GetReconciliation(ctx context.Context, id uuid.UUID) (*models.Reconciliation, error)
	ListReconciliations(ctx context.Context, q storage.GetReconciliationsQuery) (*bunpaginate.Cursor[models.Reconciliation], error)

	// V1 — Rule
	CreateRule(ctx context.Context, rule *models.Rule) error
	GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error)
	AssertRuleRevision(ctx context.Context, id uuid.UUID, revision int64) error
	DeleteRule(ctx context.Context, id uuid.UUID) error
	PatchRule(ctx context.Context, id uuid.UUID, patch storage.RulePatch) error
	ListRules(ctx context.Context, q storage.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error)

	// V1 — Evaluation
	CreateEvaluation(ctx context.Context, ev *models.Evaluation) error
	GetEvaluation(ctx context.Context, id uuid.UUID) (*models.Evaluation, error)
	ListEvaluations(ctx context.Context, q storage.GetEvaluationsQuery) (*bunpaginate.Cursor[models.Evaluation], error)

	// V1 — Alert
	OpenOrUpdateAlert(ctx context.Context, in storage.OpenAlertInput) (*storage.OpenAlertResult, error)
	AutoResolveAlert(ctx context.Context, ruleID uuid.UUID, fingerprint, periodID string, evaluationID uuid.UUID, at time.Time) (*models.Alert, error)
	ListActiveAlertFingerprints(ctx context.Context, ruleID uuid.UUID, periodID string) ([]string, error)
	AckAlert(ctx context.Context, id uuid.UUID, ack *models.Ack) (*models.Alert, error)
	ResolveAlertManual(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Alert, error)
	AcceptAlert(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Alert, error)
	SnoozeAlert(ctx context.Context, id uuid.UUID, until time.Time, by, note string) (*models.Alert, error)
	UnsnoozeAlert(ctx context.Context, id uuid.UUID, by string) (*models.Alert, error)
	GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error)
	ListAlerts(ctx context.Context, q storage.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error)
	ListAlertEvents(ctx context.Context, alertID uuid.UUID, q storage.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error)

	// Audit journal. Sealing is not here: it needs a transaction spanning several
	// statements, which an in-memory Store cannot provide, so it is reached
	// through the transactional capability asserted in CloseJournal — the same
	// pattern the evaluation runner uses.
	ListAuditEntries(ctx context.Context, f storage.AuditEntryFilters, afterSeq int64, limit int) ([]models.AuditEntry, int64, error)
	GetAuditEntry(ctx context.Context, sequence int64) (*models.AuditEntry, error)
	ChainHead(ctx context.Context) (int64, []byte, error)
	VerifyChain(ctx context.Context, fromSeq, toSeq int64) (*models.ChainVerification, error)
	ListRuleRevisions(ctx context.Context, ruleID uuid.UUID) ([]models.RuleRevision, error)
	GetRuleRevision(ctx context.Context, ruleID uuid.UUID, revision int64) (*models.RuleRevision, error)
	ListClosures(ctx context.Context) ([]models.Closure, error)
	GetClosure(ctx context.Context, id int64) (*models.Closure, error)
	AttestationsForPeriod(ctx context.Context, periodID string) ([]storage.PeriodAttestation, error)
	VerifyClosureSignature(ctx context.Context, closure *models.Closure) (bool, string, error)
	GetClosingSchedule(ctx context.Context) (string, error)
	SetClosingSchedule(ctx context.Context, cron string) error
	ListVerificationKeys(ctx context.Context) ([]storage.VerificationKey, error)
}

// Service is the orchestrator for both the legacy /policies path and the V1
// rule/evaluation/alert surface. V1-only callers can ignore `client`;
// legacy /policies callers can ignore `engine` and `templates`.
type Service struct {
	store     Store
	client    SDKFormance
	engine    *engine.Engine
	templates *templates.Registry
	runner    *domain.Runner
}

// NewService constructs the service with all collaborators. V1 work requires
// non-nil engine + templates + resolvers; legacy /policies calls will still
// work if those are nil (kept for tests / partial wiring).
func NewService(store Store, client SDKFormance, eng *engine.Engine, reg *templates.Registry, res engine.Resolvers) *Service {
	return &Service{
		store:     store,
		client:    client,
		engine:    eng,
		templates: reg,
		runner:    domain.NewRunner(store, eng, reg, res),
	}
}

// SDKFormance is the SDK surface the service layer + engine consume. It
// intentionally covers BOTH the legacy /policies path (GetPoolBalances on the
// payments v1 namespace) and the V1 engine resolvers (V2GetLedger,
// V2GetBalancesAggregated, V3GetPoolBalances{,Latest}). Mocks in tests implement
// only the subset they need.
type SDKFormance interface {
	// Legacy reconciliation /policies path
	GetPoolBalances(ctx context.Context, req operations.GetPoolBalancesRequest) (*operations.GetPoolBalancesResponse, error)
	V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error)

	// V1 engine resolvers
	V2GetLedger(ctx context.Context, req operations.V2GetLedgerRequest) (*operations.V2GetLedgerResponse, error)
	V2ListAccounts(ctx context.Context, req operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error)
	// V3GetPoolBalances is the payments v3 point-in-time pool read
	// (GET /v3/pools/{id}/balances?at=). V3GetPoolBalancesLatest is the
	// current-snapshot read (…/balances/latest). Both are genuine: `at` returns
	// the pool balance valid at that instant; the difference is that a read past
	// the pool's last balance movement is empty on the PIT route but non-empty on
	// latest (see ADR-002).
	V3GetPoolBalances(ctx context.Context, req operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error)
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

func (s *sdkFormanceClient) V3GetPoolBalances(ctx context.Context, req operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error) {
	return s.client.Payments.V3.GetPoolBalances(ctx, req)
}

func (s *sdkFormanceClient) V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	return s.client.Payments.V3.GetPoolBalancesLatest(ctx, req)
}

var _ SDKFormance = (*sdkFormanceClient)(nil)
