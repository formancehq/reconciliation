package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	pkgErrors "github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// OpenAlertInput is the payload OpenOrUpdateAlert consumes. Same shape on
// first open, re-open, and on-going failures — the storage layer figures out
// what kind of transition is happening based on the current row state.
type OpenAlertInput struct {
	RuleID       uuid.UUID
	Fingerprint  string
	Severity     models.Severity
	EvaluationID uuid.UUID
	Evidence     json.RawMessage // jsonb
	Labels       map[string]string
	OccurredAt   time.Time
}

// OpenAlertResult tells the caller what kind of transition happened so it can
// fire the matching downstream signal (toast in the UI, webhook payload).
type OpenAlertResult struct {
	Alert    *models.Alert
	Event    *models.AlertEvent
	Created  bool // true only on first-ever fail for this (rule, fingerprint)
	Reopened bool // true on transition from RESOLVED → OPEN
}

// OpenOrUpdateAlert is the single dedup-aware write the EvaluationService
// calls for every FAILING outcome. Three paths, decided from the current row:
//
//  1. No existing alert → INSERT new (Created=true), append `fail` event with
//     prev_status=NULL.
//  2. Existing RESOLVED alert → reopen in place (status back to OPEN, clear
//     resolution, occurrence_count++), append `fail` event with
//     prev_status=RESOLVED. Reopened=true.
//  3. Existing OPEN/ACK alert → update in place (occurrence_count++, fresh
//     evidence + eval id), append `fail` event with prev_status=new_status.
//
// SELECT FOR UPDATE serialises concurrent writers. The unique constraint on
// (rule_id, fingerprint) is the structural backstop. Concurrent first-opens
// race the INSERT — one wins, the other gets ErrDuplicateKeyValue and we
// retry; the retry's SELECT now finds the row and falls into path 3.
func (s *Storage) OpenOrUpdateAlert(ctx context.Context, in OpenAlertInput) (*OpenAlertResult, error) {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}

	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := s.openOrUpdateAlertOnce(ctx, in)
		if err == nil {
			return result, nil
		}
		if attempt < maxAttempts && errors.Is(err, ErrDuplicateKeyValue) {
			continue
		}
		return nil, err
	}
	return nil, errors.New("OpenOrUpdateAlert: retry budget exhausted")
}

func (s *Storage) openOrUpdateAlertOnce(ctx context.Context, in OpenAlertInput) (*OpenAlertResult, error) {
	var result *OpenAlertResult
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var current models.Alert
		err := tx.NewSelect().
			Model(&current).
			Where("rule_id = ? AND fingerprint = ?", in.RuleID, in.Fingerprint).
			For("UPDATE").
			Scan(ctx)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Path 1 — first-ever fail. INSERT, append inaugural event.
			fresh := &models.Alert{
				ID:               uuid.New(),
				RuleID:           in.RuleID,
				Fingerprint:      in.Fingerprint,
				Status:           models.AlertOpen,
				Severity:         in.Severity,
				FirstSeenAt:      in.OccurredAt,
				LastSeenAt:       in.OccurredAt,
				OccurrenceCount:  1,
				LastEvaluationID: in.EvaluationID,
				Evidence:         in.Evidence,
				Labels:           in.Labels,
			}
			if _, ierr := tx.NewInsert().Model(fresh).Exec(ctx); ierr != nil {
				return e("insert alert", ierr)
			}
			event, aerr := appendAlertEvent(ctx, tx, fresh.ID, models.AlertEventFail, nil, models.AlertOpen, &in.EvaluationID, in.Evidence, in.OccurredAt)
			if aerr != nil {
				return aerr
			}
			result = &OpenAlertResult{Alert: fresh, Event: event, Created: true}
			return nil

		case err != nil:
			return e("lookup alert", err)
		}

		prev := current.Status
		reopened := prev == models.AlertResolved

		// Paths 2 & 3 share the same UPDATE shape: occurrence_count++,
		// status set to OPEN (no-op on already-OPEN, demotes RESOLVED back
		// to OPEN, demotes ACK back to OPEN — the latter is intentional, a
		// fresh failure should re-surface even if previously acked).
		q := tx.NewUpdate().
			Model(&current).
			Set("status = ?", string(models.AlertOpen)).
			Set("last_seen_at = ?", in.OccurredAt).
			Set("last_evaluation_id = ?", in.EvaluationID).
			Set("occurrence_count = occurrence_count + 1").
			Set("evidence = ?", in.Evidence).
			Where("id = ?", current.ID).
			Returning("*")
		if reopened {
			// Clear the prior resolution — its audit lives in alert_event.
			q = q.Set("resolution = NULL").Set("ack = NULL")
		}
		if _, uerr := q.Exec(ctx); uerr != nil {
			return e("update alert", uerr)
		}
		// bun's Returning("*") into a struct pointer fills nullable JSONB
		// columns with zero-valued *Resolution / *Ack instead of nil. Mirror
		// the actual DB state in Go so callers can rely on
		// alert.Resolution == nil after a reopen.
		if reopened {
			current.Resolution = nil
			current.Ack = nil
		}

		event, aerr := appendAlertEvent(ctx, tx, current.ID, models.AlertEventFail, &prev, models.AlertOpen, &in.EvaluationID, in.Evidence, in.OccurredAt)
		if aerr != nil {
			return aerr
		}
		result = &OpenAlertResult{Alert: &current, Event: event, Created: false, Reopened: reopened}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ListActiveAlertFingerprints returns the fingerprints of every OPEN /
