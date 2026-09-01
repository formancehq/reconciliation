package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ruleResponse struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	TemplateKind    string            `json:"templateKind"`
	TemplateSpec    json.RawMessage   `json:"templateSpec"`
	CompiledCEL     string            `json:"compiledCEL,omitempty"`
	Enabled         bool              `json:"enabled"`
	Severity        string            `json:"severity"`
	PeriodType      string            `json:"periodType"`
	Schedule        *models.Schedule  `json:"schedule,omitempty"`
	Notifications   []string          `json:"notifications,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	ContractVersion int               `json:"contractVersion,omitempty"`
	Revision        string            `json:"revision,omitempty"`
}

func renderRule(r *models.Rule) *ruleResponse {
	response := &ruleResponse{
		ID:            r.ID.String(),
		Name:          r.Name,
		TemplateKind:  string(r.TemplateKind),
		TemplateSpec:  r.TemplateSpec,
		CompiledCEL:   r.CompiledCEL,
		Enabled:       r.Enabled,
		Severity:      string(r.Severity),
		PeriodType:    string(r.PeriodType),
		Schedule:      r.Schedule,
		Notifications: r.Notifications,
		Labels:        r.Labels,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
	if r.ContractVersion.Effective() == models.ContractVersionV2 {
		response.ContractVersion = int(models.ContractVersionV2)
		response.Revision = r.Revision
	}
	return response
}

func createRuleHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req service.CreateRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		rule, err := b.GetService().CreateRule(r.Context(), &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Created(w, renderRule(rule))
	}
}

func getRuleHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "ruleID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		rule, err := b.GetService().GetRule(r.Context(), id)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderRule(rule))
	}
}

func deleteRuleHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "ruleID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		if err := b.GetService().DeleteRule(r.Context(), id); err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.NoContent(w)
	}
}

// patchRuleRequest mirrors store.RulePatch but uses JSON-friendly types so
// the on-wire shape matches the rest of the API. Missing fields are left
// unchanged; an explicit `null` is treated as "unset" only for pointers.
type patchRuleRequest struct {
	Name          *string              `json:"name,omitempty"`
	TemplateKind  *models.TemplateKind `json:"templateKind,omitempty"`
	TemplateSpec  json.RawMessage      `json:"templateSpec,omitempty"`
	Enabled       *bool                `json:"enabled,omitempty"`
	Severity      *models.Severity     `json:"severity,omitempty"`
	Schedule      *models.Schedule     `json:"schedule,omitempty"`
	Notifications *[]string            `json:"notifications,omitempty"`
	Labels        *map[string]string   `json:"labels,omitempty"`
}

func patchRuleHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "ruleID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req patchRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		patch := store.RulePatch{
			Name:          req.Name,
			TemplateKind:  req.TemplateKind,
			TemplateSpec:  req.TemplateSpec,
			Enabled:       req.Enabled,
			Severity:      req.Severity,
			Schedule:      req.Schedule,
			Notifications: req.Notifications,
			Labels:        req.Labels,
		}
		if err := b.GetService().PatchRule(r.Context(), id, patch); err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		// Re-fetch so the caller gets the full post-patch document including
		// the rederived compiled_cel.
		rule, err := b.GetService().GetRule(r.Context(), id)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderRule(rule))
	}
}

func listRulesHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := store.GetRulesQuery{}
		if r.URL.Query().Get(QueryKeyCursor) != "" {
			if err := bunpaginate.UnmarshalCursor(r.URL.Query().Get(QueryKeyCursor), &q); err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("invalid '%s' query param", QueryKeyCursor))
				return
			}
		} else {
			options, err := getPaginatedQueryOptionsRules(r)
			if err != nil {
				api.BadRequest(w, ErrValidation, err)
				return
			}
			q = store.NewGetRulesQuery(*options)
		}
		cursor, err := b.GetService().ListRules(r.Context(), q)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		// Render through renderRule on both contracts (it branches on the
		// contract version internally for the V2-only fields).
		api.RenderCursor(w, *bunpaginate.MapCursor(cursor, func(rule models.Rule) *ruleResponse {
			return renderRule(&rule)
		}))
	}
}

// evaluateRuleRequest is the API-shaped equivalent of service.EvaluateRuleRequest.
// `at` defaults to "now". (The former `safetyMargin` was removed with the read
// model change — ledger reads are live, not at a shifted PIT.)
type evaluateRuleRequest struct {
	At *time.Time `json:"at,omitempty"`
}

func evaluateRuleHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "ruleID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req evaluateRuleRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				api.BadRequest(w, ErrMissingOrInvalidBody, err)
				return
			}
		}
		var svcReq service.EvaluateRuleRequest
		if req.At != nil {
			svcReq.PIT = *req.At
		}
		ev, err := b.GetService().EvaluateRule(r.Context(), id, svcReq)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderEvaluation(ev))
	}
}
