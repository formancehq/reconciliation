package store

import (
	"encoding/json"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
)

// CaptureInput is the immutable audit record of one rule evaluation, written to
// the control ledger as a capture transaction (ADR-003). It is the durable
// "what was reconciled and when" — all failing evidence plus successful
// evidence for automatic alert resolutions — recorded independently of the
// alert lifecycle.
type CaptureInput struct {
	RuleID          uuid.UUID
	ContractVersion models.ContractVersion
	TemplateKind    string
	PeriodID        string
	EvaluationID    uuid.UUID
	CapturedAt      time.Time
	Verdict         string          // "pass" | "fail"
	Trigger         string          // "scheduled" | "manual"
	Evidence        json.RawMessage // the evaluation's outcome evidence (bounded)
	RuleRevision    string
	PIT             time.Time
	StartedAt       time.Time
	Result          models.EvaluationResult
	Error           string
}
