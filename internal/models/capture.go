package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Capture is one immutable evaluation record read back from the control ledger
// (ADR-003). Every rule evaluation mints a capture transaction in the
// `capture:rule:{ruleID}:per:{period}` bucket carrying this self-describing
// snapshot. Its evidence contains every outcome and the exact values used by
// that evaluation. It is reconstructed from the capture
// transaction's metadata plus its ledger id.
//
// Unlike an AlertEvent (the alert transition timeline, sink-gated), a Capture is
// a first-class ledger transaction, so a rule's capture history is queryable live
// via ListTransactions on the bucket address.
type Capture struct {
	// TransactionID is the ledger-local id of the capture transaction — a stable
	// handle for the record within the control ledger.
	TransactionID   uint64           `json:"transactionID"`
	ContractVersion ContractVersion  `json:"-"`
	RuleID          uuid.UUID        `json:"ruleID"`
	PeriodID        string           `json:"periodID"`
	EvaluationID    uuid.UUID        `json:"evaluationID"`
	TemplateKind    string           `json:"templateKind"`
	Verdict         string           `json:"verdict"` // "pass" | "fail"
	Trigger         string           `json:"trigger"` // "scheduled" | "manual"
	CapturedAt      time.Time        `json:"capturedAt"`
	Evidence        json.RawMessage  `json:"evidence,omitempty"`
	RuleRevision    string           `json:"ruleRevision,omitempty"`
	PIT             time.Time        `json:"pit,omitempty"`
	StartedAt       time.Time        `json:"startedAt,omitempty"`
	Result          EvaluationResult `json:"result,omitempty"`
	Error           string           `json:"error,omitempty"`
}
