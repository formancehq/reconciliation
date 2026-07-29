package audit

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/formancehq/reconciliation/internal/models"
)

// A memento is the frozen, canonical payload of one recorded operation — the
// "what happened" that the chain binds. Mementos are JSON so a client can read
// one back and re-verify it, and canonical so that reading it back yields the
// exact bytes that were hashed.
//
// Large evidence is bound by digest rather than by value. The evidence itself
// already lives on the evaluation row; copying it into the memento would double
// the storage for no extra guarantee, since altering the row would change its
// canonical digest and break the chain just the same.

// RuleMemento is the full control definition at one revision. The chain
// therefore answers "what was being checked" without consulting the mutable
// rule row — the gap that makes an evidence trail unusable in an audit, because
// a verdict whose control definition has since been edited proves nothing.
type RuleMemento struct {
	RuleID         uuid.UUID           `json:"ruleID"`
	Revision       int64               `json:"revision"`
	Name           string              `json:"name"`
	TemplateKind   models.TemplateKind `json:"templateKind"`
	TemplateSpec   json.RawMessage     `json:"templateSpec,omitempty"`
	ExplanationCEL string              `json:"explanationCEL,omitempty"`
	Severity       models.Severity     `json:"severity"`
	Cadence        models.Cadence      `json:"cadence"`
	Enabled        bool                `json:"enabled"`
	Schedule       *models.Schedule    `json:"schedule,omitempty"`
	Notifications  []string            `json:"notifications,omitempty"`
	Labels         map[string]string   `json:"labels,omitempty"`
}

// NewRuleMemento freezes a rule definition.
func NewRuleMemento(r *models.Rule) (RuleMemento, error) {
	spec, err := canonicalOrNil(r.TemplateSpec)
	if err != nil {
		return RuleMemento{}, fmt.Errorf("audit: rule %s template spec: %w", r.ID, err)
	}
	return RuleMemento{
		RuleID:         r.ID,
		Revision:       r.Revision,
		Name:           r.Name,
		TemplateKind:   r.TemplateKind,
		TemplateSpec:   spec,
		ExplanationCEL: r.ExplanationCEL,
		Severity:       r.Severity,
		Cadence:        r.Cadence,
		Enabled:        r.Enabled,
		Schedule:       r.Schedule,
		Notifications:  r.Notifications,
		Labels:         r.Labels,
	}, nil
}

// RuleDeletedMemento records a tombstone. The definition is not repeated — the
// creation and revision entries already carry it, and the chain is read in
// order.
type RuleDeletedMemento struct {
	RuleID   uuid.UUID `json:"ruleID"`
	Revision int64     `json:"revision"`
	Name     string    `json:"name"`
}

// EvaluationMemento records one rule execution.
type EvaluationMemento struct {
	EvaluationID uuid.UUID               `json:"evaluationID"`
	RuleID       uuid.UUID               `json:"ruleID"`
	RuleRevision *int64                  `json:"ruleRevision,omitempty"`
	Result       models.EvaluationResult `json:"result"`
	StartedAt    time.Time               `json:"startedAt"`
	EndedAt      time.Time               `json:"endedAt"`
	// PitPerSource is the resolved point-in-time each source actually read. It
	// is what makes the evidence reproducible, so it is bound by value.
	PitPerSource map[string]time.Time `json:"pitPerSource,omitempty"`
	// EvidenceDigest binds the evaluation's evidence without copying it.
	EvidenceDigest string `json:"evidenceDigest,omitempty"`
	CostUnits      int64  `json:"costUnits"`
	Error          string `json:"error,omitempty"`
}

// NewEvaluationMemento freezes an evaluation. The evidence digest is taken over
// the canonical form of the evidence, so a re-read from jsonb — which does not
// preserve key order — re-canonicalizes to the same bytes and still matches.
func NewEvaluationMemento(ev *models.Evaluation) (EvaluationMemento, error) {
	m := EvaluationMemento{
		EvaluationID: ev.ID,
		RuleID:       ev.RuleID,
		RuleRevision: ev.RuleRevision,
		Result:       ev.Result,
		StartedAt:    ev.StartedAt.UTC(),
		EndedAt:      ev.EndedAt.UTC(),
		PitPerSource: normalizePITs(ev.PitPerSource),
		CostUnits:    ev.CostUnits,
		Error:        ev.Error,
	}
	digest, err := DigestJSON(ev.Evidence)
	if err != nil {
		return EvaluationMemento{}, fmt.Errorf("audit: evaluation %s evidence: %w", ev.ID, err)
	}
	m.EvidenceDigest = digest
	return m, nil
}

