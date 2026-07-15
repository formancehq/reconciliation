package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func enabledCronRule(expr string) models.Rule {
	return models.Rule{ID: uuid.New(), Enabled: true, Schedule: cronSched(expr)}
}

// TestRun_FiresDueRulesThenStopsOnCancel drives the full Run loop: it ticks on
// the interval, fires a rule that comes due each window, and returns promptly
// when its context is cancelled.
func TestRun_FiresDueRulesThenStopsOnCancel(t *testing.T) {
	t.Parallel()
	rule := enabledCronRule("* * * * *")
	svc := &fakeRuleSvc{rules: []models.Rule{rule}}
	s := New(svc, time.Millisecond, testLogger())

	// Deterministic clock: each call advances a minute so every tick window
	// (last, now] contains a "* * * * *" firing — no wall-clock dependence.
	var mu sync.Mutex
	base := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	calls := 0
	s.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at := base.Add(time.Duration(calls) * time.Minute)
		calls++
		return at
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	require.Eventually(t, func() bool { return svc.fired(rule.ID) }, 2*time.Second, 5*time.Millisecond,
		"Run should fire the due cron rule")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// TestNew_DefaultsNonPositiveInterval — a zero/negative interval falls back to
// one minute (cron granularity), never a busy-spin.
func TestNew_DefaultsNonPositiveInterval(t *testing.T) {
	t.Parallel()
	svc := &fakeRuleSvc{}
	require.Equal(t, time.Minute, New(svc, 0, testLogger()).interval)
	require.Equal(t, time.Minute, New(svc, -5*time.Second, testLogger()).interval)
	require.Equal(t, 2*time.Second, New(svc, 2*time.Second, testLogger()).interval)
}

// TestFire_EvaluateError_Swallowed — a rule whose evaluation errors is logged,
// not propagated or panicked: one bad rule must not stop the scheduler.
func TestFire_EvaluateError_Swallowed(t *testing.T) {
	t.Parallel()
	rule := enabledCronRule("* * * * *")
	svc := &fakeRuleSvc{rules: []models.Rule{rule}, evalErr: errors.New("engine boom")}
	s := New(svc, time.Minute, testLogger())

	s.fire(context.Background(), rule) // synchronous — must return cleanly
	require.True(t, svc.fired(rule.ID), "EvaluateRule was still invoked despite the injected error")
}

// TestFire_DefaultsSafetyMarginWhenScheduleOmitsIt — a cron rule whose schedule
// leaves safetyMargin unset (unmarshals to 0) must still evaluate at the
// documented T-30s default, matching manual evaluations — not at the tick
// instant, which would read in-flight ledger writes. (NumaryBot/codex finding.)
func TestFire_DefaultsSafetyMarginWhenScheduleOmitsIt(t *testing.T) {
	t.Parallel()
	rule := enabledCronRule("* * * * *") // no SafetyMargin set → 0
	svc := &fakeRuleSvc{rules: []models.Rule{rule}}
	s := New(svc, time.Minute, testLogger())

	s.fire(context.Background(), rule)
	require.Equal(t, defaultScheduleSafetyMargin, svc.marginFor(rule.ID),
		"an omitted schedule safetyMargin must default to 30s at fire time")
}

// TestFire_HonorsExplicitSafetyMargin — an explicit positive margin on the
// schedule is passed through unchanged (only the unset/zero case is defaulted).
func TestFire_HonorsExplicitSafetyMargin(t *testing.T) {
	t.Parallel()
	rule := enabledCronRule("* * * * *")
	rule.Schedule.SafetyMargin = 5 * time.Second
	svc := &fakeRuleSvc{rules: []models.Rule{rule}}
	s := New(svc, time.Minute, testLogger())

	s.fire(context.Background(), rule)
	require.Equal(t, 5*time.Second, svc.marginFor(rule.ID))
}

// TestTick_ListError_Swallowed — a failing ListRules is logged and the tick
// returns without firing anything.
func TestTick_ListError_Swallowed(t *testing.T) {
	t.Parallel()
	svc := &fakeRuleSvc{listErr: errors.New("db down")}
	s := New(svc, time.Minute, testLogger())
	last := time.Date(2026, 3, 15, 8, 59, 0, 0, time.UTC)
	s.tick(context.Background(), last, last.Add(time.Minute))
	require.Equal(t, 0, svc.count())
}

// TestTick_InvalidCronExpr_Skipped — a rule that slipped past create-time
// validation with a bad cron expr is logged and skipped, not fired, and does not
// poison the rest of the tick.
func TestTick_InvalidCronExpr_Skipped(t *testing.T) {
	t.Parallel()
	bad := enabledCronRule("not a cron")
	good := enabledCronRule("* * * * *")
	svc := &fakeRuleSvc{rules: []models.Rule{bad, good}}
	s := New(svc, time.Minute, testLogger())

	last := time.Date(2026, 3, 15, 8, 59, 0, 0, time.UTC)
	s.tick(context.Background(), last, last.Add(time.Minute))

	require.Eventually(t, func() bool { return svc.fired(good.ID) }, time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.False(t, svc.fired(bad.ID), "a rule with an invalid cron expr must never fire")
	require.Equal(t, 1, svc.count())
}

// TestListCronRules_OverflowWarns — when the enabled+cron set exceeds one page
// the scheduler still returns the page (it logs a warning rather than dropping
// silently). We assert it doesn't error and returns the page.
func TestListCronRules_OverflowWarns(t *testing.T) {
	t.Parallel()
	svc := &fakeRuleSvc{
		rules:   []models.Rule{enabledCronRule("* * * * *")},
		hasMore: true,
	}
	s := New(svc, time.Minute, testLogger())
	rules, err := s.listCronRules(context.Background())
	require.NoError(t, err)
	require.Len(t, rules, 1)
}
