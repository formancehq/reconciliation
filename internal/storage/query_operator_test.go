package storage

import (
	"fmt"
	"testing"

	"github.com/formancehq/go-libs/query"
	"github.com/stretchr/testify/require"
)

// The go-libs query builder admits operators (e.g. $exists) that have no entry
// in DefaultComparisonOperatorsMapping. On a date column the V1 query contexts
// used to index the map blindly, yielding an empty SQL operator and a Postgres
// syntax error surfaced as HTTP 500. They must return ErrInvalidQuery (→ 400)
// instead. Regression for the NumaryBot finding (matches the legacy
// policy/reconciliation contexts, which already guarded this). The query-context
// methods don't touch the DB, so this runs without a container.
func TestQueryContext_UnsupportedDateOperator_ReturnsInvalidQuery(t *testing.T) {
	t.Parallel()
	s := &Storage{}
	cases := []struct {
		name  string
		field string
		build func(query.Builder) (string, []any, error)
	}{
		{"rules/createdAt", "createdAt", s.ruleQueryContext},
		{"rules/updatedAt", "updatedAt", s.ruleQueryContext},
		{"alerts/firstSeenAt", "firstSeenAt", s.alertQueryContext},
		{"alerts/lastSeenAt", "lastSeenAt", s.alertQueryContext},
		{"evaluations/createdAt", "createdAt", s.evaluationQueryContext},
		{"evaluations/startedAt", "startedAt", s.evaluationQueryContext},
		{"evaluations/endedAt", "endedAt", s.evaluationQueryContext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qb, err := query.ParseJSON(fmt.Sprintf(`{"$exists":{%q:"2020-01-01T00:00:00Z"}}`, tc.field))
			require.NoError(t, err)
			_, _, err = tc.build(qb)
			require.ErrorIs(t, err, ErrInvalidQuery, "unmapped date operator must map to ErrInvalidQuery (400), not an empty SQL operator")
		})
	}
}

// A supported comparison operator on a date column still builds a clean clause.
func TestQueryContext_SupportedDateOperator_OK(t *testing.T) {
	t.Parallel()
	s := &Storage{}
	for _, tc := range []struct {
		name  string
		build func(query.Builder) (string, []any, error)
		want  string
	}{
		{"rules/createdAt", s.ruleQueryContext, "created_at >="},
		{"alerts/firstSeenAt", s.alertQueryContext, "first_seen_at >="},
		{"evaluations/startedAt", s.evaluationQueryContext, "started_at >="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			field := map[string]string{
				"rules/createdAt": "createdAt", "alerts/firstSeenAt": "firstSeenAt", "evaluations/startedAt": "startedAt",
			}[tc.name]
			qb, err := query.ParseJSON(fmt.Sprintf(`{"$gte":{%q:"2020-01-01T00:00:00Z"}}`, field))
			require.NoError(t, err)
			where, args, err := tc.build(qb)
			require.NoError(t, err)
			require.Contains(t, where, tc.want)
			require.Len(t, args, 1)
		})
	}
}
