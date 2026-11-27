package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
)

type evaluationResponse struct {
	ID              string          `json:"id"`
	RuleID          string          `json:"ruleID"`
	StartedAt       time.Time       `json:"startedAt"`
	EndedAt         time.Time       `json:"endedAt"`
	Result          string          `json:"result"`
	Evidence        json.RawMessage `json:"evidence,omitempty"`
	Error           string          `json:"error,omitempty"`
	CostUnits       int64           `json:"costUnits"`
	CreatedAt       time.Time       `json:"createdAt"`
	ContractVersion int             `json:"contractVersion,omitempty"`
}

func renderEvaluation(ev *models.Evaluation) *evaluationResponse {
	response := &evaluationResponse{
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
	if ev.ContractVersion.Effective() == models.ContractVersionV2 {
		response.ContractVersion = int(models.ContractVersionV2)
	}
	return response
}

type rulePreviewResponse struct {
	Result    string          `json:"result"`
	Evidence  json.RawMessage `json:"evidence,omitempty"`
	Error     string          `json:"error,omitempty"`
	PIT       time.Time       `json:"pit"`
	StartedAt time.Time       `json:"startedAt"`
	EndedAt   time.Time       `json:"endedAt"`
}

func renderRulePreview(p *service.RulePreview) *rulePreviewResponse {
	return &rulePreviewResponse{
		Result:    string(p.Result),
		Evidence:  p.Evidence,
		Error:     p.Error,
		PIT:       p.PIT,
		StartedAt: p.StartedAt,
		EndedAt:   p.EndedAt,
	}
}

// previewRuleHandler serves POST /rules/preview: a dry run of a rule
// configuration on the current balances, before it is saved. It writes
// nothing, so it answers 200 with the verdict, not 201.
func previewRuleHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req service.PreviewRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		preview, err := b.GetService().PreviewRule(r.Context(), &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderRulePreview(preview))
	}
}
