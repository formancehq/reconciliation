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
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type evaluationResponse struct {
	ID           string               `json:"id"`
	RuleID       string               `json:"ruleID"`
	StartedAt    time.Time            `json:"startedAt"`
	EndedAt      time.Time            `json:"endedAt"`
	PitPerSource map[string]time.Time `json:"pitPerSource,omitempty"`
	Result       string               `json:"result"`
	Evidence     json.RawMessage      `json:"evidence,omitempty"`
	Error        string               `json:"error,omitempty"`
	CostUnits    int64                `json:"costUnits"`
	CreatedAt    time.Time            `json:"createdAt"`
}

func renderEvaluation(ev *models.Evaluation) *evaluationResponse {
	return &evaluationResponse{
		ID:           ev.ID.String(),
		RuleID:       ev.RuleID.String(),
		StartedAt:    ev.StartedAt,
		EndedAt:      ev.EndedAt,
		PitPerSource: ev.PitPerSource,
		Result:       string(ev.Result),
		Evidence:     ev.Evidence,
		Error:        ev.Error,
		CostUnits:    ev.CostUnits,
		CreatedAt:    ev.CreatedAt,
	}
}

func getEvaluationHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "evaluationID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		ev, err := b.GetService().GetEvaluation(r.Context(), id)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderEvaluation(ev))
	}
}

func listEvaluationsHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := storage.GetEvaluationsQuery{}
		if r.URL.Query().Get(QueryKeyCursor) != "" {
			if err := bunpaginate.UnmarshalCursor(r.URL.Query().Get(QueryKeyCursor), &q); err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("invalid '%s' query param", QueryKeyCursor))
				return
			}
			if err := validateCursorPageSize(q.PageSize); err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("invalid '%s' query param: %w", QueryKeyCursor, err))
				return
			}
		} else {
			options, err := getPaginatedQueryOptionsEvaluations(r)
			if err != nil {
				api.BadRequest(w, ErrValidation, err)
				return
			}
			q = storage.NewGetEvaluationsQuery(*options)
		}
		cursor, err := b.GetService().ListEvaluations(r.Context(), q)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.RenderCursor(w, *cursor)
	}
}
