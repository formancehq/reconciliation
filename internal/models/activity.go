package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// RuleActivity is one immutable, ordered fact in a rule's combined history.
type RuleActivity struct {
	ID              string          `json:"id"`
	Sequence        string          `json:"sequence"`
	Kind            string          `json:"kind"`
	Category        string          `json:"category"`
	RuleID          uuid.UUID       `json:"ruleID"`
	ContractVersion ContractVersion `json:"contractVersion"`
	RuleRevision    string          `json:"ruleRevision,omitempty"`
	CorrelationID   string          `json:"correlationID,omitempty"`
	OccurredAt      time.Time       `json:"occurredAt"`
	RecordedAt      time.Time       `json:"recordedAt"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}
