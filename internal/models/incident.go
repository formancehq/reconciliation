package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// IncidentStatus is the lifecycle state. Re-opens after RESOLVED create a new
// incident row with parent_incident_id pointing at the prior one — they do not
// flip status back to OPEN. That keeps MTTR metrics clean and makes flapping
// visible in the incident list.
type IncidentStatus string

const (
	IncidentOpen         IncidentStatus = "OPEN"
	IncidentAcknowledged IncidentStatus = "ACKNOWLEDGED"
	IncidentResolved     IncidentStatus = "RESOLVED"
)

// ResolutionKind discriminates the three closure paths from spec §5.4 / §6.4.
type ResolutionKind string

const (
	// ResolutionAuto is set when the next evaluation passes against the same
	// fingerprint and the system closes the incident with no operator action.
	ResolutionAuto ResolutionKind = "auto"
	// ResolutionFixedByBooking is set when an operator marks an incident
	// resolved after posting corrective transactions. TransactionRefs is
	// optional but encouraged for audit replay.
	ResolutionFixedByBooking ResolutionKind = "fixed_by_booking"
	// ResolutionAcceptedByBusiness is set when an operator formally
	// acknowledges the discrepancy as acceptable. Note is required;
	// EvidenceSnapshot freezes the breaking evidence at decision time;
	// ExpiresAt optionally re-raises the incident if it's still failing
	// after the acceptance window.
	//
	// The literal matches the OpenAPI enum, the storage.AcceptIncident
	// docstring, and the user-facing docs — all use the same
	// "accepted_by_business" form to mirror the "fixed_by_booking"
	// naming pattern.
	ResolutionAcceptedByBusiness ResolutionKind = "accepted_by_business"
)

// Ack captures who acknowledged an incident and when. One acknowledgement per
// incident; no history. (For audit history of failing states, query the
// Evaluation rows linked to this incident.)
type Ack struct {
	By   string    `json:"by"`
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
}

// Resolution is the audit-trailed closure of an incident. Stored as JSONB on
// the incident row; immutable once set (the application enforces this — the
// schema does not). Some fields apply to only certain kinds:
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

// Incident is the stateful, dedup'd record of a currently-failing fingerprint.
// A partial unique index in the schema guarantees at most one active (OPEN or
// ACKNOWLEDGED) incident per (rule_id, fingerprint), so concurrent failing
// evaluations always update the same row rather than racing into duplicates.
type Incident struct {
	bun.BaseModel `bun:"reconciliations.incident" json:"-"`

	ID                uuid.UUID         `bun:",pk,nullzero"                          json:"id"`
	RuleID            uuid.UUID         `bun:"rule_id,notnull"                       json:"ruleID"`
	Fingerprint       string            `bun:",notnull"                              json:"fingerprint"`
	Status            IncidentStatus    `bun:",notnull"                              json:"status"`
	Severity          Severity          `bun:",notnull"                              json:"severity"`
	OpenedAt          time.Time         `bun:"opened_at,notnull,nullzero"            json:"openedAt"`
	LastSeenAt        time.Time         `bun:"last_seen_at,notnull,nullzero"         json:"lastSeenAt"`
	OccurrenceCount   int               `bun:"occurrence_count,notnull"              json:"occurrenceCount"`
	FirstEvaluationID uuid.UUID         `bun:"first_evaluation_id,notnull"           json:"firstEvaluationID"`
	LastEvaluationID  uuid.UUID         `bun:"last_evaluation_id,notnull"            json:"lastEvaluationID"`
	Evidence          json.RawMessage   `bun:",type:jsonb"                           json:"evidence,omitempty"`
	Ack               *Ack              `bun:",type:jsonb"                           json:"ack,omitempty"`
	Resolution        *Resolution       `bun:",type:jsonb"                           json:"resolution,omitempty"`
	ParentIncidentID  *uuid.UUID        `bun:"parent_incident_id,nullzero"           json:"parentIncidentID,omitempty"`
	Labels            map[string]string `bun:",type:jsonb"                           json:"labels,omitempty"`
	CreatedAt         time.Time         `bun:"created_at,notnull,nullzero"           json:"createdAt"`
	UpdatedAt         time.Time         `bun:"updated_at,notnull,nullzero"           json:"updatedAt"`
}
