package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// CaptureInput is the immutable audit record of one rule evaluation, written to
// the control ledger as a capture transaction (ADR-003). It is the durable
// "what was reconciled and when" — positive assurance on a pass, break evidence
// on a fail — recorded independently of the alert lifecycle.
type CaptureInput struct {
	RuleID       uuid.UUID
	TemplateKind string
	PeriodID     string
	EvaluationID uuid.UUID
	CapturedAt   time.Time
	Verdict      string          // "pass" | "fail"
	Trigger      string          // "scheduled" | "manual"
	Evidence     json.RawMessage // the evaluation's outcome evidence (bounded)
}
