package storage

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/stretchr/testify/require"
)

// TestPeriodScoping_CrossPeriodIsNewCase — the headline guarantee: the same
// fingerprint failing in a different period is a brand-new case (own id,
// `opened`, not `reopened`), so a later period can never rewrite an earlier
// period's record.
func TestPeriodScoping_CrossPeriodIsNewCase(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	mar := defaultOpenInput(t, ruleID, evID)
	mar.PeriodID = "2026-03"
	apr := defaultOpenInput(t, ruleID, evID)
	apr.PeriodID = "2026-04"

	rMar, err := s.OpenOrUpdateAlert(ctx, mar)
	require.NoError(t, err)
	require.True(t, rMar.Created)

	rApr, err := s.OpenOrUpdateAlert(ctx, apr)
	require.NoError(t, err)
	require.True(t, rApr.Created, "same fingerprint, new period → a fresh case")
	require.False(t, rApr.Reopened)
	require.NotEqual(t, rMar.Alert.ID, rApr.Alert.ID, "distinct alert ids per period")
	require.Equal(t, "2026-03", rMar.Alert.PeriodID)
	require.Equal(t, "2026-04", rApr.Alert.PeriodID)
}

// TestPeriodScoping_ReopenStaysWithinPeriod — reopen-in-place happens only
// within the same period; the period is the flap window.
func TestPeriodScoping_ReopenStaysWithinPeriod(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	in := defaultOpenInput(t, ruleID, evID)
	in.PeriodID = "2026-03"

	first, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	require.True(t, first.Created)

	_, err = s.AutoResolveAlert(ctx, ruleID, in.Fingerprint, "2026-03", evID, time.Now().UTC())
	require.NoError(t, err)

	reopen, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	require.False(t, reopen.Created)
	require.True(t, reopen.Reopened, "same fingerprint, same period, after resolve → reopen in place")
	require.Equal(t, first.Alert.ID, reopen.Alert.ID)
}

// TestPeriodScoping_SweepLeavesOtherPeriodsUntouched — a fresh period's
// evaluation (auto-resolve + sweep) must never close a prior period's open
// cases. This is the anti-"cook the books" guarantee: working April green
// leaves March's open case exactly as it was.
func TestPeriodScoping_SweepLeavesOtherPeriodsUntouched(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	mar := defaultOpenInput(t, ruleID, evID)
	mar.PeriodID = "2026-03"
	open, err := s.OpenOrUpdateAlert(ctx, mar)
	require.NoError(t, err)

	// April's sweep sees no active fingerprints…
	fpsApr, err := s.ListActiveAlertFingerprints(ctx, ruleID, "2026-04")
	require.NoError(t, err)
	require.Empty(t, fpsApr)

	// …and an April auto-resolve of the same fingerprint is a no-op.
	noop, err := s.AutoResolveAlert(ctx, ruleID, mar.Fingerprint, "2026-04", evID, time.Now().UTC())
	require.NoError(t, err)
	require.Nil(t, noop)

	// March's case is untouched: still OPEN, still the sole active fingerprint.
	stillOpen, err := s.GetAlert(ctx, open.Alert.ID)
	require.NoError(t, err)
	require.Equal(t, models.AlertOpen, stillOpen.Status)
	fpsMar, err := s.ListActiveAlertFingerprints(ctx, ruleID, "2026-03")
	require.NoError(t, err)
	require.Equal(t, []string{mar.Fingerprint}, fpsMar)
}

// TestPeriodScoping_ReconciliationStatus — "is March green?" is answered by the
// period's active cases (zero active ⇒ green; the production read used by the
// UI/API is ListActiveAlertFingerprints / GET /alerts?periodID=&status=OPEN).
// A resolved period stays auditable: the resolved alert remains on record, so
// going green doesn't erase that the period once had an issue.
func TestPeriodScoping_ReconciliationStatus(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	in := defaultOpenInput(t, ruleID, evID)
	in.PeriodID = "2026-03"
	open, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)

	// March is red — one active case.
	marActive, err := s.ListActiveAlertFingerprints(ctx, ruleID, "2026-03")
	require.NoError(t, err)
	require.Equal(t, []string{in.Fingerprint}, marActive)

	// A period with no cases is trivially green.
	aprActive, err := s.ListActiveAlertFingerprints(ctx, ruleID, "2026-04")
	require.NoError(t, err)
	require.Empty(t, aprActive)

	// Resolving the case turns March green…
	_, err = s.AutoResolveAlert(ctx, ruleID, in.Fingerprint, "2026-03", evID, time.Now().UTC())
	require.NoError(t, err)
	marActive, err = s.ListActiveAlertFingerprints(ctx, ruleID, "2026-03")
	require.NoError(t, err)
	require.Empty(t, marActive)

	// …but the resolved case is still on record — immutable history.
	rec, err := s.GetAlert(ctx, open.Alert.ID)
	require.NoError(t, err)
	require.Equal(t, models.AlertResolved, rec.Status)
}

// TestListAlerts_FilterByPeriod — the ?periodID= filter that powers the
// "is this period green?" read returns only that period's alerts.
func TestListAlerts_FilterByPeriod(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	mar := defaultOpenInput(t, ruleID, evID)
	mar.PeriodID = "2026-03"
	_, err := s.OpenOrUpdateAlert(ctx, mar)
	require.NoError(t, err)

	apr := defaultOpenInput(t, ruleID, evID)
	apr.PeriodID = "2026-04"
	_, err = s.OpenOrUpdateAlert(ctx, apr)
	require.NoError(t, err)

	q := NewGetAlertsQuery(
		NewPaginatedQueryOptions(AlertsFilters{}).
			WithQueryBuilder(query.Match("periodID", "2026-03")).
			WithPageSize(15),
	)
	cursor, err := s.ListAlerts(ctx, q)
	require.NoError(t, err)
	require.Len(t, cursor.Data, 1)
	require.Equal(t, "2026-03", cursor.Data[0].PeriodID)
}

// TestPeriodScoping_ContinuousDefault — an input with no period defaults to the
// continuous scope and reproduces the original immortal (rule, fingerprint)
// dedup: repeated fails fold into one ongoing case.
func TestPeriodScoping_ContinuousDefault(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	in := defaultOpenInput(t, ruleID, evID) // no PeriodID

	first, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	require.Equal(t, models.ContinuousPeriod, first.Alert.PeriodID)

	again, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	require.False(t, again.Created, "continuous scope folds repeats into one case")
	require.Equal(t, first.Alert.ID, again.Alert.ID)
}
