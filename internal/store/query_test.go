package store

import (
	"testing"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/stretchr/testify/require"
)

// Regression guard for TS-496 (main: #88): a cursor that carries a query filter
// must decode on the follow-up page. QueryBuilder is an interface, so without
// PaginatedQueryOptions.UnmarshalJSON the `qb` object fails to unmarshal and the
// handler answers `invalid 'cursor' query param` — a filtered list breaks on
// page 2. Every list endpoint rides the same type, so one round-trip per query
// shape covers them.
func TestCursor_RoundTripsQueryFilter(t *testing.T) {
	t.Parallel()

	qb := query.And(
		query.Match("templateKind", "stale_holds"),
		query.Gte("createdAt", "2026-09-01T00:00:00Z"),
	)

	t.Run("rules", func(t *testing.T) {
		t.Parallel()

		q := NewGetRulesQuery(NewPaginatedQueryOptions(RulesFilters{}).WithQueryBuilder(qb))
		oq := (*bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[RulesFilters]])(&q)
		oq.Offset = 1

		var decoded GetRulesQuery
		require.NoError(t, bunpaginate.UnmarshalCursor(oq.EncodeAsCursor(), &decoded))
		require.NotNil(t, decoded.Options.QueryBuilder, "query must survive the cursor round-trip")
		require.Equal(t, uint64(1), decoded.Offset)
	})

	t.Run("alerts", func(t *testing.T) {
		t.Parallel()

		q := NewGetAlertsQuery(NewPaginatedQueryOptions(AlertsFilters{}).WithQueryBuilder(qb))
		oq := (*bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[AlertsFilters]])(&q)

		var decoded GetAlertsQuery
		require.NoError(t, bunpaginate.UnmarshalCursor(oq.EncodeAsCursor(), &decoded))
		require.NotNil(t, decoded.Options.QueryBuilder)
	})

	t.Run("no filter decodes to a nil builder", func(t *testing.T) {
		t.Parallel()

		q := NewGetRulesQuery(NewPaginatedQueryOptions(RulesFilters{}))
		oq := (*bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[RulesFilters]])(&q)

		var decoded GetRulesQuery
		require.NoError(t, bunpaginate.UnmarshalCursor(oq.EncodeAsCursor(), &decoded))
		require.Nil(t, decoded.Options.QueryBuilder)
	})
}
