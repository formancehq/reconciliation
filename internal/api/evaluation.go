package api

import (
	"encoding/json"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
)

type evaluationResponse struct {
	ID        string          `json:"id"`
	RuleID    string          `json:"ruleID"`
	StartedAt time.Time       `json:"startedAt"`
	EndedAt   time.Time       `json:"endedAt"`
	Result    string          `json:"result"`
	Evidence  json.RawMessage `json:"evidence,omitempty"`
	Error     string          `json:"error,omitempty"`
	CostUnits int64           `json:"costUnits"`
	CreatedAt time.Time       `json:"createdAt"`
}

func renderEvaluation(ev *models.Evaluation) *evaluationResponse {
	return &evaluationResponse{
		ID:        ev.ID.String(),
		RuleID:    ev.RuleID.String(),
		StartedAt: ev.StartedAt,
		EndedAt:   ev.EndedAt,
		Result:    string(ev.Result),
		Evidence:  ev.Evidence,
		Error:     ev.Error,
		CostUnits: ev.CostUnits,
		CreatedAt: ev.CreatedAt,
	}
}
