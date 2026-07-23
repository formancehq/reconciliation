package api

import (
	"testing"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/stretchr/testify/require"
)

// Regression guard for TS-496: a cursor that carries a query filter must decode
// on the follow-up page. The shared PaginatedQueryOptions.UnmarshalJSON decodes
// the `qb` field via query.ParseJSON; without it the interface-typed builder
// fails to unmarshal and the handler returns `invalid 'cursor' query param`.
// The V1 list endpoints all ride on the same type, so one round-trip per query
// shape covers them.
func TestCursor_RoundTripsQueryFilter(t *testing.T) {
	t.Parallel()

	qb := query.And(
		query.Match("templateKind", "ledger_invariant"),
		query.Gte("createdAt", "2026-07-09T09:28:24.461Z"),
	)

	t.Run("rules", func(t *testing.T) {
		q := storage.NewGetRulesQuery(
			storage.NewPaginatedQueryOptions(storage.RulesFilters{}).WithQueryBuilder(qb),
		)
		oq := (*bunpaginate.OffsetPaginatedQuery[storage.PaginatedQueryOptions[storage.RulesFilters]])(&q)
		oq.Offset = 1

		var decoded storage.GetRulesQuery
		require.NoError(t, bunpaginate.UnmarshalCursor(oq.EncodeAsCursor(), &decoded))
		require.NotNil(t, decoded.Options.QueryBuilder, "query must survive the cursor round-trip")
		require.Equal(t, uint64(1), decoded.Offset)
	})

	t.Run("alerts", func(t *testing.T) {
		q := storage.NewGetAlertsQuery(
			storage.NewPaginatedQueryOptions(storage.AlertsFilters{}).WithQueryBuilder(qb),
		)
		oq := (*bunpaginate.OffsetPaginatedQuery[storage.PaginatedQueryOptions[storage.AlertsFilters]])(&q)

		var decoded storage.GetAlertsQuery
		require.NoError(t, bunpaginate.UnmarshalCursor(oq.EncodeAsCursor(), &decoded))
		require.NotNil(t, decoded.Options.QueryBuilder)
	})
}
