package store

import (
	"encoding/json"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
)

// OpenAlertInput is the payload OpenOrUpdateAlert consumes. Same shape on
// first open, re-open, and on-going failures — the storage layer figures out
// what kind of transition is happening based on the current row state.
type OpenAlertInput struct {
	RuleID          uuid.UUID
	ContractVersion models.ContractVersion
	Fingerprint     string
	// PeriodID scopes the alert to a reconciliation period. Empty defaults to
	// models.ContinuousPeriod, which reproduces the original
	// (rule_id, fingerprint) dedup. The caller (the evaluation service)
	// derives it from the rule's period type and the evaluation PIT.
	PeriodID     string
	Severity     models.Severity
	EvaluationID uuid.UUID
	Evidence     json.RawMessage // jsonb
	Labels       map[string]string
	OccurredAt   time.Time
}

// OpenAlertResult tells the caller what kind of transition happened so it can
// fire the matching downstream signal (toast in the UI, webhook payload).
type OpenAlertResult struct {
	Alert    *models.Alert
	Event    *models.AlertEvent
	Created  bool // true only on the first fail for this (rule, fingerprint, period)
	Reopened bool // true on a within-period transition from RESOLVED → OPEN
}
