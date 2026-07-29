package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	pkgErrors "github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// OpenAlertInput is the payload OpenOrUpdateAlert consumes. Same shape on
// first open, re-open, and on-going failures — the storage layer figures out
// what kind of transition is happening based on the current row state.
type OpenAlertInput struct {
	RuleID      uuid.UUID
	Fingerprint string
	// PeriodID scopes the alert to a reconciliation period. Empty defaults to
	// models.ContinuousPeriod, which reproduces the original
	// (rule_id, fingerprint) dedup. The caller (the evaluation service)
	// derives it from the rule's cadence and the evaluation PIT.
	PeriodID     string
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
	Created  bool // true only on the first fail for this (rule, fingerprint, period)
	Reopened bool // true on a within-period transition from RESOLVED → OPEN
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
// All three are scoped to in.PeriodID: the same fingerprint failing in a new
// period is a fresh case (path 1), never a reopen of a prior period's alert.
//
// SELECT FOR UPDATE serialises concurrent writers. The unique constraint on
// (rule_id, fingerprint, period_id) is the structural backstop. Concurrent
// first-opens race the INSERT — one wins, the other gets ErrDuplicateKeyValue
// and we retry; the retry's SELECT now finds the row and falls into path 3.
func (s *Storage) OpenOrUpdateAlert(ctx context.Context, in OpenAlertInput) (*OpenAlertResult, error) {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	if in.PeriodID == "" {
		in.PeriodID = models.ContinuousPeriod
	}

	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := s.openOrUpdateAlertOnce(ctx, in)
		if err == nil {
			// Emit only the committed attempt's event: a retried duplicate-key
			// race rolled its first attempt back and produced no row.
			s.recordAlertEvent(ctx, result.Alert, result.Event)
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
		// Lock-order invariant: chain lock BEFORE any alert row lock. See
		// Storage.lockAuditChain.
		if err := s.WithTx(tx).lockAuditChain(ctx); err != nil {
			return err
		}
		var current models.Alert
		err := tx.NewSelect().
			Model(&current).
			Where("rule_id = ? AND fingerprint = ? AND period_id = ?", in.RuleID, in.Fingerprint, in.PeriodID).
			For("UPDATE").
			Scan(ctx)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Path 1 — first fail for this fingerprint in this period. INSERT,
			// append inaugural event.
			fresh := &models.Alert{
				ID:               uuid.New(),
				RuleID:           in.RuleID,
				Fingerprint:      in.Fingerprint,
				PeriodID:         in.PeriodID,
				Status:           models.AlertOpen,
				Severity:         in.Severity,
				FirstSeenAt:      in.OccurredAt,
				LastSeenAt:       in.OccurredAt,
				OccurrenceCount:  1,
				LastEvaluationID: in.EvaluationID,
				Evidence:         in.Evidence,
				Labels:           in.Labels,
			}
			if _, ierr := tx.NewInsert().Model(fresh).Returning("*").Exec(ctx); ierr != nil {
				return e("insert alert", ierr)
			}
			// A first open always notifies.
			event, aerr := s.appendAlertEvent(ctx, tx, fresh, models.AlertEventFail, nil, models.AlertOpen, &in.EvaluationID, in.Evidence, in.OccurredAt, true)
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
		// Capture the evidence the alert is currently carrying BEFORE the
		// UPDATE below overwrites it via Returning("*"). The notify decision
		// compares it against the incoming evidence.
		prevEvidence := current.Evidence

		// Snooze state at evaluation time (captured before the UPDATE). An
		// ACTIVE snooze mutes this fail entirely — the operator asked for
		// silence even as the discrepancy moves. An EXPIRED snooze is cleared
		// here and the fail notifies once ("still failing after the mute
		// lapsed"). A reopen implies a prior resolve, which already cleared any
		// snooze, so current.Snooze is nil on that path.
		snoozeActive := current.Snooze != nil && in.OccurredAt.Before(current.Snooze.Until)
		snoozeExpired := current.Snooze != nil && !snoozeActive

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
		} else if prev == models.AlertAcknowledged {
			// Resurfacing an acknowledged alert (ACK → OPEN): the prior ack no
			// longer reflects the current, still-failing state. Clear it so the
			// row doesn't report an OPEN alert as still acknowledged; the
			// historical ack remains in alert_event.
			q = q.Set("ack = NULL")
		}
		if snoozeExpired {
			// The mute has lapsed — drop it so the alert pages normally again.
			q = q.Set("snooze = NULL")
		}
		if _, uerr := q.Exec(ctx); uerr != nil {
			return e("update alert", uerr)
		}
		// bun's Returning("*") into a struct pointer fills a jsonb column that
		// this UPDATE SET to NULL with a zero-valued *Resolution / *Ack /
		// *Snooze instead of nil. Mirror the actual DB state in Go so callers
		// can rely on alert.Resolution / alert.Snooze == nil.
		if reopened {
			current.Resolution = nil
			current.Ack = nil
		} else if prev == models.AlertAcknowledged {
			current.Ack = nil
		}
		if snoozeExpired {
			current.Snooze = nil
		}

		// Notification decision (flap suppression #1 — suppress repeats). A
		// steady-state repeat — an already-OPEN alert failing again with
		// materially-identical evidence — adds nothing a consumer hasn't
		// already been told, so it is logged but not published. Everything
		// that carries new information still notifies:
		//   - a reopen (prev=RESOLVED) — the case came back;
		//   - a resurfacing (prev=ACKNOWLEDGED, demoted to OPEN) — the ack no
		//     longer holds;
		//   - any change in evidence — the discrepancy moved.
		notify := reopened ||
			prev != models.AlertOpen ||
			!sameEvidenceJSON(prevEvidence, in.Evidence)
		// Snooze overrides the steady-state decision: an active mute silences
		// even a change or resurface; the first fail past expiry pages once.
		switch {
		case snoozeActive:
			notify = false
		case snoozeExpired:
			notify = true
		}

		event, aerr := s.appendAlertEvent(ctx, tx, &current, models.AlertEventFail, &prev, models.AlertOpen, &in.EvaluationID, in.Evidence, in.OccurredAt, notify)
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
// ACKNOWLEDGED alert for the rule WITHIN the given period. The evaluation
// service uses this to auto-resolve alerts whose fingerprint disappears from
// the next round of outcomes — e.g. when both sides of a `ledger_vs_pool_drift`
// invariant clear to zero and the asset is no longer in either source's balance
// map. Scoping to periodID is essential: a fresh evaluation of period N must
// never sweep (auto-resolve) a prior period's open cases — those stand as the
// historical reconciliation record for their own period.
func (s *Storage) ListActiveAlertFingerprints(ctx context.Context, ruleID uuid.UUID, periodID string) ([]string, error) {
	if periodID == "" {
		periodID = models.ContinuousPeriod
	}
	var fps []string
	err := s.db.NewSelect().
		Model((*models.Alert)(nil)).
		Column("fingerprint").
		Where("rule_id = ?", ruleID).
		Where("period_id = ?", periodID).
		Where("status IN (?, ?)", string(models.AlertOpen), string(models.AlertAcknowledged)).
		Scan(ctx, &fps)
	if err != nil {
		return nil, e("list active alert fingerprints", err)
	}
	return fps, nil
}

// AutoResolveAlert closes the active alert (if any) for
// (rule_id, fingerprint, period_id) with resolution.kind = "auto", and appends
// a `pass` event. No-op + nil return when no active alert exists in that period.
func (s *Storage) AutoResolveAlert(ctx context.Context, ruleID uuid.UUID, fingerprint, periodID string, evaluationID uuid.UUID, at time.Time) (*models.Alert, error) {
	if periodID == "" {
		periodID = models.ContinuousPeriod
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	resolution := &models.Resolution{
		Kind: models.ResolutionAuto,
		By:   "system",
		At:   at,
	}
	var (
		resolved *models.Alert
		event    *models.AlertEvent
	)
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock-order invariant: chain lock BEFORE any alert row lock. See
		// Storage.lockAuditChain.
		if err := s.WithTx(tx).lockAuditChain(ctx); err != nil {
			return err
		}
		var current models.Alert
		serr := tx.NewSelect().
			Model(&current).
			Where("rule_id = ? AND fingerprint = ? AND period_id = ?", ruleID, fingerprint, periodID).
			Where("status IN (?, ?)", string(models.AlertOpen), string(models.AlertAcknowledged)).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(serr, sql.ErrNoRows) {
			return nil // nothing to do — no active alert for this period
		}
		if serr != nil {
			return e("lookup alert for auto-resolve", serr)
		}
		prev := current.Status
		if _, uerr := tx.NewUpdate().
			Model(&current).
			Set("status = ?", string(models.AlertResolved)).
			Set("resolution = ?", resolution).
			Set("snooze = NULL").
			Set("last_evaluation_id = ?", evaluationID).
			Where("id = ?", current.ID).
			Returning("*").
			Exec(ctx); uerr != nil {
			return e("auto-resolve alert", uerr)
		}
		// A closed alert carries no snooze; clear any (see the Returning quirk
		// note in openOrUpdateAlertOnce).
		current.Snooze = nil
		payload, _ := json.Marshal(resolution)
		ev, aerr := s.appendAlertEvent(ctx, tx, &current, models.AlertEventPass, &prev, models.AlertResolved, &evaluationID, payload, at, true)
		if aerr != nil {
			return aerr
		}
		resolved, event = &current, ev
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.recordAlertEvent(ctx, resolved, event)
	return resolved, nil
}

// AckAlert transitions OPEN → ACKNOWLEDGED with the supplied Ack metadata and
// appends an `ack` event. Idempotent: re-ACK of an already-ACKNOWLEDGED alert
// returns the existing row UNCHANGED and does NOT append a new event — the
// original ack metadata (who/when/why) is the audit trail and must not be
// overwritten by a second ack call. Rejects on RESOLVED.
func (s *Storage) AckAlert(ctx context.Context, id uuid.UUID, ack *models.Ack) (*models.Alert, error) {
	var (
		alert models.Alert
		event *models.AlertEvent
	)
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock-order invariant: chain lock BEFORE any alert row lock. See
		// Storage.lockAuditChain.
		if err := s.WithTx(tx).lockAuditChain(ctx); err != nil {
			return err
		}
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
		ev, err := s.appendAlertEvent(ctx, tx, &alert, models.AlertEventAck, &prev, models.AlertAcknowledged, nil, payload, ack.At, true)
		if err != nil {
			return err
		}
		event = ev
		return nil
	})
	if err != nil {
		return nil, err
	}
	// event is nil on the idempotent re-ack no-op — recordAlertEvent skips it,
	// so a duplicate ack does not re-emit reconciliation.acknowledged_alert.
	s.recordAlertEvent(ctx, &alert, event)
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
	var (
		alert models.Alert
		event *models.AlertEvent
	)
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock-order invariant: chain lock BEFORE any alert row lock. See
		// Storage.lockAuditChain.
		if err := s.WithTx(tx).lockAuditChain(ctx); err != nil {
			return err
		}
		if err := tx.NewSelect().
			Model(&alert).
			Where("id = ?", id).
			Where("status IN (?, ?)", string(models.AlertOpen), string(models.AlertAcknowledged)).
			For("UPDATE").
			Scan(ctx); err != nil {
			return e("resolve alert", err)
		}
		prev := alert.Status
		// Freeze the accepted evidence from the row we just locked FOR UPDATE, so
		// the snapshot is atomic with the transition — a concurrent evaluation
		// updating the alert's evidence between a read and this write can't slip a
		// stale snapshot in. Only business acceptance snapshots; other kinds leave
		// it unset.
		if resolution.Kind == models.ResolutionAcceptedByBusiness && len(resolution.EvidenceSnapshot) == 0 {
			if len(alert.Evidence) > 0 {
				resolution.EvidenceSnapshot = alert.Evidence
			} else {
				resolution.EvidenceSnapshot = json.RawMessage("null")
			}
		}
		if _, err := tx.NewUpdate().
			Model(&alert).
			Set("status = ?", string(models.AlertResolved)).
			Set("resolution = ?", resolution).
			Set("snooze = NULL").
			Where("id = ?", id).
			Returning("*").
			Exec(ctx); err != nil {
			return e("resolve alert", err)
		}
		// A closed alert carries no snooze (see the Returning quirk note).
		alert.Snooze = nil
		payload, _ := json.Marshal(resolution)
		ev, err := s.appendAlertEvent(ctx, tx, &alert, eventType, &prev, models.AlertResolved, nil, payload, resolution.At, true)
		if err != nil {
			return err
		}
		event = ev
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.recordAlertEvent(ctx, &alert, event)
	return &alert, nil
}

// SnoozeAlert mutes an active alert's notifications until `until`, recording a
// `snooze` event. The alert keeps its status and keeps counting against
// period-green — only its notifications are suppressed (see openOrUpdateAlertOnce
// and recordAlertEvent). Rejects a non-future `until` and any non-active
// (RESOLVED) alert. Re-snoozing an already-snoozed alert overwrites the window
// with the new one — extending or shortening a mute is a legitimate operator
// action — and appends a fresh event.
func (s *Storage) SnoozeAlert(ctx context.Context, id uuid.UUID, until time.Time, by, note string) (*models.Alert, error) {
	now := time.Now().UTC()
	if !until.After(now) {
		return nil, fmt.Errorf("SnoozeAlert: until must be in the future")
	}
	snooze := &models.Snooze{Until: until.UTC(), By: by, At: now, Note: note}
	var (
		alert models.Alert
		event *models.AlertEvent
	)
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock-order invariant: chain lock BEFORE any alert row lock. See
		// Storage.lockAuditChain.
		if err := s.WithTx(tx).lockAuditChain(ctx); err != nil {
			return err
		}
		if err := tx.NewSelect().
			Model(&alert).
			Where("id = ?", id).
			Where("status IN (?, ?)", string(models.AlertOpen), string(models.AlertAcknowledged)).
			For("UPDATE").
			Scan(ctx); err != nil {
			return e("snooze alert", err)
		}
		status := alert.Status
		if _, err := tx.NewUpdate().
			Model(&alert).
			Set("snooze = ?", snooze).
			Where("id = ?", id).
			Returning("*").
			Exec(ctx); err != nil {
			return e("snooze alert", err)
		}
		payload, _ := json.Marshal(snooze)
		// Status-neutral transition: prev == new. Always notifies — the mute
		// itself is a one-off operator action worth surfacing.
		ev, aerr := s.appendAlertEvent(ctx, tx, &alert, models.AlertEventSnooze, &status, status, nil, payload, snooze.At, true)
		if aerr != nil {
			return aerr
		}
		event = ev
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.recordAlertEvent(ctx, &alert, event)
	return &alert, nil
}

// UnsnoozeAlert lifts a snooze early, recording an `unsnooze` event. Idempotent:
// unsnoozing an alert that is not snoozed returns it unchanged and appends no
// event (mirrors the re-ack no-op). `by` attributes the action in the log.
func (s *Storage) UnsnoozeAlert(ctx context.Context, id uuid.UUID, by string) (*models.Alert, error) {
	var (
		alert models.Alert
		event *models.AlertEvent
	)
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Lock-order invariant: chain lock BEFORE any alert row lock. See
		// Storage.lockAuditChain.
		if err := s.WithTx(tx).lockAuditChain(ctx); err != nil {
			return err
		}
		if err := tx.NewSelect().
			Model(&alert).
			Where("id = ?", id).
			For("UPDATE").
			Scan(ctx); err != nil {
			return e("unsnooze alert", err)
		}
		if alert.Snooze == nil {
			return nil // no-op — nothing to lift
		}
		status := alert.Status
		at := time.Now().UTC()
		if _, err := tx.NewUpdate().
			Model(&alert).
			Set("snooze = NULL").
			Where("id = ?", id).
			Returning("*").
			Exec(ctx); err != nil {
			return e("unsnooze alert", err)
		}
		alert.Snooze = nil // mirror the SET ... = NULL (see Returning quirk note)
		payload, _ := json.Marshal(map[string]string{"by": by})
		ev, aerr := s.appendAlertEvent(ctx, tx, &alert, models.AlertEventUnsnooze, &status, status, nil, payload, at, true)
		if aerr != nil {
			return aerr
		}
		event = ev
		return nil
	})
	if err != nil {
		return nil, err
	}
	// event is nil on the no-op (already un-snoozed) path — recordAlertEvent
	// skips it, so a redundant unsnooze emits nothing.
	s.recordAlertEvent(ctx, &alert, event)
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

