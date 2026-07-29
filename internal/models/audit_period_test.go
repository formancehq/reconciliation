package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mustDate(t *testing.T, iso string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", iso)
	require.NoError(t, err)
	return d
}

// Shape validation alone accepted "2026-13", "2026-02-31" and "2026-W99" — labels
// that name no real period. Sealing one consumes a range of the journal
// permanently under a label nobody will ever query, and the seal is immutable, so
// this has to be caught before the first write.
func TestValidPeriodID(t *testing.T) {
	t.Parallel()

	t.Run("accepts what the cadences produce", func(t *testing.T) {
		for _, id := range []string{
			"2026-01", "2026-05", "2026-12", // monthly
			"2026-W01", "2026-W12", "2026-W53", // weekly (2026 has 53 ISO weeks)
			"2026-05-15", "2024-02-29", // daily, incl. a real leap day
		} {
			require.True(t, ValidPeriodID(id), "should accept %q", id)
		}
	})

	t.Run("rejects shape-valid but impossible periods", func(t *testing.T) {
		for _, id := range []string{
			"2026-13", "2026-00", // no such month
			"2026-02-31", "2025-02-29", // no such day
			"2026-W00", "2026-W54", "2026-W99", // no such ISO week
			"2025-W53", // 2025 has only 52 ISO weeks
		} {
			require.False(t, ValidPeriodID(id), "should reject impossible %q", id)
		}
	})

	t.Run("rejects malformed shapes", func(t *testing.T) {
		for _, id := range []string{
			"2026-5", "26-05", "2026-05-", "may", "2026_05", "  2026-05", "",
			ContinuousPeriod, // a real period id, but never a sealable one
		} {
			require.False(t, ValidPeriodID(id), "should reject %q", id)
		}
	})

	// The check must stay exactly as strict as the producer, so it is defined by
	// round-tripping through Cadence.PeriodID rather than by a second opinion
	// about what "valid" means.
	t.Run("agrees with every id the cadences emit", func(t *testing.T) {
		base := mustDate(t, "2026-03-11")
		for _, c := range []Cadence{CadenceDaily, CadenceWeekly, CadenceMonthly} {
			for day := 0; day < 400; day++ {
				id := c.PeriodID(base.AddDate(0, 0, day))
				require.True(t, ValidPeriodID(id), "%s produced %q which validation rejects", c, id)
			}
		}
	})
}
