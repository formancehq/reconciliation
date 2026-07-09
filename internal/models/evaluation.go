package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// EvaluationResult is the outcome of a single rule execution. PASS / FAIL drive
// the alert layer; ERROR is reserved for engine-side failures (CEL builtin
// threw, source resolver timed out, budget exceeded) and raises a separate
// engine.error meta-alert rather than a data alert.
type EvaluationResult string

const (
	EvaluationPass  EvaluationResult = "PASS"
	EvaluationFail  EvaluationResult = "FAIL"
	EvaluationError EvaluationResult = "ERROR"
)

// Evaluation is persisted on every rule execution — pass or fail — so the audit
// trail and incident evidence stay reproducible. Ledger sources are read live
// at the evaluation instant — a single aggregate is an internally consistent
// snapshot (ADR-003).
type Evaluation struct {
	bun.BaseModel `bun:"reconciliations.evaluation" json:"-"`

	ID        uuid.UUID        `bun:",pk,nullzero"                 json:"id"`
	RuleID    uuid.UUID        `bun:"rule_id,notnull"              json:"ruleID"`
	StartedAt time.Time        `bun:"started_at,notnull,nullzero"  json:"startedAt"`
	EndedAt   time.Time        `bun:"ended_at,notnull,nullzero"    json:"endedAt"`
	Result    EvaluationResult `bun:",notnull"                     json:"result"`
	Evidence  json.RawMessage  `bun:",type:jsonb"                  json:"evidence,omitempty"`
	Error     string           `bun:",nullzero"                    json:"error,omitempty"`
	CostUnits int64            `bun:"cost_units,notnull"           json:"costUnits"`
	CreatedAt time.Time        `bun:"created_at,notnull,nullzero"  json:"createdAt"`
}