// sameEvidenceJSON reports whether two evidence payloads are materially
// identical. It compares the CANONICAL form of each (object keys sorted,
// insignificant whitespace removed, numbers preserved verbatim) rather than the
// raw bytes: one side is freshly marshalled by a template, the other has been
// round-tripped through Postgres jsonb, and semantically-equal payloads
// routinely differ byte-for-byte across that boundary. Two empty payloads are
// equal; a payload that fails to parse is treated as DIFFERENT — the safe
// default is to notify when in doubt.
func sameEvidenceJSON(a, b json.RawMessage) bool {
	aEmpty, bEmpty := len(a) == 0, len(b) == 0
	if aEmpty || bEmpty {
		return aEmpty && bEmpty
	}
	ca, err := canonicalJSON(a)
	if err != nil {
		return false
	}
	cb, err := canonicalJSON(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ca, cb)
}

// canonicalJSON re-encodes raw JSON into a deterministic form: encoding/json
// marshals object keys in sorted order, and json.Number preserves numeric
// literals verbatim instead of coercing them to float64 (which would lose
// precision on the big-integer balances reconciliation evidence carries).
func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// appendAlertEvent is the single insertion point for the alert_event log — and,
// now, the single point at which an alert transition enters the hash chain and
// the single point at which the sealed-period barrier is enforced. The comment
// this replaces anticipated exactly that: centralised so a future hash-chain
// implementation only needs to instrument this one func.
//
// Being the only door is what makes both guarantees hold. There is no code path
// that can transition an alert without being journalled, and none that can write
// into a closed period, because there is no second way in.
//
// `at` is the timestamp the event represents (evaluation end time, ack time);
// `created_at` is a DB-side DEFAULT so it can't drift from the row's actual
// commit time.
func (s *Storage) appendAlertEvent(
	ctx context.Context,
	tx bun.Tx,
	alert *models.Alert,
	eventType models.AlertEventType,
	prevStatus *models.AlertStatus,
	newStatus models.AlertStatus,
	evaluationID *uuid.UUID,
	payload json.RawMessage,
	at time.Time,
	notify bool,
) (*models.AlertEvent, error) {
	store := s.WithTx(tx)

	// Take the chain lock BEFORE testing the barrier. Without it the test is
	// advisory only: it can pass while a seal is still uncommitted, and this
	// transition would then block on the same lock inside AppendAuditEntry, wake
	// up after the seal has committed, and append anyway — landing a case
	// movement in books that are already closed.
	if err := store.lockAuditChain(ctx); err != nil {
		return nil, err
	}

	// The closing barrier. A sealed period's cases do not move — that is what
	// "closed" means to an auditor, and it is the one property a journal alone
	// cannot provide. Under a periodic cadence the successor period opens a fresh
	// case, so this rejects only genuine writes-into-closed-books: a late
	// evaluation backdated into a sealed period, or an operator editing history.
	if err := store.assertPeriodWritable(ctx, alert.PeriodID,
		fmt.Sprintf("the %s transition of alert %s", eventType, alert.ID)); err != nil {
		return nil, err
	}

	if at.IsZero() {
		at = time.Now().UTC()
	}
	event := &models.AlertEvent{
		ID:           uuid.New(),
		AlertID:      alert.ID,
		EvaluationID: evaluationID,
		Type:         eventType,
		PrevStatus:   prevStatus,
		NewStatus:    newStatus,
		Payload:      payload,
		Notify:       notify,
		At:           at,
	}
	if _, err := tx.NewInsert().Model(event).Returning("*").Exec(ctx); err != nil {
		return nil, e("append alert event", err)
	}

	// The alert struct the caller holds reflects the post-transition row, so the
	// memento records the status and occurrence count as of this event rather
	// than as of some later read.
	snapshot := *alert
	snapshot.Status = newStatus
	memento, err := audit.NewAlertTransitionMemento(&snapshot, event)
	if err != nil {
		return nil, err
	}
	raw, err := audit.BuildMemento(memento)
	if err != nil {
		return nil, err
	}

	entry, err := store.AppendAuditEntry(ctx, AppendAuditInput{
		At:           at,
		Kind:         models.AuditAlertTransition,
		RuleID:       &alert.RuleID,
		AlertID:      &alert.ID,
		EvaluationID: evaluationID,
		PeriodID:     alert.PeriodID,
		Subject:      subjectForTransition(ctx, eventType),
		Memento:      raw,
	})
	if err != nil {
		return nil, err
	}

	if _, err := tx.NewUpdate().Model((*models.AlertEvent)(nil)).
		Set("audit_sequence = ?", entry.Sequence).
		Where("id = ?", event.ID).Exec(ctx); err != nil {
		return nil, e("link alert event to audit entry", err)
	}
	if _, err := tx.NewUpdate().Model((*models.Alert)(nil)).
		Set("audit_sequence = ?", entry.Sequence).
		Where("id = ?", alert.ID).Exec(ctx); err != nil {
		return nil, e("link alert to audit entry", err)
	}
	event.AuditSequence = &entry.Sequence
	alert.AuditSequence = &entry.Sequence
	return event, nil
}

