// Package events turns alert lifecycle transitions into outbound messages on
// the Formance message bus, consumed by the Webhooks module exactly like every
// other Formance domain event (ledger.*, payments.*). It is the single place
// that knows how an alert_event row maps to a public webhook event name and
// what the wire payload looks like.
package events

import (
	"context"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	logging "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/models"
)

const (
	// EventApp is the `app` field of every published message. The Webhooks
	// worker lowercases it and prepends it to the type, so a message with
	// App="reconciliation" and Type="alert.opened" is matched by subscribers
	// against the event name "reconciliation.alert.opened". Mirrors
	// cmd.ServiceName (kept as a literal here to avoid importing cmd).
	EventApp = "reconciliation"
	// EventVersion tags the payload schema. Bumped only on a breaking change
	// to AlertEventPayload.
	EventVersion = "v1"
	// Topic is the logical topic every alert event is published to. The
	// operator's --publisher-topic-mapping routes it to the physical
	// subject the Webhooks module subscribes to (a single `*:<subject>` or
	// `reconciliation:<subject>` mapping covers all six event types — the
	// `type` field discriminates them downstream).
	Topic = "reconciliation"
)

// Event type suffixes. The consumer-facing event name is EventApp + "." +
// <suffix> (assembled by the Webhooks worker), e.g. "reconciliation.alert.opened".
// The full table lives in docs/technical/api.md §Events.
const (
	EventTypeAlertOpened       = "alert.opened"
	EventTypeAlertUpdated      = "alert.updated"
	EventTypeAlertAcknowledged = "alert.acknowledged"
	EventTypeAlertResolved     = "alert.resolved"
	EventTypeAlertAccepted     = "alert.accepted"
	EventTypeAlertReopened     = "alert.reopened"
	EventTypeAlertSnoozed      = "alert.snoozed"
	EventTypeAlertUnsnoozed    = "alert.unsnoozed"
)

// AlertEventPayload is the wire shape carried by every reconciliation.alert.*
// event. It pairs the alert's current state (which embeds the latest
// evaluation's evidence on Alert.Evidence) with the append-only event row that
// triggered the publish — giving consumers the transition (prev→new status,
// resolution/ack detail in Event.Payload, originating evaluation id) without a
// second API round-trip.
type AlertEventPayload struct {
	Alert *models.Alert      `json:"alert"`
	Event *models.AlertEvent `json:"event"`
}

// EventTypeFor maps an alert_event row to its webhook event type suffix. It is
// a pure function of the row — the single source of truth for the lifecycle →
// event-name mapping documented in docs/technical/api.md §Events. Returns ""
// for an unrecognised row, which the publisher treats as "do not publish".
//
//	fail  + prev=NULL      → opened
//	fail  + prev=RESOLVED  → reopened   (Event.IsReopen())
//	fail  + prev=OPEN/ACK  → updated
//	ack                    → acknowledged
//	pass                   → resolved   (auto-resolve)
//	resolve                → resolved   (fixed_by_booking)
//	accept                 → accepted
//	snooze                 → snoozed
//	unsnooze               → unsnoozed
func EventTypeFor(e *models.AlertEvent) string {
	switch e.Type {
	case models.AlertEventFail:
		switch {
		case e.PrevStatus == nil:
			return EventTypeAlertOpened
		case e.IsReopen():
			return EventTypeAlertReopened
		default:
			return EventTypeAlertUpdated
		}
	case models.AlertEventAck:
		return EventTypeAlertAcknowledged
	case models.AlertEventPass, models.AlertEventResolve:
		return EventTypeAlertResolved
	case models.AlertEventAccept:
		return EventTypeAlertAccepted
	case models.AlertEventSnooze:
		return EventTypeAlertSnoozed
	case models.AlertEventUnsnooze:
		return EventTypeAlertUnsnoozed
	default:
		return ""
	}
}

// NewAlertEventMessage builds the publish.EventMessage envelope for a
// transition. eventType must be a non-empty suffix (see EventTypeFor).
func NewAlertEventMessage(eventType string, alert *models.Alert, event *models.AlertEvent) publish.EventMessage {
	return publish.EventMessage{
		// The event row id is globally unique per transition — a natural
		// idempotency key for downstream dedup.
		IdempotencyKey: event.ID.String(),
		Date:           event.At,
		App:            EventApp,
		Version:        EventVersion,
		Type:           eventType,
		Payload:        AlertEventPayload{Alert: alert, Event: event},
	}
}

// Publisher dispatches alert events to the message bus. It wraps the
// topic-mapped message.Publisher provided by messagingfx; a nil inner
// publisher (messaging not configured) turns every publish into a no-op so the
// service runs cleanly without a broker.
type Publisher struct {
	publisher message.Publisher
}

// NewPublisher wraps p. p may be nil (messaging disabled) — see Publisher.
func NewPublisher(p message.Publisher) *Publisher {
	return &Publisher{publisher: p}
}

// PublishAlertEvent makes one publish attempt for one alert_event row. Broker
// retries may replay it. Failures are logged, never returned: the DB transition
// has already committed by the time this runs, so a transient bus error must
// not surface as a request error. Webhooks retries delivery on its side; the
// at-least-once gap (commit succeeds, publish fails) is the accepted trade-off
// — same as the Ledger.
func (pub *Publisher) PublishAlertEvent(ctx context.Context, alert *models.Alert, event *models.AlertEvent) {
	if pub == nil || pub.publisher == nil {
		return
	}
	eventType := EventTypeFor(event)
	if eventType == "" {
		logging.FromContext(ctx).Errorf(
			"reconciliation: no webhook event type for alert_event %s (type=%s) — skipping publish",
			event.ID, event.Type)
		return
	}
	msg := publish.NewMessage(ctx, NewAlertEventMessage(eventType, alert, event))
	// Keep the transport-level message identifier stable across publisher
	// replays and across pods. The payload idempotency key carries the same
	// value for consumers that deduplicate at the event-envelope layer.
	msg.UUID = event.ID.String()
	logging.FromContext(ctx).WithFields(map[string]any{
		"alert":      alert.ID,
		"alertEvent": event.ID,
		"type":       eventType,
	}).Debugf("publishing reconciliation alert event")
	if err := pub.publisher.Publish(Topic, msg); err != nil {
		logging.FromContext(ctx).Errorf(
			"reconciliation: publishing alert event %s (%s.%s): %s",
			event.ID, EventApp, eventType, err)
	}
}
