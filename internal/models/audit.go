package models

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// AuditEntryKind discriminates the operation an audit entry records. Every
// state-bearing operation of the module produces exactly one entry, and the
// entry is written inside the same transaction as the operation it records —
// so a rolled-back operation leaves no entry and a committed one cannot be
// missing from the chain.
type AuditEntryKind string

const (
	// AuditRuleCreated records a new control definition. The memento carries
	// revision 1 of the rule spec.
	AuditRuleCreated AuditEntryKind = "rule.created"
	// AuditRuleRevised records a change to a control definition. The memento
	// carries the new revision's full spec, so the chain answers "what was
	// being checked at the time" without consulting the mutable rule row.
	AuditRuleRevised AuditEntryKind = "rule.revised"
	// AuditRuleDeleted records the tombstoning of a control. The rule row and
	// every evaluation, alert and event it produced are retained — deletion is
	// an event in the chain, not an erasure.
	AuditRuleDeleted AuditEntryKind = "rule.deleted"
	// AuditEvaluationCommitted records one rule execution, PASS, FAIL or
	// ERROR alike. The memento carries the evidence digest and the resolved
	// point-in-time per source.
	AuditEvaluationCommitted AuditEntryKind = "evaluation.committed"
	// AuditAlertTransition records one alert lifecycle transition — including
	// the ones whose notification is suppressed, which are exactly the ones an
	// auditor would otherwise never see.
	AuditAlertTransition AuditEntryKind = "alert.transition"
	// AuditPeriodSealed records the closure of a reconciliation period. The
	// memento carries the sealing hash, so the seal is itself chained.
	AuditPeriodSealed AuditEntryKind = "period.sealed"
)

// SubjectSource discriminates the origin of an action. The distinction is
// hash-bound, not informational: a scheduled evaluation and a human resolution
// hash differently even when every other field matches, so an auditor can tell
// automatic controls from manual intervention without trusting a text field.
type SubjectSource string

const (
	// SubjectSourceUnknown is an authenticated caller whose token carried no
	// usable issuer. Encoded as tag 0x00.
	SubjectSourceUnknown SubjectSource = ""
	// SubjectSourceIssuer is a caller identified by its OIDC token issuer.
	// Encoded as tag 0x01.
	SubjectSourceIssuer SubjectSource = "issuer"
	// SubjectSourceClientID is a caller identified by an OAuth client id when
	// no issuer was present. Encoded as tag 0x02.
	SubjectSourceClientID SubjectSource = "client_id"
	// SubjectSourceSystem is an internal component acting without a caller —
	// the scheduler committing an evaluation, the snooze-expiry sweep. Subject
	// is empty and SourceValue names the component. Encoded as tag 0x03.
	SubjectSourceSystem SubjectSource = "system_component"
)

// Subject is the attribution snapshot bound into an audit entry's hash. It is
// derived from the verified bearer token (or set to a system component), never
// supplied by the client — which is what separates it from the pre-chain `by`
// fields on Ack and Resolution.
type Subject struct {
	// Subject is the OIDC `sub` claim, or empty for a system component.
	Subject string `json:"subject,omitempty"`
	// Source names which identifier SourceValue holds.
	Source SubjectSource `json:"source,omitempty"`
	// SourceValue is the issuer URL, the client id, or the component name.
	SourceValue string `json:"sourceValue,omitempty"`
	// Scopes are the token's granted scopes, stored sorted so the encoding is
	// independent of the order the authorization server happened to emit.
	Scopes []string `json:"scopes,omitempty"`
}

// SystemSubject builds the attribution for an internal component.
func SystemSubject(component string) Subject {
	return Subject{Source: SubjectSourceSystem, SourceValue: component}
}

// IsSystem reports whether this action was taken by the module itself rather
// than by an authenticated caller.
func (s Subject) IsSystem() bool { return s.Source == SubjectSourceSystem }

// Display renders the subject for human consumption — the API's read models
// expose both this and the structured form.
func (s Subject) Display() string {
	if s.Source == SubjectSourceSystem {
		return "system:" + s.SourceValue
	}
	if s.Subject != "" {
		return s.Subject
	}
	return "unknown"
}

