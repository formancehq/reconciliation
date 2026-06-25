package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// AlertStatus is the lifecycle state of an Alert. An alert is the stable
// entity identified by (rule_id, fingerprint, period_id) and stays at the same
// id for its period's lifetime — re-opens within the period are status
// transitions in place, not new rows. The audit trail of every transition
// lives in AlertEvent.
type AlertStatus string

const (
	AlertOpen         AlertStatus = "OPEN"
	AlertAcknowledged AlertStatus = "ACKNOWLEDGED"
	AlertResolved     AlertStatus = "RESOLVED"
)

// ResolutionKind discriminates the three closure paths from spec §5.4 / §6.4.
type ResolutionKind string

const (
	// ResolutionAuto is set when the next evaluation passes against the same
	// fingerprint and the system closes the alert with no operator action.
	ResolutionAuto ResolutionKind = "auto"
	// ResolutionFixedByBooking is set when an operator marks an alert
	// resolved after posting corrective transactions. TransactionRefs is
	// optional but encouraged for audit replay.
	ResolutionFixedByBooking ResolutionKind = "fixed_by_booking"
	// ResolutionAcceptedByBusiness is set when an operator formally
	// acknowledges the discrepancy as acceptable. Note is required;
	// EvidenceSnapshot freezes the breaking evidence at decision time;
	// ExpiresAt optionally re-raises the alert if it's still failing
	// after the acceptance window.
	//
	// The literal matches the OpenAPI enum, the storage.AcceptAlert
	// docstring, and the user-facing docs — all use the same
	// "accepted_by_business" form to mirror the "fixed_by_booking"
	// naming pattern.
	ResolutionAcceptedByBusiness ResolutionKind = "accepted_by_business"
)

// Ack captures who acknowledged an alert and when. The CURRENT ack lives on
// the Alert row; historical acks (e.g. an alert acked, resolved, re-opened,
// acked again) are preserved as AlertEvent rows with type='ack'.
type Ack struct {
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
}

// Resolution is the audit-trailed closure of an alert. Stored as JSONB on
// the alert row (the *current* resolution). Historical resolutions across
// reopen cycles live as AlertEvent rows with type in {resolve, accept}.
// Some fields apply to only certain kinds:
//   - TransactionRefs:  fixed_by_booking only
//   - EvidenceSnapshot: accepted only (frozen at decision time)
//   - ExpiresAt:        accepted only, optional
type Resolution struct {
	Kind             ResolutionKind  `json:"kind"`
	By               string          `json:"by"`
	At               time.Time       `json:"at"`
	Note             string          `json:"note,omitempty"`
	TransactionRefs  []string        `json:"transactionRefs,omitempty"`
	EvidenceSnapshot json.RawMessage `json:"evidenceSnapshot,omitempty"`
	ExpiresAt        *time.Time      `json:"expiresAt,omitempty"`
}

// Alert is the stable, dedup'd record of a failing fingerprint within a
// reconciliation period. A UNIQUE constraint on (rule_id, fingerprint,
// period_id) guarantees one alert per triple — concurrent failing evaluations
// of the same period update the same row, and re-opens within the period
// transition status back to OPEN in place. The same fingerprint failing in a
// new period is a separate alert (see PeriodID / models.Cadence).
//
// OccurrenceCount is the count of FAIL events on this alert across reopen
// cycles within its period (for a continuous-cadence rule, that is the lifetime
// count). For finer per-episode counts, query AlertEvent.
type Alert struct {
	bun.BaseModel `bun:"reconciliations.alert" json:"-"`

	ID          uuid.UUID `bun:",pk,nullzero"                  json:"id"`
	RuleID      uuid.UUID `bun:"rule_id,notnull"               json:"ruleID"`
	Fingerprint string    `bun:",notnull"                      json:"fingerprint"`
	// PeriodID scopes the alert to a reconciliation period (e.g. "2026-03",
	// or "continuous" for a live-monitoring rule). The dedup identity is
	// (rule_id, fingerprint, period_id): a new period opens a fresh case
	// rather than reopening a prior period's. See models.Cadence.PeriodID.
	PeriodID         string            `bun:"period_id,notnull"             json:"periodID"`
	Status           AlertStatus       `bun:",notnull"                      json:"status"`
	Severity         Severity          `bun:",notnull"                      json:"severity"`
	FirstSeenAt      time.Time         `bun:"first_seen_at,notnull,nullzero" json:"firstSeenAt"`
	LastSeenAt       time.Time         `bun:"last_seen_at,notnull,nullzero"  json:"lastSeenAt"`
	OccurrenceCount  int64             `bun:"occurrence_count,notnull"       json:"occurrenceCount"`
	LastEvaluationID uuid.UUID         `bun:"last_evaluation_id,notnull"     json:"lastEvaluationID"`
	Evidence         json.RawMessage   `bun:",type:jsonb"                    json:"evidence,omitempty"`
	Ack              *Ack              `bun:",type:jsonb"                    json:"ack,omitempty"`
	Resolution       *Resolution       `bun:",type:jsonb"                    json:"resolution,omitempty"`
	Labels           map[string]string `bun:",type:jsonb"                    json:"labels,omitempty"`
	CreatedAt        time.Time         `bun:"created_at,notnull,nullzero"    json:"createdAt"`
	UpdatedAt        time.Time         `bun:"updated_at,notnull,nullzero"    json:"updatedAt"`
}

