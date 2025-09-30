package storage

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var (
	p1 = &models.Policy{
		ID:         uuid.New(),
		CreatedAt:  time.Now().Add(-1 * time.Hour),
		Name:       "test1",
		LedgerName: "default",
		LedgerQuery: map[string]interface{}{
			"$match": map[string]interface{}{
				"account": "test",
			},
		},
		PaymentsPoolID: uuid.New(),
	}

	p2 = &models.Policy{
		ID:         uuid.New(),
		CreatedAt:  time.Now().Add(-2 * time.Hour),
		Name:       "test2",
		LedgerName: "default",
		LedgerQuery: map[string]interface{}{
			"$match": map[string]interface{}{
				"account": "test",
			},
		},
		PaymentsPoolID: uuid.New(),
	}

	p3 = &models.Policy{
		ID:         uuid.New(),
		CreatedAt:  time.Now().Add(-3 * time.Hour),
		Name:       "test3",
		LedgerName: "default",
		LedgerQuery: map[string]interface{}{
			"$match": map[string]interface{}{
				"account": "test",
			},
		},
		PaymentsPoolID: uuid.New(),
	}
)

func insertPolicies(t *testing.T, store *Storage, policies ...*models.Policy) {
	for _, policy := range policies {
		err := store.CreatePolicy(context.Background(), policy)
		require.NoError(t, err)
	}
}

func TestPolicyList(t *testing.T) {
	store := newStore(t)

	insertPolicies(t, store, p1, p2, p3)

	t.Run("nominal", func(t *testing.T) {
		policies, err := store.ListPolicies(context.Background(), GetPoliciesQuery{})
		require.NoError(t, err)
		require.Len(t, policies.Data, 3)
	})

	t.Run("with query id", func(t *testing.T) {
		policies, err := store.ListPolicies(context.Background(), GetPoliciesQuery{
			Options: PaginatedQueryOptions[PoliciesFilters]{
				QueryBuilder: query.Match("id", p1.ID.String()),
			},
		})

		require.NoError(t, err)
		require.Len(t, policies.Data, 1)
		require.Equal(t, policies.Data[0].ID, p1.ID)
	})

	t.Run("with query unknown id", func(t *testing.T) {
		policies, err := store.ListPolicies(context.Background(), GetPoliciesQuery{
			Options: PaginatedQueryOptions[PoliciesFilters]{
				QueryBuilder: query.Match("id", uuid.New().String()),
			},
		})
		require.NoError(t, err)
		require.Len(t, policies.Data, 0)
	})

	t.Run("with query name", func(t *testing.T) {
		policies, err := store.ListPolicies(context.Background(), GetPoliciesQuery{
			Options: PaginatedQueryOptions[PoliciesFilters]{
				QueryBuilder: query.Match("name", p1.Name),
			},
		})

		require.NoError(t, err)
		require.Len(t, policies.Data, 1)
		require.Equal(t, policies.Data[0].ID, p1.ID)
	})

	t.Run("with query unknown name", func(t *testing.T) {
		policies, err := store.ListPolicies(context.Background(), GetPoliciesQuery{
			Options: PaginatedQueryOptions[PoliciesFilters]{
				QueryBuilder: query.Match("name", "unknown"),
			},
		})

		require.NoError(t, err)
		require.Len(t, policies.Data, 0)
	})

	t.Run("with query createdAt", func(t *testing.T) {
		policies, err := store.ListPolicies(context.Background(), GetPoliciesQuery{
			Options: PaginatedQueryOptions[PoliciesFilters]{
				QueryBuilder: query.Lt("createdAt", p1.CreatedAt),
			},
		})

		require.NoError(t, err)
		require.Len(t, policies.Data, 2)
		require.Equal(t, policies.Data[0].ID, p2.ID)
		require.Equal(t, policies.Data[1].ID, p3.ID)
	})
}
