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

// Sealing orders periods by these bounds, and a wrong bound would either refuse a
// legitimate close or let a seal reach back into closed books — neither of which
// can be undone afterwards.
func TestPeriodStartAndEnd(t *testing.T) {
	for _, tc := range []struct {
		id         string
		start, end string
	}{
		{"2026-05", "2026-05-01", "2026-06-01"},
		{"2026-12", "2026-12-01", "2027-01-01"},
		{"2026-05-15", "2026-05-15", "2026-05-16"},
		{"2026-02-28", "2026-02-28", "2026-03-01"},
		{"2026-W12", "2026-03-16", "2026-03-23"},
		{"2026-W01", "2025-12-29", "2026-01-05"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			start, ok := PeriodStart(tc.id)
			require.True(t, ok)
			require.Equal(t, tc.start, start.Format("2006-01-02"))
			require.Equal(t, time.UTC, start.Location())

			end, ok := PeriodEnd(tc.id)
			require.True(t, ok)
			require.Equal(t, tc.end, end.Format("2006-01-02"))
			require.True(t, end.After(start))
		})
	}

	// An id no cadence produces has no bounds, so sealing cannot order it.
	for _, bad := range []string{"2026-13", "2026-02-31", "2026-W99", "continuous", "", "may"} {
		_, ok := PeriodStart(bad)
		require.False(t, ok, "PeriodStart(%q) must not resolve", bad)
		_, ok = PeriodEnd(bad)
		require.False(t, ok, "PeriodEnd(%q) must not resolve", bad)
	}

	// Consecutive periods meet exactly: no gap, no overlap. This is what makes the
	// closed calendar contiguous.
	mayEnd, _ := PeriodEnd("2026-05")
	juneStart, _ := PeriodStart("2026-06")
	require.Equal(t, mayEnd, juneStart)
}
