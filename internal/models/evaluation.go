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
// trail and incident evidence stay auditable. PitPerSource records the resolved
// point-in-time each historical Source actually used. For a Payments `latest`
// read it records the observation time because the upstream response exposes no
// snapshot timestamp; evidence is frozen, but exact historical replay of that
// latest snapshot is not guaranteed (see ADR-002).
type Evaluation struct {
	bun.BaseModel `bun:"reconciliations.evaluation" json:"-"`

	ID           uuid.UUID            `bun:",pk,nullzero"                 json:"id"`
	RuleID       uuid.UUID            `bun:"rule_id,notnull"              json:"ruleID"`
	StartedAt    time.Time            `bun:"started_at,notnull,nullzero"  json:"startedAt"`
	EndedAt      time.Time            `bun:"ended_at,notnull,nullzero"    json:"endedAt"`
	PitPerSource map[string]time.Time `bun:"pit_per_source,type:jsonb,notnull" json:"pitPerSource"`
	Result       EvaluationResult     `bun:",notnull"                     json:"result"`
	Evidence     json.RawMessage      `bun:",type:jsonb"                  json:"evidence,omitempty"`
	Error        string               `bun:",nullzero"                    json:"error,omitempty"`
	CostUnits    int64                `bun:"cost_units,notnull"           json:"costUnits"`
	// ScheduledAt and RuleRevision are internal occurrence identity fields.
	// They remain outside the public response while the partial unique index on
	// them fences duplicate committed effects after a job row is cleaned up.
	ScheduledAt  *time.Time `bun:"scheduled_at,nullzero"         json:"-"`
	RuleRevision *int64     `bun:"rule_revision,nullzero"        json:"-"`
	CreatedAt    time.Time  `bun:"created_at,notnull,nullzero"  json:"createdAt"`
}