// ACKNOWLEDGED alert for the rule. The evaluation service uses this to
// auto-resolve alerts whose fingerprint disappears from the next round of
// outcomes — e.g. when both sides of a `ledger_vs_pool_drift` invariant clear
// to zero and the asset is no longer in either source's balance map.
func (s *Storage) ListActiveAlertFingerprints(ctx context.Context, ruleID uuid.UUID) ([]string, error) {
	var fps []string
	err := s.db.NewSelect().
		Model((*models.Alert)(nil)).
		Column("fingerprint").
		Where("rule_id = ?", ruleID).
		Where("status IN (?, ?)", string(models.AlertOpen), string(models.AlertAcknowledged)).
		Scan(ctx, &fps)
	if err != nil {
		return nil, e("list active alert fingerprints", err)
	}
	return fps, nil
}

// AutoResolveAlert closes the active alert (if any) for (rule_id, fingerprint)
// with resolution.kind = "auto", and appends a `pass` event. No-op + nil
// return when no active alert exists.
func (s *Storage) AutoResolveAlert(ctx context.Context, ruleID uuid.UUID, fingerprint string, evaluationID uuid.UUID, at time.Time) (*models.Alert, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	resolution := &models.Resolution{
		Kind: models.ResolutionAuto,
		By:   "system",
		At:   at,
	}
	var resolved *models.Alert
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var current models.Alert
		serr := tx.NewSelect().
			Model(&current).
			Where("rule_id = ? AND fingerprint = ?", ruleID, fingerprint).
			Where("status IN (?, ?)", string(models.AlertOpen), string(models.AlertAcknowledged)).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(serr, sql.ErrNoRows) {
			return nil // nothing to do — alert already resolved or doesn't exist
		}
		if serr != nil {
			return e("lookup alert for auto-resolve", serr)
		}
		prev := current.Status
		if _, uerr := tx.NewUpdate().
			Model(&current).
			Set("status = ?", string(models.AlertResolved)).
			Set("resolution = ?", resolution).
			Set("last_evaluation_id = ?", evaluationID).
			Where("id = ?", current.ID).
			Returning("*").
			Exec(ctx); uerr != nil {
			return e("auto-resolve alert", uerr)
		}
		payload, _ := json.Marshal(resolution)
		if _, aerr := appendAlertEvent(ctx, tx, current.ID, models.AlertEventPass, &prev, models.AlertResolved, &evaluationID, payload, at); aerr != nil {
			return aerr
		}
		resolved = &current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

// AckAlert transitions OPEN → ACKNOWLEDGED with the supplied Ack metadata and
// appends an `ack` event. Idempotent: re-ACK of an already-ACKNOWLEDGED alert
// returns the existing row UNCHANGED and does NOT append a new event — the
// original ack metadata (who/when/why) is the audit trail and must not be
// overwritten by a second ack call. Rejects on RESOLVED.
func (s *Storage) AckAlert(ctx context.Context, id uuid.UUID, ack *models.Ack) (*models.Alert, error) {
	var alert models.Alert
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewSelect().
			Model(&alert).
			Where("id = ?", id).
			For("UPDATE").
			Scan(ctx); err != nil {
			return e("ack alert", err)
		}
		if alert.Status == models.AlertResolved {
			return e("ack alert", ErrNotFound)
		}
		if alert.Status == models.AlertAcknowledged {
			return nil // no-op, preserve original ack
		}
		prev := alert.Status
		if _, err := tx.NewUpdate().
			Model(&alert).
			Set("status = ?", string(models.AlertAcknowledged)).
			Set("ack = ?", ack).
			Where("id = ?", id).
			Returning("*").
			Exec(ctx); err != nil {
			return e("ack alert", err)
		}
		payload, _ := json.Marshal(ack)
		if _, err := appendAlertEvent(ctx, tx, alert.ID, models.AlertEventAck, &prev, models.AlertAcknowledged, nil, payload, ack.At); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &alert, nil
}

// ResolveAlertManual closes the alert with kind = "fixed_by_booking" (or
// "auto" if called by the system path — AutoResolveAlert is the usual entry
// point for that). Appends a `resolve` event. Rejects on already-resolved.
func (s *Storage) ResolveAlertManual(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Alert, error) {
	if resolution.Kind != models.ResolutionFixedByBooking && resolution.Kind != models.ResolutionAuto {
		return nil, fmt.Errorf("ResolveAlertManual: unsupported resolution kind %q", resolution.Kind)
	}
	return s.applyResolution(ctx, id, resolution, models.AlertEventResolve)
}

// AcceptAlert closes the alert with kind = "accepted_by_business". Note is
// required; the service layer enforces evidence-snapshot capture. Appends an
// `accept` event.
func (s *Storage) AcceptAlert(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Alert, error) {
	if resolution.Kind != models.ResolutionAcceptedByBusiness {
		return nil, fmt.Errorf("AcceptAlert: expected kind %q, got %q", models.ResolutionAcceptedByBusiness, resolution.Kind)
	}
	if resolution.Note == "" {
		return nil, fmt.Errorf("AcceptAlert: note is required")
	}
	return s.applyResolution(ctx, id, resolution, models.AlertEventAccept)
}

func (s *Storage) applyResolution(ctx context.Context, id uuid.UUID, resolution *models.Resolution, eventType models.AlertEventType) (*models.Alert, error) {
	var alert models.Alert
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewSelect().
			Model(&alert).
			Where("id = ?", id).
			Where("status IN (?, ?)", string(models.AlertOpen), string(models.AlertAcknowledged)).
			For("UPDATE").
			Scan(ctx); err != nil {
			return e("resolve alert", err)
		}
		prev := alert.Status
		if _, err := tx.NewUpdate().
			Model(&alert).
			Set("status = ?", string(models.AlertResolved)).
			Set("resolution = ?", resolution).
			Where("id = ?", id).
			Returning("*").
			Exec(ctx); err != nil {
			return e("resolve alert", err)
		}
		payload, _ := json.Marshal(resolution)
		if _, err := appendAlertEvent(ctx, tx, alert.ID, eventType, &prev, models.AlertResolved, nil, payload, resolution.At); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &alert, nil
}

// GetAlert returns the alert by id or ErrNotFound.
func (s *Storage) GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error) {
	var alert models.Alert
	err := s.db.NewSelect().Model(&alert).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, e("get alert", err)
	}
	return &alert, nil
}