// AuditEntry is one link in the module's append-only, hash-chained journal.
//
// Two counters, deliberately: Seq is the physical insertion order and may have
// gaps (a rolled-back transaction consumes a bigserial value), while Sequence
// is the dense logical counter assigned under the chain advisory lock. Gap
// detection rides on Sequence — a bigserial cannot carry it. This mirrors the
// (seq, id) split the Ledger V2 logs table uses.
//
// The row is immutable by grant, not by convention: the application role has no
// UPDATE or DELETE privilege on the table and a trigger raises on either.
type AuditEntry struct {
	bun.BaseModel `bun:"reconciliations.audit_entry" json:"-"`

	Seq      int64          `bun:"seq,pk,autoincrement"          json:"-"`
	Sequence int64          `bun:"sequence,notnull"              json:"sequence"`
	At       time.Time      `bun:"at,notnull,nullzero"           json:"at"`
	Kind     AuditEntryKind `bun:"kind,notnull"                  json:"kind"`

	RuleID       *uuid.UUID `bun:"rule_id,nullzero"       json:"ruleID,omitempty"`
	RuleRevision *int64     `bun:"rule_revision,nullzero" json:"ruleRevision,omitempty"`
	AlertID      *uuid.UUID `bun:"alert_id,nullzero"      json:"alertID,omitempty"`
	EvaluationID *uuid.UUID `bun:"evaluation_id,nullzero" json:"evaluationID,omitempty"`
	PeriodID     string     `bun:"period_id,nullzero"     json:"periodID,omitempty"`

	Subject Subject `bun:"subject,type:jsonb,notnull" json:"subject"`

	// Memento is the canonical, frozen byte form of the operation's payload —
	// what actually enters the hash. It is stored separately from any indexable
	// jsonb projection precisely because jsonb does not preserve key order or
	// number literals, so a hash taken over a jsonb round-trip would not
	// re-verify. The Ledger V2 logs table uses the same split.
	Memento []byte `bun:"memento,notnull" json:"-"`
	// MementoDigest lets a reader detect an altered memento without recomputing
	// the whole chain.
	MementoDigest []byte `bun:"memento_digest,notnull" json:"mementoDigest"`

	PrevHash    []byte `bun:"prev_hash"                json:"prevHash,omitempty"`
	Hash        []byte `bun:"hash,notnull"             json:"hash"`
	HashVersion int    `bun:"hash_version,notnull"     json:"hashVersion"`

	CreatedAt time.Time `bun:"created_at,notnull,nullzero" json:"createdAt"`
}

// RuleRevision is an immutable snapshot of a control definition at one
// revision. Without it the chain proves the verdict of a control but not what
// the control was, because the rule row only ever holds the latest spec.
type RuleRevision struct {
	bun.BaseModel `bun:"reconciliations.rule_revision" json:"-"`

	RuleID         uuid.UUID         `bun:"rule_id,pk"                    json:"ruleID"`
	Revision       int64             `bun:"revision,pk"                   json:"revision"`
	Name           string            `bun:"name,notnull"                  json:"name"`
	TemplateKind   TemplateKind      `bun:"template_kind,notnull"         json:"templateKind"`
	TemplateSpec   json.RawMessage   `bun:"template_spec,type:jsonb"      json:"templateSpec"`
	ExplanationCEL string            `bun:"explanation_cel,nullzero"      json:"explanationCEL,omitempty"`
	Severity       Severity          `bun:"severity,notnull"              json:"severity"`
	Cadence        Cadence           `bun:"cadence,notnull"               json:"cadence"`
	Enabled        bool              `bun:"enabled,notnull"               json:"enabled"`
	Schedule       *Schedule         `bun:"schedule,type:jsonb"           json:"schedule,omitempty"`
	Notifications  []string          `bun:"notifications,type:jsonb"      json:"notifications,omitempty"`
	Labels         map[string]string `bun:"labels,type:jsonb"           json:"labels,omitempty"`
	// AuditSequence links the revision to the chain entry that recorded it.
	AuditSequence int64     `bun:"audit_sequence,notnull" json:"auditSequence"`
	CreatedAt     time.Time `bun:"created_at,notnull,nullzero" json:"createdAt"`
}

// PeriodSealStatus is the lifecycle of a reconciliation period.
type PeriodSealStatus string

const (
	// PeriodOpen is the implicit state of any period that has never been
	// sealed. It is not stored — the absence of a row means open.
	PeriodOpen PeriodSealStatus = "OPEN"
	// PeriodSealed means the period's audit range is closed and its alerts are
	// no longer writable.
	PeriodSealed PeriodSealStatus = "SEALED"
)

