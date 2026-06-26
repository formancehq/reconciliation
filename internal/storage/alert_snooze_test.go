package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/events"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/stretchr/testify/require"
)

// TestSnoozeAlert_SuppressesEvenOnChange — the defining difference from flap
// suppression #1: an ACTIVE snooze mutes a failing alert even when the evidence
// moves (which would normally publish `updated`). Only `opened` and the
// one-off `snoozed` event reach the bus.
func TestSnoozeAlert_SuppressesEvenOnChange(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	base := time.Now().UTC()
	in := defaultOpenInput(t, ruleID, evID)
	in.OccurredAt = base

	res, err := s.OpenOrUpdateAlert(ctx, in) // opened
	require.NoError(t, err)

	snoozed, err := s.SnoozeAlert(ctx, res.Alert.ID, base.Add(time.Hour), "alice@formance.com", "migration in flight")
	require.NoError(t, err)
	require.NotNil(t, snoozed.Snooze)
	require.Equal(t, "alice@formance.com", snoozed.Snooze.By)

	// A failing eval WITH changed evidence, while snoozed → muted.
	moved := in
	moved.Evidence = json.RawMessage(`{"drift":"999"}`)
	moved.OccurredAt = base.Add(10 * time.Minute)
	_, err = s.OpenOrUpdateAlert(ctx, moved)
	require.NoError(t, err)

	require.Equal(t, []string{
		events.EventTypeAlertOpened,
		events.EventTypeAlertSnoozed,
	}, fake.webhookTypes(), "an active snooze mutes even a changing alert")

	// The record is intact: the muted fail is still logged (notify=false).
	evs := allAlertEvents(t, s, res.Alert.ID)
	require.Len(t, evs, 3, "opened + snoozed + the muted fail are all recorded")
}

// TestSnoozeAlert_ExpiryResurfacesOnce — the first failing eval at/after the
// snooze window clears the mute and publishes one `updated`; the alert is no
// longer snoozed afterwards.
func TestSnoozeAlert_ExpiryResurfacesOnce(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	base := time.Now().UTC()
	in := defaultOpenInput(t, ruleID, evID)
	in.OccurredAt = base

	res, err := s.OpenOrUpdateAlert(ctx, in) // opened
	require.NoError(t, err)
	_, err = s.SnoozeAlert(ctx, res.Alert.ID, base.Add(time.Hour), "alice", "") // snoozed
	require.NoError(t, err)

	// Eval after the window — identical evidence, but expiry pages once.
	past := in
	past.OccurredAt = base.Add(2 * time.Hour)
	_, err = s.OpenOrUpdateAlert(ctx, past)
	require.NoError(t, err)

	require.Equal(t, []string{
		events.EventTypeAlertOpened,
		events.EventTypeAlertSnoozed,
		events.EventTypeAlertUpdated,
	}, fake.webhookTypes())

	got, err := s.GetAlert(ctx, res.Alert.ID)
	require.NoError(t, err)
	require.Nil(t, got.Snooze, "an expired snooze is cleared")

	// And a subsequent identical fail is back to ordinary #1 suppression.
	later := in
	later.OccurredAt = base.Add(3 * time.Hour)
	_, err = s.OpenOrUpdateAlert(ctx, later)
	require.NoError(t, err)
	require.Equal(t, []string{
		events.EventTypeAlertOpened,
		events.EventTypeAlertSnoozed,
		events.EventTypeAlertUpdated,
	}, fake.webhookTypes(), "post-expiry identical repeat is suppressed again")
}

// TestUnsnoozeAlert_LiftsAndRestoresNormalBehaviour — an explicit unsnooze
// publishes `unsnoozed`, clears the mute, and a following identical fail is
// suppressed by the ordinary #1 path (no spurious `updated`).
func TestUnsnoozeAlert_LiftsAndRestoresNormalBehaviour(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	base := time.Now().UTC()
	in := defaultOpenInput(t, ruleID, evID)
	in.OccurredAt = base

	res, err := s.OpenOrUpdateAlert(ctx, in) // opened
	require.NoError(t, err)
	_, err = s.SnoozeAlert(ctx, res.Alert.ID, base.Add(time.Hour), "alice", "") // snoozed
	require.NoError(t, err)

	lifted, err := s.UnsnoozeAlert(ctx, res.Alert.ID, "bob")
	require.NoError(t, err)
	require.Nil(t, lifted.Snooze)

	identical := in
	identical.OccurredAt = base.Add(2 * time.Minute)
	_, err = s.OpenOrUpdateAlert(ctx, identical)
	require.NoError(t, err)

	require.Equal(t, []string{
		events.EventTypeAlertOpened,
		events.EventTypeAlertSnoozed,
		events.EventTypeAlertUnsnoozed,
	}, fake.webhookTypes(), "after unsnooze, an identical fail is suppressed again")
}

// TestUnsnoozeAlert_NoOpWhenNotSnoozed — lifting a snooze that isn't there
// returns the alert unchanged and emits no event.
func TestUnsnoozeAlert_NoOpWhenNotSnoozed(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)

	got, err := s.UnsnoozeAlert(ctx, res.Alert.ID, "bob")
	require.NoError(t, err)
	require.Nil(t, got.Snooze)
	require.Equal(t, []string{events.EventTypeAlertOpened}, fake.webhookTypes(),
		"a redundant unsnooze emits nothing")
}

// TestSnoozeAlert_RejectsResolvedAndPastWindow — can't mute a closed alert, and
// the window must be in the future.
func TestSnoozeAlert_RejectsResolvedAndPastWindow(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)

	_, err = s.SnoozeAlert(ctx, res.Alert.ID, time.Now().UTC().Add(-time.Hour), "alice", "")
	require.Error(t, err, "a past window is rejected")

	_, err = s.AutoResolveAlert(ctx, ruleID, res.Alert.Fingerprint, models.ContinuousPeriod, evID, time.Now().UTC())
	require.NoError(t, err)
	_, err = s.SnoozeAlert(ctx, res.Alert.ID, time.Now().UTC().Add(time.Hour), "alice", "")
	require.ErrorIs(t, err, ErrNotFound, "a resolved alert cannot be snoozed")
}

// TestResolveClearsSnooze — closing an alert drops any snooze it carried, so a
// later reopen isn't unexpectedly muted by a stale window.
func TestResolveClearsSnooze(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)
	_, err = s.SnoozeAlert(ctx, res.Alert.ID, time.Now().UTC().Add(time.Hour), "alice", "")
	require.NoError(t, err)

	resolved, err := s.ResolveAlertManual(ctx, res.Alert.ID, &models.Resolution{
		Kind: models.ResolutionFixedByBooking, By: "ops", At: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.Nil(t, resolved.Snooze, "resolving clears the snooze")
}
