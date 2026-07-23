package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type EvaluationJobStatus string

const (
	EvaluationJobPending   EvaluationJobStatus = "PENDING"
	EvaluationJobRunning   EvaluationJobStatus = "RUNNING"
	EvaluationJobSucceeded EvaluationJobStatus = "SUCCEEDED"
	EvaluationJobFailed    EvaluationJobStatus = "FAILED"
	EvaluationJobCancelled EvaluationJobStatus = "CANCELLED"
)

// EvaluationJob is the durable representation of one cron occurrence.
// ClaimToken is a fencing token: every heartbeat and terminal transition must
// match it, so a worker whose lease was reclaimed cannot commit stale work.
type EvaluationJob struct {
	bun.BaseModel `bun:"reconciliations.evaluation_job"`

	ID           uuid.UUID           `bun:",pk,nullzero"`
	RuleID       uuid.UUID           `bun:"rule_id,notnull"`
	RuleRevision int64               `bun:"rule_revision,notnull"`
	ScheduledAt  time.Time           `bun:"scheduled_at,notnull"`
	Status       EvaluationJobStatus `bun:",notnull"`
	Attempts     int                 `bun:",notnull"`
	AvailableAt  time.Time           `bun:"available_at,notnull"`
	LeaseUntil   *time.Time          `bun:"lease_until,nullzero"`
	ClaimToken   *uuid.UUID          `bun:"claim_token,nullzero"`
	LastError    string              `bun:"last_error,nullzero"`
	CreatedAt    time.Time           `bun:"created_at,notnull,nullzero"`
	UpdatedAt    time.Time           `bun:"updated_at,notnull,nullzero"`
}
