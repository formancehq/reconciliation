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

func (s *mockSDKFormanceClient) V2ListAccounts(_ context.Context, _ operations.V2ListAccountsRequest) (*operations.V2ListAccountsResponse, error) {
	// Legacy /policies tests don't exercise per-account scope; return empty.
	return &operations.V2ListAccountsResponse{StatusCode: http.StatusOK}, nil
}

func (s *mockSDKFormanceClient) v3PoolBalances() []shared.V3PoolBalance {
	balances := make([]shared.V3PoolBalance, 0, len(s.paymentsBalances))
	for assetCode, balance := range s.paymentsBalances {
		balances = append(balances, shared.V3PoolBalance{
			Amount: balance,
			Asset:  assetCode,
		})
	}
	return balances
}

func (s *mockSDKFormanceClient) V3GetPoolBalances(ctx context.Context, req operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error) {
	// The legacy /policies path reads point-in-time via this method. The mock
	// ignores `req.At` and returns the configured balances — the /policies tests
	// assert the drift logic, not payments-side PIT windowing.
	return &operations.V3GetPoolBalancesResponse{
		StatusCode: http.StatusOK,
		V3PoolBalancesResponse: &shared.V3PoolBalancesResponse{
			Data: s.v3PoolBalances(),
		},
	}, nil
}

func (s *mockSDKFormanceClient) V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	// The V1 ledger_vs_pool_drift template resolves the pool via latest when the
	// evaluation is "as of now"; mirror the same balances so tests reach them
	// regardless of which surface they exercise.
	return &operations.V3GetPoolBalancesLatestResponse{
		StatusCode: http.StatusOK,
		V3PoolBalancesResponse: &shared.V3PoolBalancesResponse{
			Data: s.v3PoolBalances(),
		},
	}, nil
}

type mockStore struct {
}

func newMockStore() *mockStore {
	return &mockStore{}
}

func (s *mockStore) Ping(ctx context.Context) error {
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

func (s *mockStore) CreateReconciliation(ctx context.Context, reco *models.Reconciliation) error {
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
func (s *mockStore) AssertRuleRevision(context.Context, uuid.UUID, int64) error { return nil }
func (s *mockStore) DeleteRule(context.Context, uuid.UUID) error                { return nil }
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
func (s *mockStore) OpenOrUpdateAlert(context.Context, storage.OpenAlertInput) (*storage.OpenAlertResult, error) {
	return nil, nil
}
func (s *mockStore) AutoResolveAlert(context.Context, uuid.UUID, string, string, uuid.UUID, time.Time) (*models.Alert, error) {
	return nil, nil
}
func (s *mockStore) ListActiveAlertFingerprints(context.Context, uuid.UUID, string) ([]string, error) {
	return nil, nil
}
func (s *mockStore) AckAlert(context.Context, uuid.UUID, *models.Ack) (*models.Alert, error) {
	return nil, nil
}
func (s *mockStore) ResolveAlertManual(context.Context, uuid.UUID, *models.Resolution) (*models.Alert, error) {
	return nil, nil
}
func (s *mockStore) AcceptAlert(context.Context, uuid.UUID, *models.Resolution) (*models.Alert, error) {
	return nil, nil
}
func (s *mockStore) SnoozeAlert(context.Context, uuid.UUID, time.Time, string, string) (*models.Alert, error) {
	return nil, nil
}
func (s *mockStore) UnsnoozeAlert(context.Context, uuid.UUID, string) (*models.Alert, error) {
	return nil, nil
}
func (s *mockStore) GetAlert(context.Context, uuid.UUID) (*models.Alert, error) {
	return nil, nil
}
func (s *mockStore) ListAlerts(context.Context, storage.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error) {
	return nil, nil
}
func (s *mockStore) ListAlertEvents(context.Context, uuid.UUID, storage.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error) {
	return nil, nil
}

func (s *mockStore) ListAuditEntries(context.Context, storage.AuditEntryFilters, int64, int) ([]models.AuditEntry, int64, error) {
	return nil, 0, nil
}
func (s *mockStore) GetAuditEntry(context.Context, int64) (*models.AuditEntry, error) {
	return nil, nil
}
func (s *mockStore) ChainHead(context.Context) (int64, []byte, error) {
	return 0, nil, nil
}
func (s *mockStore) VerifyChain(context.Context, int64, int64) (*models.ChainVerification, error) {
	return nil, nil
}
func (s *mockStore) ListRuleRevisions(context.Context, uuid.UUID) ([]models.RuleRevision, error) {
	return nil, nil
}
func (s *mockStore) GetRuleRevision(context.Context, uuid.UUID, int64) (*models.RuleRevision, error) {
	return nil, nil
}
func (s *mockStore) ListPeriodSeals(context.Context) ([]models.PeriodSeal, error) {
	return nil, nil
}
func (s *mockStore) GetPeriodSeal(context.Context, string) (*models.PeriodSeal, error) {
	return nil, nil
}
func (s *mockStore) VerifySealSignature(context.Context, *models.PeriodSeal) (bool, string, error) {
	return false, "", nil
}
func (s *mockStore) ListVerificationKeys(context.Context) ([]storage.VerificationKey, error) {
	return nil, nil
}

var _ Store = (*mockStore)(nil)
