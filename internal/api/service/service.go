package service

import (
	"context"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"

	sdk "github.com/formancehq/formance-sdk-go/v3"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
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
}

// Service is the orchestrator for both the legacy /policies path and the V1
// rule/evaluation/alert surface. V1-only callers can ignore `client`;
// legacy /policies callers can ignore `engine` and `templates`.
type Service struct {
	store     Store
	client    SDKFormance
	engine    *engine.Engine
	templates *templates.Registry
	resolvers engine.Resolvers
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
		resolvers: res,
	}
}

// inTx runs fn under a single transaction when the underlying store
// supports it (the real *storage.Storage does), and forwards the store
// through unchanged otherwise. Test fakes typically skip the transaction —
// their in-memory state already gives all-or-nothing semantics naturally,
// so they don't need to implement bun.RunInTx.
//
// The type assertion is defined inline rather than as a named interface
// because keeping it here avoids dragging storage internals (sql.TxOptions
// etc.) into the Store interface — Store stays purely about data shapes.
func (s *Service) inTx(ctx context.Context, fn func(ctx context.Context, store Store) error) error {
	type runner interface {
		RunInTx(ctx context.Context, fn func(ctx context.Context, scoped *storage.Storage) error) error
	}
	if tx, ok := s.store.(runner); ok {
		return tx.RunInTx(ctx, func(ctx context.Context, scoped *storage.Storage) error {
			return fn(ctx, scoped)
		})
	}
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
