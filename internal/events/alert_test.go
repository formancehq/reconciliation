package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func statusPtr(s models.AlertStatus) *models.AlertStatus { return &s }

func TestEventTypeFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		event models.AlertEvent
		want  string
	}{
		{
			name:  "first fail (prev nil) → opened",
			event: models.AlertEvent{Type: models.AlertEventFail, PrevStatus: nil, NewStatus: models.AlertOpen},
			want:  EventTypeAlertOpened,
		},
		{
			name:  "fail while OPEN → updated",
			event: models.AlertEvent{Type: models.AlertEventFail, PrevStatus: statusPtr(models.AlertOpen), NewStatus: models.AlertOpen},
			want:  EventTypeAlertUpdated,
		},
		{
			name:  "fail while ACKNOWLEDGED → updated",
			event: models.AlertEvent{Type: models.AlertEventFail, PrevStatus: statusPtr(models.AlertAcknowledged), NewStatus: models.AlertOpen},
			want:  EventTypeAlertUpdated,
		},
		{
			name:  "fail after RESOLVED → reopened",
			event: models.AlertEvent{Type: models.AlertEventFail, PrevStatus: statusPtr(models.AlertResolved), NewStatus: models.AlertOpen},
			want:  EventTypeAlertReopened,
		},
		{
			name:  "ack → acknowledged",
			event: models.AlertEvent{Type: models.AlertEventAck, PrevStatus: statusPtr(models.AlertOpen), NewStatus: models.AlertAcknowledged},
			want:  EventTypeAlertAcknowledged,
		},
		{
			name:  "pass (auto-resolve) → resolved",
			event: models.AlertEvent{Type: models.AlertEventPass, PrevStatus: statusPtr(models.AlertOpen), NewStatus: models.AlertResolved},
			want:  EventTypeAlertResolved,
		},
		{
			name:  "resolve (fixed_by_booking) → resolved",
			event: models.AlertEvent{Type: models.AlertEventResolve, PrevStatus: statusPtr(models.AlertAcknowledged), NewStatus: models.AlertResolved},
			want:  EventTypeAlertResolved,
		},
		{
			name:  "accept → accepted",
			event: models.AlertEvent{Type: models.AlertEventAccept, PrevStatus: statusPtr(models.AlertOpen), NewStatus: models.AlertResolved},
			want:  EventTypeAlertAccepted,
		},
		{
			name:  "unknown type → empty (do not publish)",
			event: models.AlertEvent{Type: models.AlertEventType("bogus")},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, EventTypeFor(&tc.event))
		})
	}
}

// recordingPublisher captures published messages for assertion.
type recordingPublisher struct {
	topics []string
	msgs   []*message.Message
	err    error
}

func (r *recordingPublisher) Publish(topic string, messages ...*message.Message) error {
	if r.err != nil {
		return r.err
	}
	r.topics = append(r.topics, topic)
	r.msgs = append(r.msgs, messages...)
	return nil
}
func (r *recordingPublisher) Close() error { return nil }

func TestPublisher_PublishAlertEvent(t *testing.T) {
	t.Parallel()

	alert := &models.Alert{
		ID:       uuid.New(),
		Status:   models.AlertResolved,
		Evidence: json.RawMessage(`{"delta":"100"}`),
	}
	event := &models.AlertEvent{
		ID:         uuid.New(),
		AlertID:    alert.ID,
		Type:       models.AlertEventResolve,
		PrevStatus: statusPtr(models.AlertOpen),
		NewStatus:  models.AlertResolved,
		At:         time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC),
	}

	rec := &recordingPublisher{}
	pub := NewPublisher(rec)
	pub.PublishAlertEvent(context.Background(), alert, event)

	require.Len(t, rec.msgs, 1, "exactly one message per event row")
	require.Equal(t, []string{Topic}, rec.topics)
	require.Equal(t, event.ID.String(), rec.msgs[0].UUID)

	// The envelope round-trips and carries app/version/type + full alert+event.
	var env publish.EventMessage
	require.NoError(t, json.Unmarshal(rec.msgs[0].Payload, &env))
	require.Equal(t, EventApp, env.App)
	require.Equal(t, EventVersion, env.Version)
	require.Equal(t, EventTypeAlertResolved, env.Type)
	require.Equal(t, event.ID.String(), env.IdempotencyKey)
	require.True(t, event.At.Equal(env.Date))

	payloadBytes, err := json.Marshal(env.Payload)
	require.NoError(t, err)
	var payload AlertEventPayload
	require.NoError(t, json.Unmarshal(payloadBytes, &payload))
	require.Equal(t, alert.ID, payload.Alert.ID)
	require.Equal(t, event.ID, payload.Event.ID)
	require.JSONEq(t, `{"delta":"100"}`, string(payload.Alert.Evidence))
}

func TestPublisher_NilSafe(t *testing.T) {
	t.Parallel()
	// nil *Publisher and nil inner publisher are both no-ops, not panics.
	var nilPub *Publisher
	require.NotPanics(t, func() {
		nilPub.PublishAlertEvent(context.Background(), &models.Alert{}, &models.AlertEvent{ID: uuid.New(), Type: models.AlertEventFail})
	})
	require.NotPanics(t, func() {
		NewPublisher(nil).PublishAlertEvent(context.Background(), &models.Alert{}, &models.AlertEvent{ID: uuid.New(), Type: models.AlertEventFail})
	})
}

func TestPublisher_UnknownEventTypeNotPublished(t *testing.T) {
	t.Parallel()
	rec := &recordingPublisher{}
	NewPublisher(rec).PublishAlertEvent(context.Background(), &models.Alert{ID: uuid.New()},
		&models.AlertEvent{ID: uuid.New(), Type: models.AlertEventType("bogus")})
	require.Empty(t, rec.msgs, "unmapped event types must not be published")
}