// AlertTransitionMemento records one alert lifecycle transition, including the
// ones whose notification is suppressed — which are exactly the transitions an
// auditor would otherwise have no way to see.
type AlertTransitionMemento struct {
	AlertEventID uuid.UUID             `json:"alertEventID"`
	AlertID      uuid.UUID             `json:"alertID"`
	RuleID       uuid.UUID             `json:"ruleID"`
	Type         models.AlertEventType `json:"type"`
	PrevStatus   *models.AlertStatus   `json:"prevStatus,omitempty"`
	NewStatus    models.AlertStatus    `json:"newStatus"`
	Fingerprint  string                `json:"fingerprint"`
	PeriodID     string                `json:"periodID"`
	Severity     models.Severity       `json:"severity"`
	// OccurrenceCount at the time of the transition, so a reader can see the
	// alert's progression without joining back to a mutable row.
	OccurrenceCount int64      `json:"occurrenceCount"`
	EvaluationID    *uuid.UUID `json:"evaluationID,omitempty"`
	// Notify is bound so that a suppression decision cannot be rewritten after
	// the fact — "we were never notified" becomes a checkable claim.
	Notify bool `json:"notify"`
	// PayloadDigest binds the transition's payload (the ack note, the resolution
	// with its transaction references, the frozen evidence snapshot on a
	// business acceptance).
	PayloadDigest string    `json:"payloadDigest,omitempty"`
	At            time.Time `json:"at"`
}

// NewAlertTransitionMemento freezes a transition.
func NewAlertTransitionMemento(alert *models.Alert, event *models.AlertEvent) (AlertTransitionMemento, error) {
	m := AlertTransitionMemento{
		AlertEventID:    event.ID,
		AlertID:         event.AlertID,
		RuleID:          alert.RuleID,
		Type:            event.Type,
		PrevStatus:      event.PrevStatus,
		NewStatus:       event.NewStatus,
		Fingerprint:     alert.Fingerprint,
		PeriodID:        alert.PeriodID,
		Severity:        alert.Severity,
		OccurrenceCount: alert.OccurrenceCount,
		EvaluationID:    event.EvaluationID,
		Notify:          event.Notify,
		At:              event.At.UTC(),
	}
	digest, err := DigestJSON(event.Payload)
	if err != nil {
		return AlertTransitionMemento{}, fmt.Errorf("audit: alert event %s payload: %w", event.ID, err)
	}
	m.PayloadDigest = digest
	return m, nil
}

// PeriodSealMemento records a period closure inside the chain, so the seal is
// itself an audited act rather than a side table anyone could add rows to.
type PeriodSealMemento struct {
	PeriodID      string `json:"periodID"`
	FirstSequence int64  `json:"firstSequence"`
	LastSequence  int64  `json:"lastSequence"`
	EntryCount    int64  `json:"entryCount"`
	AlertCount    int64  `json:"alertCount"`
	// UnresolvedCount is the headline an auditor asks for first: how many
	// discrepancies were still open when the period closed.
	UnresolvedCount int64  `json:"unresolvedCount"`
	StateHash       string `json:"stateHash"`
	SealingHash     string `json:"sealingHash"`
	SigningKeyID    string `json:"signingKeyID,omitempty"`
}

// DigestJSON returns the hex digest of a JSON payload's canonical form, or an
// empty string when the payload is absent. Used to bind large payloads by
// reference.
func DigestJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	canonical, err := Canonicalize(raw)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(MementoDigest(canonical)), nil
}

// BuildMemento canonicalizes a memento payload into the bytes that get hashed
// and stored.
func BuildMemento(payload any) ([]byte, error) {
	return CanonicalizeValue(payload)
}

func canonicalOrNil(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	canonical, err := Canonicalize(raw)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(canonical), nil
}

// normalizePITs forces UTC so the memento does not depend on the offset the
// driver happened to return.
func normalizePITs(in map[string]time.Time) map[string]time.Time {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]time.Time, len(in))
	for k, v := range in {
		out[k] = v.UTC()
	}
	return out
}
