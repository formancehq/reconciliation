package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPeriodType_PeriodID(t *testing.T) {
	t.Parallel()
	pit := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
	cases := []struct {
		periodType PeriodType
		pit        time.Time
		want       string
	}{
		{PeriodTypeMonthly, pit, "2026-03"},
		{PeriodTypeWeekly, pit, "2026-W11"}, // 2026-03-15 is ISO week 11
		{PeriodTypeDaily, pit, "2026-03-15"},
		{PeriodTypeContinuous, pit, ContinuousPeriod},
		{PeriodType("yearly"), pit, ContinuousPeriod}, // unknown → safe fallback
		{PeriodType(""), pit, ContinuousPeriod},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, tc.periodType.PeriodID(tc.pit), "periodType %q", tc.periodType)
	}
}

// Bucketing is in UTC: an instant late on Mar 31 in a western zone is already
// April in UTC, so it buckets to the April period. Documents the known
// timezone simplification.
func TestPeriodType_PeriodID_UTCNormalised(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("UTC-5", -5*3600)
	lateMarch := time.Date(2026, 3, 31, 23, 0, 0, 0, loc) // = 2026-04-01T04:00Z
	require.Equal(t, "2026-04", PeriodTypeMonthly.PeriodID(lateMarch))
	require.Equal(t, "2026-04-01", PeriodTypeDaily.PeriodID(lateMarch))
}

// Determinism: any two instants in the same bucket yield the same id, so
// re-evaluating a period continues its case instead of spawning a new one.
func TestPeriodType_PeriodID_Deterministic(t *testing.T) {
	t.Parallel()
	early := time.Date(2026, 3, 1, 0, 0, 1, 0, time.UTC)
	late := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)
	require.Equal(t, PeriodTypeMonthly.PeriodID(early), PeriodTypeMonthly.PeriodID(late))
}

func TestPeriodType_Valid(t *testing.T) {
	t.Parallel()
	for _, c := range []PeriodType{PeriodTypeContinuous, PeriodTypeDaily, PeriodTypeWeekly, PeriodTypeMonthly} {
		require.True(t, c.Valid(), "%q should be valid", c)
	}
	for _, c := range []PeriodType{"", "MONTHLY", "yearly", "hourly"} {
		require.False(t, c.Valid(), "%q should be invalid", c)
	}
}
