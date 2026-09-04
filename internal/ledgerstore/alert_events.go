package ledgerstore

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
)

// alertEventNamespace anchors the deterministic ids the projection synthesises
// for alert events. A ledger event is a transaction, not a row, so it has no
// natural uuid — the id is a stable function of (alertID, ledger sequence), so
// the same event keeps the same id across reads.
var alertEventNamespace = uuid.MustParse("6f9c2d7e-4a1b-4c3d-8e5f-0a1b2c3d4e5f")

// transitionEventType maps a ledger transition (the envelope type suffix) to the
// caller-facing AlertEventType. opened/occurred/reopened are all failing-eval
// events — a reopen is a fail whose prevStatus is RESOLVED (see
// AlertEvent.IsReopen), not a distinct type; auto_resolved is the passing-eval
// event. ok=false for a suffix we don't recognise.
func transitionEventType(transition string) (models.AlertEventType, bool) {
	switch transitionType(transition) {
	case transitionOpened, transitionOccurred, transitionReopened:
		return models.AlertEventFail, true
	case transitionAutoResolved:
		return models.AlertEventPass, true
	case transitionAcknowledged:
		return models.AlertEventAck, true
	case transitionResolved:
		return models.AlertEventResolve, true
	case transitionAccepted:
		return models.AlertEventAccept, true
	case transitionSnoozed:
		return models.AlertEventSnooze, true
	case transitionUnsnoozed:
		return models.AlertEventUnsnooze, true
	default:
		return "", false
	}
}

// alertEventFromActivity projects one rule activity (a committed transition
// transaction) into an AlertEvent, iff the activity is a transition of the given
// alert. ok=false skips everything else — evaluations, other alerts' events, and
// unrecognised kinds.
func alertEventFromActivity(act models.RuleActivity, alertID uuid.UUID) (models.AlertEvent, bool) {
	if act.Category != "alert" {
		return models.AlertEvent{}, false
	}
	var env transitionEvent
	if err := json.Unmarshal(act.Payload, &env); err != nil {
		return models.AlertEvent{}, false
	}
	if env.AlertID != alertID.String() {
		return models.AlertEvent{}, false
	}
	if len(env.Type) <= len(transitionEventTypePrefix) {
		return models.AlertEvent{}, false
	}
	eventType, ok := transitionEventType(env.Type[len(transitionEventTypePrefix):])
	if !ok {
		return models.AlertEvent{}, false
	}

	ev := models.AlertEvent{
		ID:        uuid.NewSHA1(alertEventNamespace, []byte(alertID.String()+":"+act.Sequence)),
		AlertID:   alertID,
		Type:      eventType,
		NewStatus: models.AlertStatus(env.NewStatus),
		At:        env.OccurredAt,
		CreatedAt: act.RecordedAt,
		// Read-side projection: write-time notification suppression is not modelled
		// here (see docs/technical/notification-suppression.md). Every recorded
		// transition is surfaced; a suppressing consumer decides what to publish.
		Notify: true,
		// act.Sequence is the ledger transaction id (activityFromTransaction sets it
		// from tx.GetId()) — the identifier of the write, not the audit sequence.
		TransactionID: act.Sequence,
	}
	if env.PrevStatus != "" {
		prev := models.AlertStatus(env.PrevStatus)
		ev.PrevStatus = &prev
	}
	if env.CorrelationID != "" {
		if evalID, err := uuid.Parse(env.CorrelationID); err == nil {
			ev.EvaluationID = &evalID
		}
	}
	if len(env.Payload) > 0 {
		if b, err := json.Marshal(env.Payload); err == nil {
			ev.Payload = b
		}
	}
	return ev, true
}

// ListAlertEvents projects one alert's lifecycle history from the control
// ledger's activity stream — the same committed transitions the rule timeline is
// built from, filtered to this alert and shaped as append-only events, newest
// first, offset-paginated. Every event carries its TransactionID (the ledger
// write behind the transition), which is covered by the ledger's signed audit
// chain — so an auditor can pull an alert's whole history and locate each write.
//
// The scan is over the rule's activity account (O(rule history) per page, as the
// rule timeline already is); an alert-keyed ledger index would make it O(alert),
// deferred as a follow-up.
func (s *LedgerStore) ListAlertEvents(ctx context.Context, alertID uuid.UUID, q store.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error) {
	alert, err := s.GetAlert(ctx, alertID)
	if err != nil {
		return nil, err
	}

	events := make([]models.AlertEvent, 0, 32)
	if err := s.client.ListTransactionsFunc(ctx, s.controlLedger, schema.FilterAddressPrefix(schema.ActivityAccount(alert.RuleID.String())), func(tx *commonpb.Transaction) error {
		act, ok := activityFromTransaction(tx)
		if !ok {
			return nil
		}
		if ev, ok := alertEventFromActivity(act, alertID); ok {
			events = append(events, ev)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("list events for alert %s: %w", alertID, err)
	}

	slices.SortFunc(events, func(a, b models.AlertEvent) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		// Tiebreak on the ledger transaction id, newest first — a burst of
		// transitions can share an instant, but the tx id is monotonic.
		as, _ := strconv.ParseUint(a.TransactionID, 10, 64)
		bs, _ := strconv.ParseUint(b.TransactionID, 10, 64)
		switch {
		case bs < as:
			return -1
		case bs > as:
			return 1
		default:
			return 0
		}
	})

	data, more := paginateSlice(events, q.Offset, q.PageSize)
	return offsetCursor(bunpaginate.OffsetPaginatedQuery[store.PaginatedQueryOptions[store.AlertEventsFilters]](q), data, more), nil
}