// PeriodSeal closes a contiguous range of the chain under a period label.
//
// The range, not the label, is what the seal covers — exactly as a Ledger V3
// chapter closes at an audit-sequence boundary rather than filtering on
// content. FirstSequence continues from the previous seal, so the seals form a
// partition of the chain with no gap and no overlap.
type PeriodSeal struct {
	bun.BaseModel `bun:"reconciliations.period_seal" json:"-"`

	PeriodID      string `bun:"period_id,pk"            json:"periodID"`
	FirstSequence int64  `bun:"first_sequence,notnull"  json:"firstSequence"`
	LastSequence  int64  `bun:"last_sequence,notnull"   json:"lastSequence"`
	// EntryCount is the number of chain entries the seal covers. Zero is legal
	// (a quiet period) and is not the same as an unsealed period.
	EntryCount int64 `bun:"entry_count,notnull" json:"entryCount"`
	// LastAuditHash is the chain head at seal time. Chain verification resumes
	// across a sealed boundary from this value, the same way the Ledger does
	// across an archived chapter.
	LastAuditHash []byte `bun:"last_audit_hash" json:"lastAuditHash,omitempty"`
	// StateHash covers the derived alert state of the period, so the seal
	// commits to the outcome as well as to the journal that produced it.
	StateHash []byte `bun:"state_hash,notnull" json:"stateHash"`
	// SealingHash is what an auditor checks: one value standing for the whole
	// period.
	SealingHash []byte `bun:"sealing_hash,notnull" json:"sealingHash"`
	// Signature is the Ed25519 signature of SealingHash. It is what makes the
	// seal verifiable by a third party who holds only the public key — the
	// chain hashes themselves are keyed and cannot be checked from outside.
	Signature []byte `bun:"signature" json:"signature,omitempty"`
	// SigningKeyID identifies which public key verifies Signature.
	SigningKeyID string  `bun:"signing_key_id,nullzero" json:"signingKeyID,omitempty"`
	SealedBy     Subject `bun:"sealed_by,type:jsonb,notnull" json:"sealedBy"`
	// AlertCount and UnresolvedCount are the period's headline figures, frozen
	// at seal time so a report does not have to re-derive them later.
	AlertCount      int64     `bun:"alert_count,notnull"      json:"alertCount"`
	UnresolvedCount int64     `bun:"unresolved_count,notnull" json:"unresolvedCount"`
	SealedAt        time.Time `bun:"sealed_at,notnull,nullzero" json:"sealedAt"`
	// AuditSequence is the chain entry that recorded this seal.
	AuditSequence int64 `bun:"audit_sequence,notnull" json:"auditSequence"`
}

// Status reports the period's lifecycle state. A stored PeriodSeal is always
// sealed; the method exists so API read models can render both cases through
// one type.
func (p *PeriodSeal) Status() PeriodSealStatus {
	if p == nil {
		return PeriodOpen
	}
	return PeriodSealed
}

// periodIDFormats are the exact shapes Cadence.PeriodID produces. Anything else
// is a typo, and a typo here is not recoverable: sealing advances a global
// boundary and the seal itself is immutable, so "2026-5" would consume the range
// that belonged to 2026-05 and leave those entries attested under a label nobody
// will ever look up. Neither seal can be corrected afterwards.
var periodIDFormats = []*regexp.Regexp{
	regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`), // daily   — 2026-05-15
	regexp.MustCompile(`^\d{4}-W\d{2}$`),      // weekly  — 2026-W12
	regexp.MustCompile(`^\d{4}-\d{2}$`),       // monthly — 2026-05
}

// ValidPeriodID reports whether id is a period a supported cadence can actually
// produce. ContinuousPeriod is excluded deliberately: it is a real period id but
// not a sealable one, and the caller rejects it with a more specific error.
func ValidPeriodID(id string) bool {
	for _, format := range periodIDFormats {
		if format.MatchString(id) {
			return true
		}
	}
	return false
}

// ChainViolationType names the ways a chain walk can fail. The set is closed:
// any tampering with the journal surfaces as one of these three.
type ChainViolationType string

const (
	// ChainViolationHashMismatch means a recomputed hash differs from the
	// stored one — a hashed field was altered after the fact.
	ChainViolationHashMismatch ChainViolationType = "HASH_MISMATCH"
	// ChainViolationSequenceGap means the dense logical sequence skips a value
	// — an entry was removed.
	ChainViolationSequenceGap ChainViolationType = "SEQUENCE_GAP"
	// ChainViolationMementoDigest means the stored memento no longer matches
	// its digest — the payload was edited without recomputing the chain.
	ChainViolationMementoDigest ChainViolationType = "MEMENTO_DIGEST_MISMATCH"
	// ChainViolationBrokenLink means an entry's prev_hash does not match the
	// preceding entry's hash — entries were reordered or spliced.
	ChainViolationBrokenLink ChainViolationType = "BROKEN_LINK"
)

// ChainVerification is the outcome of walking a range of the chain. The walk
// stops at the first violation: past a break, every downstream comparison is
// meaningless.
type ChainVerification struct {
	OK            bool               `json:"ok"`
	FirstSequence int64              `json:"firstSequence"`
	LastSequence  int64              `json:"lastSequence"`
	EntriesWalked int64              `json:"entriesWalked"`
	Violation     ChainViolationType `json:"violation,omitempty"`
	AtSequence    *int64             `json:"atSequence,omitempty"`
	Detail        string             `json:"detail,omitempty"`
	// SealsCrossed lists the period seals the walk traversed, each of whose
	// sealing hash was re-derived and checked against the stored value.
	SealsCrossed []string `json:"sealsCrossed,omitempty"`
}