// appendAlertEvent is the single insertion point for the alert_event log.
// Centralised so every transition writes exactly the same shape and so a
// future hash-chain implementation only needs to instrument this one func.
// `at` is the timestamp the event represents (evaluation end time, ack time);
// `created_at` is a DB-side DEFAULT so it can't drift from the row's actual
// commit time.
func appendAlertEvent(
	ctx context.Context,
	tx bun.IDB,
	alertID uuid.UUID,
	eventType models.AlertEventType,
	prevStatus *models.AlertStatus,
	newStatus models.AlertStatus,
	evaluationID *uuid.UUID,
	payload json.RawMessage,
	at time.Time,
) (*models.AlertEvent, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	event := &models.AlertEvent{
		ID:           uuid.New(),
		AlertID:      alertID,
		EvaluationID: evaluationID,
		Type:         eventType,
		PrevStatus:   prevStatus,
		NewStatus:    newStatus,
		Payload:      payload,
		At:           at,
	}
	if _, err := tx.NewInsert().Model(event).Exec(ctx); err != nil {
		return nil, e("append alert event", err)
	}
	return event, nil
}

// ListAlertEvents returns every event for an alert, most recent first.
// Cursor-paginated for UIs that need to walk long histories. The append-only
// nature of the table means the result is stable: an event row, once written,
// is never modified.
func (s *Storage) ListAlertEvents(ctx context.Context, alertID uuid.UUID) ([]models.AlertEvent, error) {
	var events []models.AlertEvent
	err := s.db.NewSelect().
		Model(&events).
		Where("alert_id = ?", alertID).
		Order("at DESC", "id DESC").
		Scan(ctx)
	if err != nil {
		return nil, e("list alert events", err)
	}
	return events, nil
}

