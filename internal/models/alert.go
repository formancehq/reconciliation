package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// AlertStatus is the lifecycle state of an Alert. An alert is the stable
// entity identified by (rule_id, fingerprint) and stays at the same id for
// its entire life — re-opens are status transitions in place, not new rows.
// The audit trail of every transition lives in AlertEvent.
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

// Alert is the stable, dedup'd record of a failing fingerprint. A UNIQUE
// constraint on (rule_id, fingerprint) guarantees one alert per pair across
// the lifetime of the rule — concurrent failing evaluations update the
// same row, and re-opens transition status back to OPEN in place.
//
// OccurrenceCount is the LIFETIME count of FAIL events on this alert
// (i.e. summed across reopen cycles). For per-episode counts, query
// AlertEvent filtered to the current episode boundaries.
type Alert struct {
	bun.BaseModel `bun:"reconciliations.alert" json:"-"`

	ID               uuid.UUID         `bun:",pk,nullzero"                  json:"id"`
	RuleID           uuid.UUID         `bun:"rule_id,notnull"               json:"ruleID"`
	Fingerprint      string            `bun:",notnull"                      json:"fingerprint"`
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

	ID            uuid.UUID       `bun:",pk,nullzero"          json:"id"`
	AlertID       uuid.UUID       `bun:"alert_id,notnull"       json:"alertID"`
	EvaluationID  *uuid.UUID      `bun:"evaluation_id,nullzero" json:"evaluationID,omitempty"`
	Type          AlertEventType  `bun:",notnull"                json:"type"`
	PrevStatus    *AlertStatus    `bun:"prev_status,nullzero"    json:"prevStatus,omitempty"`
	NewStatus     AlertStatus     `bun:"new_status,notnull"      json:"newStatus"`
	Payload       json.RawMessage `bun:",type:jsonb"             json:"payload,omitempty"`
	At            time.Time       `bun:",notnull,nullzero"       json:"at"`
	CreatedAt     time.Time       `bun:"created_at,notnull,nullzero" json:"createdAt"`
}

// IsReopen returns true when this fail event lands on a previously-resolved
// alert. The helper exists so consumers don't need to remember the convention
// (fail + prev=RESOLVED) and so the predicate has a single source of truth.
func (e *AlertEvent) IsReopen() bool {
	return e.Type == AlertEventFail && e.PrevStatus != nil && *e.PrevStatus == AlertResolved
}
