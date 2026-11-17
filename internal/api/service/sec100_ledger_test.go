package service

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/formancehq/formance-sdk-go/v3"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/shared"
	"github.com/formancehq/reconciliation/internal/ledgername"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sec100InvalidLegacyNames = []string{
	"", ".", "..", "main/../../payments", "main%2f..%2fauth",
	"main?next=1", "main#fragment", "main\\other", "main\n",
	strings.Repeat("a", 65),
}

func TestSEC100_LegacyPolicyRejectsInvalidLedgerBeforeStorage(t *testing.T) {
	for _, name := range sec100InvalidLegacyNames {
		t.Run(name, func(t *testing.T) {
			req := &CreatePolicyRequest{
				Name: "local policy", LedgerName: name,
				PaymentsPoolID: "3b61a0f0-0000-4000-8000-000000000000",
			}
			require.Error(t, req.Validate())
			// A nil store makes any attempt to persist this request fail the test.
			s := &Service{}
			policy, err := s.CreatePolicy(t.Context(), req)
			require.ErrorIs(t, err, ErrValidation)
			require.Nil(t, policy)
		})
	}
}

func TestSEC100_LegacyStoredLedgerRejectedBeforeSDK(t *testing.T) {
	for _, name := range sec100InvalidLegacyNames {
		t.Run(name, func(t *testing.T) {
			// Stored policies bypass creation validation. No SDK client is needed:
			// rejection must happen before any outbound call.
			s := &Service{}
			balances, err := s.getAccountsAggregatedBalance(t.Context(), name, nil, time.Now())
			require.ErrorIs(t, err, ledgername.ErrInvalid)
			require.Nil(t, balances)
		})
	}
}

func TestSEC100_LegacySupportedLedgerUsesIntendedSDKRoute(t *testing.T) {
	const name = "tenant:prod.eu-west-1"
	req := &CreatePolicyRequest{Name: "local policy", LedgerName: name, PaymentsPoolID: uuid.NewString()}
	require.NoError(t, req.Validate())
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Equal(t, "/api/ledger/v2/"+name+"/aggregate/balances", r.URL.EscapedPath())
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"USD/2":100}}`))
	}))
	defer srv.Close()
	s := &Service{client: NewSDKFormance(sdk.New(sdk.WithServerURL(srv.URL), sdk.WithClient(srv.Client())))}
	balances, err := s.getAccountsAggregatedBalance(t.Context(), name, map[string]interface{}{}, time.Now())
	require.NoError(t, err)
	require.Equal(t, map[string]*big.Int{"USD/2": big.NewInt(100)}, balances)
	require.EqualValues(t, 1, requests.Load())
}

func TestSEC100_LegacyUpstreamErrorDetailsArePrivate(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"errorCode":"INTERNAL","errorMessage":"fake upstream secret: PostgresError"}`))
			}))
			defer srv.Close()
			s := &Service{client: NewSDKFormance(sdk.New(sdk.WithServerURL(srv.URL), sdk.WithClient(srv.Client())))}
			_, err := s.getAccountsAggregatedBalance(t.Context(), "main", nil, time.Now())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "fake upstream secret")
			require.NotContains(t, err.Error(), "PostgresError")
		})
	}
}

type sec100StoredPolicyStore struct {
	Store
	policy *models.Policy
}

func (s *sec100StoredPolicyStore) GetPolicy(context.Context, uuid.UUID) (*models.Policy, error) {
	return s.policy, nil
}

type sec100StoredPolicySDK struct {
	SDKFormance
	ledgerCalls atomic.Int32
}

func (s *sec100StoredPolicySDK) V2GetBalancesAggregated(context.Context, operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error) {
	s.ledgerCalls.Add(1)
	return &operations.V2GetBalancesAggregatedResponse{}, nil
}

func (s *sec100StoredPolicySDK) V3GetPoolBalances(context.Context, operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error) {
	return &operations.V3GetPoolBalancesResponse{
		StatusCode:             http.StatusOK,
		V3PoolBalancesResponse: &shared.V3PoolBalancesResponse{},
	}, nil
}

func TestSEC100_LegacyReconciliationRejectsStoredUnsafeLedger(t *testing.T) {
	client := &sec100StoredPolicySDK{}
	s := &Service{
		store: &sec100StoredPolicyStore{policy: &models.Policy{
			LedgerName: "main/../../payments", PaymentsPoolID: uuid.New(),
		}},
		client: client,
	}
	result, err := s.Reconciliation(t.Context(), uuid.NewString(), &ReconciliationRequest{
		ReconciledAtLedger: time.Now().Add(-time.Hour), ReconciledAtPayments: time.Now().Add(-time.Hour),
	})
	require.ErrorIs(t, err, ledgername.ErrInvalid)
	require.Nil(t, result)
	require.Zero(t, client.ledgerCalls.Load())
}
