package service

import (
	"context"
	"math/big"
	"net/http"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"

	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/shared"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

type mockSDKFormanceClient struct {
	ledgerVersion    string
	ledgerBalances   map[string]*big.Int
	paymentsVersion  string
	paymentsBalances map[string]*big.Int
}

func newMockSDKFormanceClient(
	ledgerVersion string,
	ledgerBalances map[string]*big.Int,
	paymentsVersion string,
	paymentsBalances map[string]*big.Int,
) *mockSDKFormanceClient {
	return &mockSDKFormanceClient{
		ledgerVersion:    ledgerVersion,
		ledgerBalances:   ledgerBalances,
		paymentsVersion:  paymentsVersion,
		paymentsBalances: paymentsBalances,
	}
}

func (s *mockSDKFormanceClient) GetPoolBalances(ctx context.Context, req operations.GetPoolBalancesRequest) (*operations.GetPoolBalancesResponse, error) {
	poolBalances := make([]shared.PoolBalance, 0, len(s.paymentsBalances))
	for assetCode, balance := range s.paymentsBalances {
		poolBalances = append(poolBalances, shared.PoolBalance{
			Amount: balance,
			Asset:  assetCode,
		})
	}

	return &operations.GetPoolBalancesResponse{
		PoolBalancesResponse: &shared.PoolBalancesResponse{
			Data: shared.PoolBalances{
				Balances: poolBalances,
			},
		},
		StatusCode: http.StatusOK,
	}, nil
}

func (s *mockSDKFormanceClient) V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error) {
	balances := make(map[string]*big.Int)
	for assetCode, balance := range s.ledgerBalances {
		balances[assetCode] = balance
	}

	return &operations.V2GetBalancesAggregatedResponse{
		StatusCode: http.StatusOK,
		V2AggregateBalancesResponse: &shared.V2AggregateBalancesResponse{
			Data: balances,
		},
	}, nil
}

// V1 engine stubs — the legacy /policies tests don't exercise these, so the
// mock returns benign empty responses. Engine-specific tests use the engine
// package's own fakes.
func (s *mockSDKFormanceClient) V2GetLedger(ctx context.Context, req operations.V2GetLedgerRequest) (*operations.V2GetLedgerResponse, error) {
	return &operations.V2GetLedgerResponse{
		StatusCode:          http.StatusOK,
		V2GetLedgerResponse: &shared.V2GetLedgerResponse{Data: shared.V2Ledger{Name: req.Ledger}},
	}, nil
}

func (s *mockSDKFormanceClient) V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	// Both the legacy /policies path and the V1 ledger_vs_pool_drift template
	// route through this method since the legacy GetPoolBalances PIT endpoint
	// returns empty under payments v3. Mirror the V1 mock's behaviour so tests
	// reach the same balances regardless of which surface they exercise.
	balances := make([]shared.V3PoolBalance, 0, len(s.paymentsBalances))
	for assetCode, balance := range s.paymentsBalances {
		balances = append(balances, shared.V3PoolBalance{
			Amount: balance,
			Asset:  assetCode,
		})
	}
	return &operations.V3GetPoolBalancesLatestResponse{
		StatusCode: http.StatusOK,
		V3PoolBalancesResponse: &shared.V3PoolBalancesResponse{
			Data: balances,
		},
	}, nil
}

type mockStore struct {
}

func newMockStore() *mockStore {
	return &mockStore{}
}

func (s *mockStore) Ping() error {
	return nil
}

func (s *mockStore) CreatePolicy(ctx context.Context, policy *models.Policy) error {
	return nil
}

func (s *mockStore) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (s *mockStore) GetPolicy(ctx context.Context, id uuid.UUID) (*models.Policy, error) {
	return &models.Policy{
		ID:             id,
		CreatedAt:      time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
		Name:           "test",
		LedgerName:     "default",
		LedgerQuery:    map[string]interface{}{},
		PaymentsPoolID: uuid.New(),
	}, nil
}

func (s *mockStore) ListPolicies(ctx context.Context, q storage.GetPoliciesQuery) (*bunpaginate.Cursor[models.Policy], error) {
	return nil, nil
}

func (s *mockStore) CreateReconciation(ctx context.Context, reco *models.Reconciliation) error {
	return nil
}

func (s *mockStore) GetReconciliation(ctx context.Context, id uuid.UUID) (*models.Reconciliation, error) {
	return nil, nil
}

func (s *mockStore) ListReconciliations(ctx context.Context, q storage.GetReconciliationsQuery) (*bunpaginate.Cursor[models.Reconciliation], error) {
	return nil, nil
}

// --- V1 stubs --------------------------------------------------------------
// Legacy reconciliation_test.go does not exercise the V1 surface; these stubs
// exist solely so mockStore satisfies the Store interface. V1 services have
// their own dedicated mocks in v1_orchestration_test.go.

func (s *mockStore) CreateRule(context.Context, *models.Rule) error { return nil }
func (s *mockStore) GetRule(context.Context, uuid.UUID) (*models.Rule, error) {
	return nil, nil
}
func (s *mockStore) DeleteRule(context.Context, uuid.UUID) error { return nil }
func (s *mockStore) PatchRule(context.Context, uuid.UUID, storage.RulePatch) error {
	return nil
}
func (s *mockStore) ListRules(context.Context, storage.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error) {
	return nil, nil
}
func (s *mockStore) CreateEvaluation(context.Context, *models.Evaluation) error {
	return nil
}
func (s *mockStore) GetEvaluation(context.Context, uuid.UUID) (*models.Evaluation, error) {
	return nil, nil
}
func (s *mockStore) ListEvaluations(context.Context, storage.GetEvaluationsQuery) (*bunpaginate.Cursor[models.Evaluation], error) {
	return nil, nil
}
func (s *mockStore) OpenOrUpdateIncident(context.Context, storage.OpenIncidentInput) (*storage.OpenIncidentResult, error) {
	return nil, nil
}
func (s *mockStore) AutoResolveIncident(context.Context, uuid.UUID, string, uuid.UUID, time.Time) (*models.Incident, error) {
	return nil, nil
}
func (s *mockStore) ListActiveIncidentFingerprints(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}
func (s *mockStore) AckIncident(context.Context, uuid.UUID, *models.Ack) (*models.Incident, error) {
	return nil, nil
}
func (s *mockStore) ResolveIncidentManual(context.Context, uuid.UUID, *models.Resolution) (*models.Incident, error) {
	return nil, nil
}
func (s *mockStore) AcceptIncident(context.Context, uuid.UUID, *models.Resolution) (*models.Incident, error) {
	return nil, nil
}
func (s *mockStore) GetIncident(context.Context, uuid.UUID) (*models.Incident, error) {
	return nil, nil
}
func (s *mockStore) ListIncidents(context.Context, storage.GetIncidentsQuery) (*bunpaginate.Cursor[models.Incident], error) {
	return nil, nil
}

var _ Store = (*mockStore)(nil)
