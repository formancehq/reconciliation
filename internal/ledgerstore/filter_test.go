package ledgerstore

import (
	"testing"
	"time"

	"github.com/formancehq/go-libs/query"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/stretchr/testify/require"
)

func TestBuildListFilter_NilQuery_PrefixOnly(t *testing.T) {
	t.Parallel()

	f, err := buildListFilter(schema.ItemPrefix(), nil, alertLeaf)
	require.NoError(t, err)
	require.Equal(t, schema.ItemPrefix(), f.GetAddress().GetHardcodedPrefix())
}

func TestBuildListFilter_AlertEquality(t *testing.T) {
	t.Parallel()

	f, err := buildListFilter(schema.ItemPrefix(), query.Match("status", "OPEN"), alertLeaf)
	require.NoError(t, err)

	// Top level ANDs the item prefix with the translated predicate.
	sub := f.GetAnd().GetFilters()
	require.Len(t, sub, 2)
	require.Equal(t, schema.ItemPrefix(), sub[0].GetAddress().GetHardcodedPrefix())
	require.Equal(t, schema.MetaStatus, sub[1].GetField().GetField().GetMetadata())
	require.Equal(t, "OPEN", sub[1].GetField().GetStringCond().GetHardcoded())
}

func TestBuildListFilter_AlertConjunction(t *testing.T) {
	t.Parallel()

	qb := query.And(query.Match("status", "OPEN"), query.Match("severity", "high"))
	f, err := buildListFilter(schema.ItemPrefix(), qb, alertLeaf)
	require.NoError(t, err)

	// prefix AND (status AND severity)
	top := f.GetAnd().GetFilters()
	require.Len(t, top, 2)
	inner := top[1].GetAnd().GetFilters()
	require.Len(t, inner, 2)
	require.Equal(t, schema.MetaStatus, inner[0].GetField().GetField().GetMetadata())
	require.Equal(t, schema.MetaSeverity, inner[1].GetField().GetField().GetMetadata())
}

func TestBuildListFilter_AlertDatetimeRange(t *testing.T) {
	t.Parallel()

	ts := "2026-03-01T00:00:00Z"
	want := mustMicros(t, ts)

	f, err := buildListFilter(schema.ItemPrefix(), query.Gt("firstSeenAt", ts), alertLeaf)
	require.NoError(t, err)

	cond := f.GetAnd().GetFilters()[1].GetField()
	require.Equal(t, schema.MetaFirstSeenAt, cond.GetField().GetMetadata())
	require.Equal(t, want, cond.GetIntCond().GetMin())
	require.True(t, cond.GetIntCond().GetMinExclusive(), "$gt is exclusive")
}

func TestBuildListFilter_AlertOrNot(t *testing.T) {
	t.Parallel()

	qb := query.Or(
		query.Match("status", "OPEN"),
		query.Not(query.Match("severity", "low")),
	)
	f, err := buildListFilter(schema.ItemPrefix(), qb, alertLeaf)
	require.NoError(t, err)

	or := f.GetAnd().GetFilters()[1].GetOr().GetFilters()
	require.Len(t, or, 2)
	require.Equal(t, schema.MetaStatus, or[0].GetField().GetField().GetMetadata())
	require.Equal(t, schema.MetaSeverity, or[1].GetNot().GetFilter().GetField().GetField().GetMetadata())
}

func TestBuildListFilter_RuleIDIsAddress(t *testing.T) {
	t.Parallel()

	id := "11111111-1111-1111-1111-111111111111"
	f, err := buildListFilter(schema.RulePrefix(), query.Match("id", id), ruleLeaf)
	require.NoError(t, err)

	// A rule's id resolves to its address, not a metadata field.
	require.Equal(t, schema.RuleAccount(id), f.GetAnd().GetFilters()[1].GetAddress().GetHardcodedPrefix())
}

func TestBuildListFilter_RuleEnabledBool(t *testing.T) {
	t.Parallel()

	f, err := buildListFilter(schema.RulePrefix(), query.Match("enabled", true), ruleLeaf)
	require.NoError(t, err)

	cond := f.GetAnd().GetFilters()[1].GetField()
	require.Equal(t, schema.MetaEnabled, cond.GetField().GetMetadata())
	require.True(t, cond.GetBoolCond().GetHardcoded())
}

func TestBuildListFilter_Rejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		qb   query.Builder
		leaf schema.LeafMapper
	}{
		{"unknown alert key", query.Match("bogus", "x"), alertLeaf},
		{"unknown rule key", query.Match("bogus", "x"), ruleLeaf},
		{"status wrong operator", query.Gt("status", "OPEN"), alertLeaf},
		{"status wrong type", query.Match("status", 42), alertLeaf},
		{"enabled wrong type", query.Match("enabled", "yes"), ruleLeaf},
		{"bad datetime", query.Gt("firstSeenAt", "not-a-date"), alertLeaf},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildListFilter(schema.ItemPrefix(), tc.qb, tc.leaf)
			require.ErrorIs(t, err, store.ErrInvalidQuery)
		})
	}
}

func mustMicros(t *testing.T, s string) int64 {
	t.Helper()

	ts, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)

	return ts.UnixMicro()
}
