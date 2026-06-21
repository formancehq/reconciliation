package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// GetAlert returns the alert or storage.ErrNotFound.
func (s *Service) GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error) {
	return s.store.GetAlert(ctx, id)
}

// ListAlerts is a passthrough — filters live in storage.
func (s *Service) ListAlerts(ctx context.Context, q storage.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error) {
	return s.store.ListAlerts(ctx, q)
}

// ListAlertEvents returns the full append-only history of one alert,
// most-recent-first. This is the API surface for the "timeline" view in the
// UI and for any future audit-export tooling.
func (s *Service) ListAlertEvents(ctx context.Context, alertID uuid.UUID) ([]models.AlertEvent, error) {
	return s.store.ListAlertEvents(ctx, alertID)
}

// engineErrorFingerprint is the synthetic fingerprint used by the evaluation
// orchestrator when a kernel/resolver failure raises a meta-alert. Lifting
// it to a package-level constant makes the alert layer's "is this a data
// or an engine alert?" check trivial and grep-able.
const engineErrorFingerprint = "engine.error"

func isEngineErrorAlert(a *models.Alert) bool {
	if a == nil {
		return false
	}
	return a.Fingerprint == engineErrorFingerprint
}

// FormatAlertSummary is a convenience for logs / digests. Kept here so the
// alert-layer concerns aren't littered across the service.
func FormatAlertSummary(a *models.Alert) string {
	if a == nil {
		return ""
	}
	tag := "data"
	if isEngineErrorAlert(a) {
		tag = "engine.error"
	}
	return fmt.Sprintf("[%s/%s] rule=%s fingerprint=%s status=%s severity=%s",
		tag, a.ID, a.RuleID, a.Fingerprint, a.Status, a.Severity)
}