// subjectForTransition attributes a transition.
//
// An engine-driven fail or pass with no caller on the context is the scheduler
// acting, and is recorded as a system component — which hashes differently from
// a caller-less request, so "the machine did this" is a cryptographic claim
// rather than a convention. When a caller IS present, even on a fail or pass,
// they get the attribution: a human who triggered an evaluation owns its
// consequences.
func subjectForTransition(ctx context.Context, eventType models.AlertEventType) models.Subject {
	subject := audit.SubjectFrom(ctx)
	if subject.Subject != "" || subject.Source != models.SubjectSourceUnknown {
		return subject
	}
	switch eventType {
	case models.AlertEventFail, models.AlertEventPass:
		return models.SystemSubject(audit.ComponentEngine)
	default:
		return subject
	}
}

// ListAlertEvents returns a page of an alert's append-only history, most recent
// first (at DESC, id DESC — a stable total order even when several events share
// a timestamp). Backed by the alert_event_alert_idx (alert_id, at DESC) index.
//
// Pagination is mandatory, not cosmetic: a long-lived alert (a continuous-cadence
// rule, or an engine.error meta-alert) accumulates one row per failing evaluation
// indefinitely — notification suppression keeps those rows out of the bus but NOT
// out of the table — so an unbounded read could pull millions of rows into memory.
// Offset-paginated to match the other list endpoints; the (alert_id, at) index
// keeps early pages cheap. (Keyset on (at, id) would scale better for very deep
// pages — a future refinement, deferred for cross-endpoint consistency.)
func (s *Storage) ListAlertEvents(ctx context.Context, alertID uuid.UUID, q GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error) {
	exists, err := s.db.NewSelect().Model((*models.Alert)(nil)).Where("id = ?", alertID).Exists(ctx)
	if err != nil {
		return nil, e("check alert before listing events", err)
	}
	if !exists {
		return nil, e("list alert events", ErrNotFound)
	}
	return paginateWithOffset[PaginatedQueryOptions[AlertEventsFilters], models.AlertEvent](s, ctx,
		(*bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[AlertEventsFilters]])(&q),
		func(query *bun.SelectQuery) *bun.SelectQuery {
			return query.Where("alert_id = ?", alertID).Order("at DESC", "id DESC")
		},
	)
}

type AlertEventsFilters struct{}

type GetAlertEventsQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[AlertEventsFilters]]

func NewGetAlertEventsQuery(opts PaginatedQueryOptions[AlertEventsFilters]) GetAlertEventsQuery {
	return GetAlertEventsQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}

func (s *Storage) buildAlertListQuery(selectQuery *bun.SelectQuery, where string, args []any) *bun.SelectQuery {
	selectQuery = selectQuery.Order("last_seen_at DESC", "id DESC")
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
		case "periodID":
			if operator != "$match" {
				return "", nil, pkgErrors.Wrap(ErrInvalidQuery, "'periodID' can only be used with $match")
			}
			return "period_id = ?", []any{value}, nil
		case "firstSeenAt", "lastSeenAt":
			col := map[string]string{"firstSeenAt": "first_seen_at", "lastSeenAt": "last_seen_at"}[key]
			sqlOperator, ok := query.DefaultComparisonOperatorsMapping[operator]
			if !ok {
				return "", nil, pkgErrors.Wrapf(ErrInvalidQuery, "operator '%s' is not supported for '%s'", operator, key)
			}
			return fmt.Sprintf("%s %s ?", col, sqlOperator), []any{value}, nil
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
