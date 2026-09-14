package storage

import (
	"context"
	"testing"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/stretchr/testify/require"
)

// TestRule_PeriodTypeVocabularyMatchesConstraint guards the seam left by renaming
// this field at the API and Go level while deliberately leaving the column named
// `cadence` (see models.Rule.PeriodType). The Go vocabulary and the DB CHECK are
// two independent lists that must agree: if they drift, a value the service
// accepts becomes a 500 at insert time instead of a 400 at validation time.
//
// Round-tripping through storage also proves the bun tag still points at the
// real column — a wrong tag fails here rather than in production.
func TestRule_PeriodTypeVocabularyMatchesConstraint(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for _, pt := range []models.PeriodType{
		models.PeriodTypeContinuous,
		models.PeriodTypeDaily,
		models.PeriodTypeWeekly,
		models.PeriodTypeMonthly,
	} {
		rule := makeRule("ok-" + string(pt))
		rule.PeriodType = pt
		require.NoError(t, s.CreateRule(ctx, rule), "%q must be accepted by rule_cadence_chk", pt)

		got, err := s.GetRule(ctx, rule.ID)
		require.NoError(t, err)
		require.Equal(t, pt, got.PeriodType, "%q must survive a storage round-trip", pt)
	}

	bad := makeRule("bad-period-type")
	bad.PeriodType = models.PeriodType("hourly")
	require.Error(t, s.CreateRule(ctx, bad),
		"rule_cadence_chk must reject values outside the vocabulary")
}

// TestRule_PeriodTypeDefaultsToContinuous pins the storage-layer default. The
// column is NOT NULL with no empty value permitted, so a direct insert that
// skipped the service-layer default would otherwise fail the CHECK.
func TestRule_PeriodTypeDefaultsToContinuous(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	rule := makeRule("no-period-type")
	rule.PeriodType = ""
	require.NoError(t, s.CreateRule(ctx, rule))

	got, err := s.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	require.Equal(t, models.PeriodTypeContinuous, got.PeriodType)
}