// AlertEventType discriminates the trigger of an event row. Reopen is not a
// distinct type — a 'fail' event with prev_status='RESOLVED' IS a reopen.
type AlertEventType string

const (
	// AlertEventFail is recorded for every failing evaluation against the alert.
	AlertEventFail AlertEventType = "fail"
	// AlertEventPass is recorded when an evaluation passes and the alert
	// auto-resolves. One pass event per resolved alert per evaluation.
	AlertEventPass AlertEventType = "pass"
	// AlertEventAck is a manual transition to ACKNOWLEDGED.
	AlertEventAck AlertEventType = "ack"
	// AlertEventResolve is a manual transition with kind=fixed_by_booking.
	AlertEventResolve AlertEventType = "resolve"
	// AlertEventAccept is a manual transition with kind=accepted_by_business.
	AlertEventAccept AlertEventType = "accept"
)

// AlertEvent is one row in the alert's append-only history. Together with the
// Alert row's current state, the event log is the complete picture of an
// alert's lifecycle — including every prior resolution across reopen cycles.
//
// Append-only by convention (code paths never UPDATE or DELETE). Future work
// may add a per-alert seq column + prev_hash/hash chain to make the log
// independently verifiable, similar to how the Ledger logs transactions.
type AlertEvent struct {
	bun.BaseModel `bun:"reconciliations.alert_event" json:"-"`

	ID           uuid.UUID       `bun:",pk,nullzero"          json:"id"`
	AlertID      uuid.UUID       `bun:"alert_id,notnull"       json:"alertID"`
	EvaluationID *uuid.UUID      `bun:"evaluation_id,nullzero" json:"evaluationID,omitempty"`
	Type         AlertEventType  `bun:",notnull"                json:"type"`
	PrevStatus   *AlertStatus    `bun:"prev_status,nullzero"    json:"prevStatus,omitempty"`
	NewStatus    AlertStatus     `bun:"new_status,notnull"      json:"newStatus"`
	Payload      json.RawMessage `bun:",type:jsonb"             json:"payload,omitempty"`
	// Notify is the notification decision for this row, computed once at write
	// time. true (the default) → the transition is published to the message
	// bus; false → it is recorded in the append-only log for audit but NOT
	// paged. Only repeated, materially-identical fails are suppressed (see
	// Storage.recordAlertEvent and docs/technical/notification-suppression.md);
	// opens, reopens, evidence changes, and every manual transition stay true.
	Notify    bool      `bun:"notify,notnull"              json:"notify"`
	At        time.Time `bun:",notnull,nullzero"          json:"at"`
	CreatedAt time.Time `bun:"created_at,notnull,nullzero" json:"createdAt"`
}

// IsReopen returns true when this fail event lands on a previously-resolved
// alert. The helper exists so consumers don't need to remember the convention
// (fail + prev=RESOLVED) and so the predicate has a single source of truth.
func (e *AlertEvent) IsReopen() bool {
	return e.Type == AlertEventFail && e.PrevStatus != nil && *e.PrevStatus == AlertResolved
}