func (s *Storage) buildAlertListQuery(selectQuery *bun.SelectQuery, where string, args []any) *bun.SelectQuery {
	selectQuery = selectQuery.Order("last_seen_at DESC")
	if where != "" {
		return selectQuery.Where(where, args...)
	}
	return selectQuery
}

// ListAlerts returns a cursor-paginated set. Filters: id, ruleID, status,
// severity, fingerprint, firstSeenAt, lastSeenAt.
func (s *Storage) ListAlerts(ctx context.Context, q GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error) {
	var (
		where string
		args  []any
		err   error
	)
	if q.Options.QueryBuilder != nil {
		where, args, err = s.alertQueryContext(q.Options.QueryBuilder)
		if err != nil {
			return nil, err
		}
	}
	return paginateWithOffset[PaginatedQueryOptions[AlertsFilters], models.Alert](s, ctx,
		(*bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[AlertsFilters]])(&q),
		func(query *bun.SelectQuery) *bun.SelectQuery {
			return s.buildAlertListQuery(query, where, args)
		},
	)
}

func (s *Storage) alertQueryContext(qb query.Builder) (string, []any, error) {
	return qb.Build(query.ContextFn(func(key, operator string, value any) (string, []any, error) {
		switch key {
		case "id", "status", "severity", "fingerprint":
			if operator != "$match" {
				return "", nil, pkgErrors.Wrap(ErrInvalidQuery, key+" can only be used with $match")
			}
			return fmt.Sprintf("%s = ?", key), []any{value}, nil
		case "ruleID":
			if operator != "$match" {
				return "", nil, pkgErrors.Wrap(ErrInvalidQuery, "'ruleID' can only be used with $match")
			}
			return "rule_id = ?", []any{value}, nil
		case "firstSeenAt", "lastSeenAt":
			col := map[string]string{"firstSeenAt": "first_seen_at", "lastSeenAt": "last_seen_at"}[key]
			return fmt.Sprintf("%s %s ?", col, query.DefaultComparisonOperatorsMapping[operator]), []any{value}, nil
		default:
			return "", nil, pkgErrors.Wrapf(ErrInvalidQuery, "unknown key '%s' when building alert query", key)
		}
	}))
}

type AlertsFilters struct{}

type GetAlertsQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[AlertsFilters]]

func NewGetAlertsQuery(opts PaginatedQueryOptions[AlertsFilters]) GetAlertsQuery {
	return GetAlertsQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}
