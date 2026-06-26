package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

// AckAlertRequest is the body of POST /alerts/{id}/ack.
type AckAlertRequest struct {
	By   string `json:"by"`
	Note string `json:"note,omitempty"`
}

// ResolveAlertRequest is the body of POST /alerts/{id}/resolve. When
// TransactionRefs is non-empty the resolution kind is `fixed_by_booking`;
// otherwise the caller is recording a system-attributed auto-resolution. The
// `accepted_by_business` path is a separate endpoint (AcceptAlert).
type ResolveAlertRequest struct {
	By              string   `json:"by"`
	Note            string   `json:"note,omitempty"`
	TransactionRefs []string `json:"transactionRefs,omitempty"`
}

// AcceptAlertRequest is the body of POST /alerts/{id}/accept. The note field
// is required by spec — see PRD §6.4 (Resolution model).
type AcceptAlertRequest struct {
	By        string     `json:"by"`
	Note      string     `json:"note"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// SnoozeAlertRequest is the body of POST /alerts/{id}/snooze. `until` is the
// instant the mute lifts; it must be in the future. The alert stays OPEN and
// keeps counting against period-green — only its notifications are silenced.
type SnoozeAlertRequest struct {
	By    string    `json:"by"`
	Until time.Time `json:"until"`
	Note  string    `json:"note,omitempty"`
}

// UnsnoozeAlertRequest is the body of POST /alerts/{id}/unsnooze. `by`
// attributes the action in the append-only log.
type UnsnoozeAlertRequest struct {
	By string `json:"by"`
}

// AckAlert transitions an OPEN alert to ACKNOWLEDGED. Idempotent at the
// storage layer — a second ack on an already-ACKNOWLEDGED alert preserves the
// original metadata and does NOT append a new event.
func (s *Service) AckAlert(ctx context.Context, id uuid.UUID, req *AckAlertRequest) (*models.Alert, error) {
	if req == nil || req.By == "" {
		return nil, errors.New("ack: 'by' is required")
	}
	ack := &models.Ack{
		By:   req.By,
		At:   time.Now().UTC(),
		Note: req.Note,
	}
	return s.store.AckAlert(ctx, id, ack)
}

// ResolveAlert applies a manual `fixed_by_booking` resolution. The system
// `auto` path lives at the storage layer (AutoResolveAlert), invoked by the
// evaluation loop on PASS.
func (s *Service) ResolveAlert(ctx context.Context, id uuid.UUID, req *ResolveAlertRequest) (*models.Alert, error) {
	if req == nil || req.By == "" {
		return nil, errors.New("resolve: 'by' is required")
	}
	resolution := &models.Resolution{
		Kind:            models.ResolutionFixedByBooking,
		By:              req.By,
		At:              time.Now().UTC(),
		Note:            req.Note,
		TransactionRefs: req.TransactionRefs,
	}
	return s.store.ResolveAlertManual(ctx, id, resolution)
}

// AcceptAlert applies the `accepted_by_business` resolution. Snapshots the
// alert's current evidence onto the resolution so the audit trail is
// reproducible even after the underlying balances change.
func (s *Service) AcceptAlert(ctx context.Context, id uuid.UUID, req *AcceptAlertRequest) (*models.Alert, error) {
	if req == nil || req.By == "" {
		return nil, errors.New("accept: 'by' is required")
	}
	if req.Note == "" {
		return nil, errors.New("accept: 'note' is required for business acceptance")
	}

	current, err := s.store.GetAlert(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshot := current.Evidence
	if len(snapshot) == 0 {
		snapshot = json.RawMessage("null")
	}

	resolution := &models.Resolution{
		Kind:             models.ResolutionAcceptedByBusiness,
		By:               req.By,
		At:               time.Now().UTC(),
		Note:             req.Note,
		EvidenceSnapshot: snapshot,
		ExpiresAt:        req.ExpiresAt,
	}
	return s.store.AcceptAlert(ctx, id, resolution)
}

// SnoozeAlert mutes an alert's notifications until req.Until. The alert keeps
// failing and stays counted against period-green; only its webhooks go quiet.
func (s *Service) SnoozeAlert(ctx context.Context, id uuid.UUID, req *SnoozeAlertRequest) (*models.Alert, error) {
	if req == nil || req.By == "" {
		return nil, errors.New("snooze: 'by' is required")
	}
	if req.Until.IsZero() {
		return nil, errors.New("snooze: 'until' is required")
	}
	if !req.Until.After(time.Now().UTC()) {
		return nil, errors.New("snooze: 'until' must be in the future")
	}
	return s.store.SnoozeAlert(ctx, id, req.Until, req.By, req.Note)
}

// UnsnoozeAlert lifts a snooze early. Idempotent — unsnoozing an alert that is
// not snoozed returns it unchanged.
func (s *Service) UnsnoozeAlert(ctx context.Context, id uuid.UUID, req *UnsnoozeAlertRequest) (*models.Alert, error) {
	if req == nil || req.By == "" {
		return nil, errors.New("unsnooze: 'by' is required")
	}
	return s.store.UnsnoozeAlert(ctx, id, req.By)
}

// GetAlert returns the alert or storage.ErrNotFound.
func (s *Service) GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error) {
	return s.store.GetAlert(ctx, id)
}

// ListAlerts is a passthrough — filters live in storage.
func (s *Service) ListAlerts(ctx context.Context, q storage.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error) {
	return s.store.ListAlerts(ctx, q)
}

// ListAlertEvents returns a page of one alert's append-only history,
// most-recent-first. Paginated because a long-lived alert's timeline is
// unbounded (one row per failing evaluation). This is the API surface for the
// "timeline" view and any future audit-export tooling.
func (s *Service) ListAlertEvents(ctx context.Context, alertID uuid.UUID, q storage.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error) {
	return s.store.ListAlertEvents(ctx, alertID, q)
}

// engineErrorFingerprint is the synthetic fingerprint used by the evaluation
// orchestrator when a kernel/resolver failure raises a meta-alert (see
// openEngineErrorAlert in evaluation.go). Promoted to a package-level
// constant so the value lives in one place — anywhere we route or filter
// engine-health alerts can match on this literal.
const engineErrorFingerprint = "engine.error"
