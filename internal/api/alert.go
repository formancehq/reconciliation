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
	"github.com/formancehq/reconciliation/internal/contractversion"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type alertResponse struct {
	ID               string             `json:"id"`
	RuleID           string             `json:"ruleID"`
	PeriodID         string             `json:"periodID,omitempty"`
	Fingerprint      string             `json:"fingerprint"`
	Status           string             `json:"status"`
	Severity         string             `json:"severity"`
	FirstSeenAt      time.Time          `json:"firstSeenAt"`
	LastSeenAt       time.Time          `json:"lastSeenAt"`
	OccurrenceCount  int64              `json:"occurrenceCount"`
	LastEvaluationID string             `json:"lastEvaluationID"`
	Evidence         json.RawMessage    `json:"evidence,omitempty"`
	Ack              *models.Ack        `json:"ack,omitempty"`
	Resolution       *models.Resolution `json:"resolution,omitempty"`
	Snooze           *models.Snooze     `json:"snooze,omitempty"`
	Labels           map[string]string  `json:"labels,omitempty"`
	CreatedAt        time.Time          `json:"createdAt"`
	UpdatedAt        time.Time          `json:"updatedAt"`
	ContractVersion  int                `json:"contractVersion,omitempty"`
}

func renderAlert(a *models.Alert) *alertResponse {
	response := &alertResponse{
		ID:               a.ID.String(),
		RuleID:           a.RuleID.String(),
		Fingerprint:      a.Fingerprint,
		Status:           string(a.Status),
		Severity:         string(a.Severity),
		FirstSeenAt:      a.FirstSeenAt,
		LastSeenAt:       a.LastSeenAt,
		OccurrenceCount:  a.OccurrenceCount,
		LastEvaluationID: a.LastEvaluationID.String(),
		Evidence:         a.Evidence,
		Ack:              a.Ack,
		Resolution:       a.Resolution,
		Snooze:           a.Snooze,
		Labels:           a.Labels,
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
	}
	if a.ContractVersion.Effective() == models.ContractVersionV2 {
		response.ContractVersion = int(models.ContractVersionV2)
		response.PeriodID = a.PeriodID
	}
	return response
}

type alertEventResponse struct {
	ID           string          `json:"id"`
	AlertID      string          `json:"alertID"`
	EvaluationID *string         `json:"evaluationID,omitempty"`
	Type         string          `json:"type"`
	PrevStatus   *string         `json:"prevStatus,omitempty"`
	NewStatus    string          `json:"newStatus"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	At           time.Time       `json:"at"`
	IsReopen     bool            `json:"isReopen"`
	Notify       bool            `json:"notify"`
}

func renderAlertEvent(e *models.AlertEvent) *alertEventResponse {
	var evalID *string
	if e.EvaluationID != nil {
		s := e.EvaluationID.String()
		evalID = &s
	}
	var prev *string
	if e.PrevStatus != nil {
		s := string(*e.PrevStatus)
		prev = &s
	}
	return &alertEventResponse{
		ID:           e.ID.String(),
		AlertID:      e.AlertID.String(),
		EvaluationID: evalID,
		Type:         string(e.Type),
		PrevStatus:   prev,
		NewStatus:    string(e.NewStatus),
		Payload:      e.Payload,
		At:           e.At,
		IsReopen:     e.IsReopen(),
		Notify:       e.Notify,
	}
}

func getAlertHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "alertID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		alert, err := b.GetService().GetAlert(r.Context(), id)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderAlert(alert))
	}
}

func listAlertsHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := store.GetAlertsQuery{}
		if r.URL.Query().Get(QueryKeyCursor) != "" {
			if err := bunpaginate.UnmarshalCursor(r.URL.Query().Get(QueryKeyCursor), &q); err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("invalid '%s' query param", QueryKeyCursor))
				return
			}
		} else {
			options, err := getPaginatedQueryOptionsAlerts(r)
			if err != nil {
				api.BadRequest(w, ErrValidation, err)
				return
			}
			q = store.NewGetAlertsQuery(*options)
		}
		cursor, err := b.GetService().ListAlerts(r.Context(), q)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		if version, _ := contractversion.FromContext(r.Context()); version == models.ContractVersionV2 {
			api.RenderCursor(w, *bunpaginate.MapCursor(cursor, func(alert models.Alert) *alertResponse {
				return renderAlert(&alert)
			}))
			return
		}
		api.RenderCursor(w, *cursor)
	}
}

// listAlertEventsHandler returns the append-only timeline for a single alert,
// cursor-paginated (most-recent-first). Pagination is required: a long-lived
// alert (rule with periodType continuous, engine.error meta-alert) accumulates one row
// per failing evaluation indefinitely — suppression keeps those off the bus but
// not out of the table — so an unbounded response could be enormous.
func listAlertEventsHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "alertID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		q := store.GetAlertEventsQuery{}
		if r.URL.Query().Get(QueryKeyCursor) != "" {
			if err := bunpaginate.UnmarshalCursor(r.URL.Query().Get(QueryKeyCursor), &q); err != nil {
				api.BadRequest(w, ErrValidation, fmt.Errorf("invalid '%s' query param", QueryKeyCursor))
				return
			}
		} else {
			options, err := getPaginatedQueryOptionsAlertEvents(r)
			if err != nil {
				api.BadRequest(w, ErrValidation, err)
				return
			}
			q = store.NewGetAlertEventsQuery(*options)
		}
		cursor, err := b.GetService().ListAlertEvents(r.Context(), id, q)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.RenderCursor(w, *bunpaginate.MapCursor(cursor, func(e models.AlertEvent) *alertEventResponse {
			return renderAlertEvent(&e)
		}))
	}
}

func ackAlertHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "alertID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req service.AckAlertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		alert, err := b.GetService().AckAlert(r.Context(), id, &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderAlert(alert))
	}
}

func resolveAlertHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "alertID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req service.ResolveAlertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		alert, err := b.GetService().ResolveAlert(r.Context(), id, &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderAlert(alert))
	}
}

func acceptAlertHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "alertID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req service.AcceptAlertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		alert, err := b.GetService().AcceptAlert(r.Context(), id, &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderAlert(alert))
	}
}

func snoozeAlertHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "alertID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req service.SnoozeAlertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		alert, err := b.GetService().SnoozeAlert(r.Context(), id, &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderAlert(alert))
	}
}

func unsnoozeAlertHandler(b backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "alertID"))
		if err != nil {
			api.BadRequest(w, ErrInvalidID, err)
			return
		}
		var req service.UnsnoozeAlertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.BadRequest(w, ErrMissingOrInvalidBody, err)
			return
		}
		alert, err := b.GetService().UnsnoozeAlert(r.Context(), id, &req)
		if err != nil {
			handleServiceErrors(w, r, err)
			return
		}
		api.Ok(w, renderAlert(alert))
	}
}
