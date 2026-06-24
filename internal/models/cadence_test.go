package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCadence_PeriodID(t *testing.T) {
	t.Parallel()
	pit := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
	cases := []struct {
		cadence Cadence
		pit     time.Time
		want    string
	}{
		{CadenceMonthly, pit, "2026-03"},
		{CadenceWeekly, pit, "2026-W11"}, // 2026-03-15 is ISO week 11
		{CadenceDaily, pit, "2026-03-15"},
		{CadenceContinuous, pit, ContinuousPeriod},
		{Cadence("yearly"), pit, ContinuousPeriod}, // unknown → safe fallback
		{Cadence(""), pit, ContinuousPeriod},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, tc.cadence.PeriodID(tc.pit), "cadence %q", tc.cadence)
	}
}

// Bucketing is in UTC: an instant late on Mar 31 in a western zone is already
// April in UTC, so it buckets to the April period. Documents the known
// timezone simplification.
func TestCadence_PeriodID_UTCNormalised(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("UTC-5", -5*3600)
	lateMarch := time.Date(2026, 3, 31, 23, 0, 0, 0, loc) // = 2026-04-01T04:00Z
	require.Equal(t, "2026-04", CadenceMonthly.PeriodID(lateMarch))
	require.Equal(t, "2026-04-01", CadenceDaily.PeriodID(lateMarch))
}

// Determinism: any two instants in the same bucket yield the same id, so
// re-evaluating a period continues its case instead of spawning a new one.
func TestCadence_PeriodID_Deterministic(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 3, 1, 0, 0, 1, 0, time.UTC)
	late := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)
	require.Equal(t, CadenceMonthly.PeriodID(early), CadenceMonthly.PeriodID(late))
}

func TestCadence_Valid(t *testing.T) {
	t.Parallel()
	for _, c := range []Cadence{CadenceContinuous, CadenceDaily, CadenceWeekly, CadenceMonthly} {
		require.True(t, c.Valid(), "%q should be valid", c)
	}
	for _, c := range []Cadence{"", "MONTHLY", "yearly", "hourly"} {
		require.False(t, c.Valid(), "%q should be invalid", c)
	}
}
