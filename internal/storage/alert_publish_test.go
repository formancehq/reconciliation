package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/events"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeAlertEventPublisher records every PublishAlertEvent call so tests can
// assert which webhook events a sequence of transitions produced, in order.
type fakeAlertEventPublisher struct {
	mu       sync.Mutex
	captured []capturedAlertEvent
}

type capturedAlertEvent struct {
	alertID     uuid.UUID
	eventID     uuid.UUID
	webhookType string
}

func (f *fakeAlertEventPublisher) PublishAlertEvent(_ context.Context, alert *models.Alert, event *models.AlertEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captured = append(f.captured, capturedAlertEvent{
		alertID:     alert.ID,
		eventID:     event.ID,
		webhookType: events.EventTypeFor(event),
	})
}

func (f *fakeAlertEventPublisher) webhookTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.captured))
	for i, c := range f.captured {
		out[i] = c.webhookType
	}
	return out
}

func (f *fakeAlertEventPublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.captured)
}

// TestPublishAlertEvents_AllTransitions drives one alert through every
// lifecycle state and asserts each transition emits exactly one webhook event
// of the documented type, in order, all for the same alert id.
func TestPublishAlertEvents_AllTransitions(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	in := defaultOpenInput(t, ruleID, evID)
	fp := in.Fingerprint

	res, err := s.OpenOrUpdateAlert(ctx, in) // opened
	require.NoError(t, err)
	id := res.Alert.ID

	_, err = s.OpenOrUpdateAlert(ctx, in) // updated
	require.NoError(t, err)

	_, err = s.AckAlert(ctx, id, &models.Ack{By: "alice", At: time.Now().UTC()}) // acknowledged
	require.NoError(t, err)

	_, err = s.ResolveAlertManual(ctx, id, &models.Resolution{
		Kind: models.ResolutionFixedByBooking, By: "ops", At: time.Now().UTC(),
	}) // resolved
	require.NoError(t, err)

	_, err = s.OpenOrUpdateAlert(ctx, in) // reopened (prev=RESOLVED)
	require.NoError(t, err)

	_, err = s.AutoResolveAlert(ctx, ruleID, fp, models.ContinuousPeriod, evID, time.Now().UTC()) // resolved (pass)
	require.NoError(t, err)

	_, err = s.OpenOrUpdateAlert(ctx, in) // reopened again
	require.NoError(t, err)

	_, err = s.AcceptAlert(ctx, id, &models.Resolution{
		Kind: models.ResolutionAcceptedByBusiness, By: "treasury", At: time.Now().UTC(), Note: "confirmed",
	}) // accepted
	require.NoError(t, err)

	require.Equal(t, []string{
		events.EventTypeAlertOpened,
		events.EventTypeAlertUpdated,
		events.EventTypeAlertAcknowledged,
		events.EventTypeAlertResolved,
		events.EventTypeAlertReopened,
		events.EventTypeAlertResolved,
		events.EventTypeAlertReopened,
		events.EventTypeAlertAccepted,
	}, fake.webhookTypes())

	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, c := range fake.captured {
		require.Equal(t, id, c.alertID, "every event is for the one stable alert id")
		require.NotEqual(t, uuid.Nil, c.eventID)
	}
}

// TestPublishAlertEvents_AckNoOpEmitsNothing — an idempotent re-ack writes no
// event row, so it must emit no webhook event.
func TestPublishAlertEvents_AckNoOpEmitsNothing(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)
	id := res.Alert.ID

	_, err = s.AckAlert(ctx, id, &models.Ack{By: "alice", At: time.Now().UTC()})
	require.NoError(t, err)
	_, err = s.AckAlert(ctx, id, &models.Ack{By: "bob", At: time.Now().UTC()}) // no-op
	require.NoError(t, err)

	require.Equal(t, []string{
		events.EventTypeAlertOpened,
		events.EventTypeAlertAcknowledged,
	}, fake.webhookTypes(), "re-ack must not re-emit acknowledged")
}

// TestPublishAlertEvents_AutoResolveNoOpEmitsNothing — auto-resolving a
// fingerprint with no active alert is a no-op and emits nothing.
func TestPublishAlertEvents_AutoResolveNoOpEmitsNothing(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	resolved, err := s.AutoResolveAlert(ctx, ruleID, "no-such-fingerprint", models.ContinuousPeriod, evID, time.Now().UTC())
	require.NoError(t, err)
	require.Nil(t, resolved)
	require.Zero(t, fake.count())
}

// TestPublishAlertEvents_DeferredUntilOuterCommit — under an outer RunInTx
// (the evaluation path), events buffer and publish only after the transaction
// commits, never mid-transaction.
func TestPublishAlertEvents_DeferredUntilOuterCommit(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	in := defaultOpenInput(t, ruleID, evID)

	err := s.RunInTx(ctx, func(ctx context.Context, txStore *Storage) error {
		_, err := txStore.OpenOrUpdateAlert(ctx, in)
		require.NoError(t, err)
		_, err = txStore.OpenOrUpdateAlert(ctx, in)
		require.NoError(t, err)
		require.Zero(t, fake.count(), "nothing may publish before the outer tx commits")
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		events.EventTypeAlertOpened,
		events.EventTypeAlertUpdated,
	}, fake.webhookTypes(), "buffered events publish once, after commit, in order")
}

// TestPublishAlertEvents_NestedRunInTxDefersToOutermost — a RunInTx nested
// inside another (a bun savepoint) must not flush; the outermost owner does,
// once, after the real commit. Guards the savepoint-nesting contract.
func TestPublishAlertEvents_NestedRunInTxDefersToOutermost(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	in := defaultOpenInput(t, ruleID, evID)

	err := s.RunInTx(ctx, func(ctx context.Context, outer *Storage) error {
		return outer.RunInTx(ctx, func(ctx context.Context, inner *Storage) error {
			_, err := inner.OpenOrUpdateAlert(ctx, in)
			require.NoError(t, err)
			require.Zero(t, fake.count(), "a nested (savepoint) commit must not publish")
			return nil
		})
	})
	require.NoError(t, err)
	require.Equal(t, []string{events.EventTypeAlertOpened}, fake.webhookTypes(),
		"event publishes once, after the outermost commit")
}

// TestPublishAlertEvents_RolledBackOuterTxEmitsNothing — the critical
// correctness guarantee: a rolled-back outer transaction publishes no events,
// so the bus never sees a transition the database discarded.
func TestPublishAlertEvents_RolledBackOuterTxEmitsNothing(t *testing.T) {
	fake := &fakeAlertEventPublisher{}
	s := newStore(t).WithPublisher(fake)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	wantErr := errors.New("callback failed")
	err := s.RunInTx(ctx, func(ctx context.Context, txStore *Storage) error {
		_, _ = txStore.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)
	require.Zero(t, fake.count(), "a rolled-back transaction must publish nothing")
}
