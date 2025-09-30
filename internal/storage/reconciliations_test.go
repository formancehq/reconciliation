package storage

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var (
	r1 = &models.Reconciliation{
		ID:                   uuid.New(),
		PolicyID:             p1.ID,
		CreatedAt:            time.Now().Add(-1 * time.Hour),
		ReconciledAtLedger:   time.Now().Add(-1 * time.Hour),
		ReconciledAtPayments: time.Now().Add(-1 * time.Hour),
		Status:               models.ReconciliationOK,
		LedgerBalances: map[string]*big.Int{
			"USD": big.NewInt(100),
			"EUR": big.NewInt(200),
		},
		PaymentsBalances: map[string]*big.Int{
			"USD": big.NewInt(100),
			"EUR": big.NewInt(200),
		},
		DriftBalances: map[string]*big.Int{
			"USD": big.NewInt(0),
			"EUR": big.NewInt(0),
		},
		Error: "",
	}

	r2 = &models.Reconciliation{
		ID:                   uuid.New(),
		PolicyID:             p1.ID,
		CreatedAt:            time.Now().Add(-2 * time.Hour),
		ReconciledAtLedger:   time.Now().Add(-2 * time.Hour),
		ReconciledAtPayments: time.Now().Add(-2 * time.Hour),
		Status:               models.ReconciliationOK,
		LedgerBalances: map[string]*big.Int{
			"USD": big.NewInt(100),
			"EUR": big.NewInt(200),
		},
	}

	r3 = &models.Reconciliation{
		ID:                   uuid.New(),
		PolicyID:             p2.ID,
		CreatedAt:            time.Now().Add(-3 * time.Hour),
		ReconciledAtLedger:   time.Now().Add(-3 * time.Hour),
		ReconciledAtPayments: time.Now().Add(-3 * time.Hour),
		Status:               models.ReconciliationNotOK,
		Error:                "different number of assets",
	}
)

func insertReconciliations(t *testing.T, store *Storage, reconciliations ...*models.Reconciliation) {
	for _, reconciliation := range reconciliations {
		err := store.CreateReconciation(context.Background(), reconciliation)
		require.NoError(t, err)
	}
}

func TestReconciliationList(t *testing.T) {
	store := newStore(t)

	insertPolicies(t, store, p1, p2, p3)
	insertReconciliations(t, store, r1, r2, r3)

	t.Run("nominal", func(t *testing.T) {
		reconciliations, err := store.ListReconciliations(context.Background(), GetReconciliationsQuery{})
		require.NoError(t, err)
		require.Len(t, reconciliations.Data, 3)
	})

	t.Run("with query id", func(t *testing.T) {
		reconciliations, err := store.ListReconciliations(context.Background(), GetReconciliationsQuery{
			Options: PaginatedQueryOptions[ReconciliationsFilters]{
				QueryBuilder: query.Match("id", r1.ID.String()),
			},
		})

		require.NoError(t, err)
		require.Len(t, reconciliations.Data, 1)
		require.Equal(t, reconciliations.Data[0].ID, r1.ID)
	})

	t.Run("with query unknown id", func(t *testing.T) {
		reconciliations, err := store.ListReconciliations(context.Background(), GetReconciliationsQuery{
			Options: PaginatedQueryOptions[ReconciliationsFilters]{
				QueryBuilder: query.Match("id", uuid.New().String()),
			},
		})

		require.NoError(t, err)
		require.Len(t, reconciliations.Data, 0)
	})

	t.Run("with query status ok", func(t *testing.T) {
		reconciliations, err := store.ListReconciliations(context.Background(), GetReconciliationsQuery{
			Options: PaginatedQueryOptions[ReconciliationsFilters]{
				QueryBuilder: query.Match("status", models.ReconciliationOK),
			},
		})
		require.NoError(t, err)
		require.Len(t, reconciliations.Data, 2)
		require.Equal(t, reconciliations.Data[0].ID, r1.ID)
		require.Equal(t, reconciliations.Data[1].ID, r2.ID)
	})

	t.Run("with query status not ok", func(t *testing.T) {
		reconciliations, err := store.ListReconciliations(context.Background(), GetReconciliationsQuery{
			Options: PaginatedQueryOptions[ReconciliationsFilters]{
				QueryBuilder: query.Match("status", models.ReconciliationNotOK),
			},
		})

		require.NoError(t, err)
		require.Len(t, reconciliations.Data, 1)
		require.Equal(t, reconciliations.Data[0].ID, r3.ID)
	})

	t.Run("with query policyID", func(t *testing.T) {
		reconciliations, err := store.ListReconciliations(context.Background(), GetReconciliationsQuery{
			Options: PaginatedQueryOptions[ReconciliationsFilters]{
				QueryBuilder: query.Match("policyID", p1.ID.String()),
			},
		})
		require.NoError(t, err)
		require.Len(t, reconciliations.Data, 2)
		require.Equal(t, reconciliations.Data[0].ID, r1.ID)
		require.Equal(t, reconciliations.Data[1].ID, r2.ID)
	})

	t.Run("with query unknown policyID", func(t *testing.T) {
		reconciliations, err := store.ListReconciliations(context.Background(), GetReconciliationsQuery{
			Options: PaginatedQueryOptions[ReconciliationsFilters]{
				QueryBuilder: query.Match("policyID", uuid.New().String()),
			},
		})
		require.NoError(t, err)
		require.Len(t, reconciliations.Data, 0)
	})

	t.Run("with query createdAt", func(t *testing.T) {
		reconciliations, err := store.ListReconciliations(context.Background(), GetReconciliationsQuery{
			Options: PaginatedQueryOptions[ReconciliationsFilters]{
				QueryBuilder: query.Lt("createdAt", r1.CreatedAt),
			},
		})
		require.NoError(t, err)
		require.Len(t, reconciliations.Data, 2)
		require.Equal(t, reconciliations.Data[0].ID, r2.ID)
		require.Equal(t, reconciliations.Data[1].ID, r3.ID)
	})

}
