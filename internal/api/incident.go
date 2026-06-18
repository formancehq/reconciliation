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
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type incidentResponse struct {
	ID                string             `json:"id"`
	RuleID            string             `json:"ruleID"`
	Fingerprint       string             `json:"fingerprint"`
	Status            string             `json:"status"`
	Severity          string             `json:"severity"`
	OpenedAt          time.Time          `json:"openedAt"`
	LastSeenAt        time.Time          `json:"lastSeenAt"`
	OccurrenceCount   int                `json:"occurrenceCount"`
	FirstEvaluationID string             `json:"firstEvaluationID"`
	LastEvaluationID  string             `json:"lastEvaluationID"`
	Evidence          json.RawMessage    `json:"evidence,omitempty"`
	Ack               *models.Ack        `json:"ack,omitempty"`
	Resolution        *models.Resolution `json:"resolution,omitempty"`
	ParentIncidentID  *string            `json:"parentIncidentID,omitempty"`
	Labels            map[string]string  `json:"labels,omitempty"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
}

func renderIncident(in *models.Incident) *incidentResponse {
	var parent *string
	if in.ParentIncidentID != nil {
		s := in.ParentIncidentID.String()
		parent = &s
	}
	return &incidentResponse{
		ID:                in.ID.String(),
		RuleID:            in.RuleID.String(),
		Fingerprint:       in.Fingerprint,
		Status:            string(in.Status),
		Severity:          string(in.Severity),
		OpenedAt:          in.OpenedAt,
		LastSeenAt:        in.LastSeenAt,
		OccurrenceCount:   in.OccurrenceCount,
		FirstEvaluationID: in.FirstEvaluationID.String(),
		LastEvaluationID:  in.LastEvaluationID.String(),
		Evidence:          in.Evidence,
		Ack:               in.Ack,
		Resolution:        in.Resolution,
		ParentIncidentID:  parent,
		Labels:            in.Labels,
		CreatedAt:         in.CreatedAt,
		UpdatedAt:         in.UpdatedAt,
	}
}

func getIncidentHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "incidentID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		inc, err := b.GetService().GetIncident(r.Context(), id)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderIncident(inc))
	}
}

func listIncidentsHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := storage.GetIncidentsQuery{}
		if r.URL.Query().Get(QueryKeyCursor) != "" {
			if err := bunpaginate.UnmarshalCursor(r.URL.Query().Get(QueryKeyCursor), &q); err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("invalid '%s' query param", QueryKeyCursor))
				return
			}
		} else {
			options, err := getPaginatedQueryOptionsIncidents(r)
			if err != nil {
				api.BadRequest(w, ErrValidation, err)
				return
			}
			q = storage.NewGetIncidentsQuery(*options)
		}
		cursor, err := b.GetService().ListIncidents(r.Context(), q)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.RenderCursor(w, *cursor)
	}
}

func ackIncidentHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "incidentID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req service.AckIncidentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		inc, err := b.GetService().AckIncident(r.Context(), id, &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderIncident(inc))
	}
}

func resolveIncidentHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "incidentID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req service.ResolveIncidentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		inc, err := b.GetService().ResolveIncident(r.Context(), id, &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderIncident(inc))
	}
}

func acceptIncidentHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "incidentID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req service.AcceptIncidentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		inc, err := b.GetService().AcceptIncident(r.Context(), id, &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderIncident(inc))
	}
}
