package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type captureResponse struct {
	TransactionID   uint64          `json:"transactionID"`
	RuleID          string          `json:"ruleID"`
	PeriodID        string          `json:"periodID"`
	EvaluationID    string          `json:"evaluationID"`
	TemplateKind    string          `json:"templateKind"`
	Verdict         string          `json:"verdict"`
	Trigger         string          `json:"trigger"`
	CapturedAt      time.Time       `json:"capturedAt"`
	Evidence        json.RawMessage `json:"evidence,omitempty"`
	ContractVersion int             `json:"contractVersion,omitempty"`
	RuleRevision    string          `json:"ruleRevision,omitempty"`
	PIT             *time.Time      `json:"pit,omitempty"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	Result          string          `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
}

func renderCapture(c *models.Capture) *captureResponse {
	response := &captureResponse{
		TransactionID: c.TransactionID,
		RuleID:        c.RuleID.String(),
		PeriodID:      c.PeriodID,
		EvaluationID:  c.EvaluationID.String(),
		TemplateKind:  c.TemplateKind,
		Verdict:       c.Verdict,
		Trigger:       c.Trigger,
		CapturedAt:    c.CapturedAt,
		Evidence:      c.Evidence,
	}
	if c.ContractVersion.Effective() == models.ContractVersionV2 {
		response.ContractVersion = int(models.ContractVersionV2)
		response.RuleRevision = c.RuleRevision
		response.PIT = &c.PIT
		response.StartedAt = &c.StartedAt
		response.Result = string(c.Result)
		response.Error = c.Error
	}
	return response
}

// listRuleCapturesHandler returns a rule's evaluation history — the immutable
// captures recorded per evaluation (ADR-003), most-recent-first and
// cursor-paginated. Optional `?period=` scopes to one reconciliation period.
// Unlike the alert timeline, captures are live ledger transactions, so this is
// backed today (no event sink required).
func listRuleCapturesHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "ruleID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}

		q := store.GetCapturesQuery{}
		if r.URL.Query().Get(QueryKeyCursor) != "" {
			if err := bunpaginate.UnmarshalCursor(r.URL.Query().Get(QueryKeyCursor), &q); err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("invalid '%s' query param", QueryKeyCursor))
				return
			}
		} else {
			options, err := getPaginatedQueryOptionsCaptures(r)
			if err != nil {
				api.BadRequest(w, ErrValidation, err)
				return
			}

			q = store.NewGetCapturesQuery(*options)
		}

		cursor, err := b.GetService().ListCaptures(r.Context(), id, q)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}

		api.RenderCursor(w, *bunpaginate.MapCursor(cursor, func(c models.Capture) *captureResponse {
			return renderCapture(&c)
		}))
	}
}
