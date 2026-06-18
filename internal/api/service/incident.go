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

// AckIncidentRequest is the body of POST /incidents/{id}/ack.
type AckIncidentRequest struct {
	By   string `json:"by"`
	Note string `json:"note,omitempty"`
}

// ResolveIncidentRequest is the body of POST /incidents/{id}/resolve. When
// TransactionRefs is non-empty the resolution kind is `fixed_by_booking`;
// otherwise the caller is recording a system-attributed auto-resolution. The
// `accepted_by_business` path is a separate endpoint (AcceptIncident).
type ResolveIncidentRequest struct {
	By              string   `json:"by"`
	Note            string   `json:"note,omitempty"`
	TransactionRefs []string `json:"transactionRefs,omitempty"`
}

// AcceptIncidentRequest is the body of POST /incidents/{id}/accept. The note
// field is required by spec — see PRD §6.4 (Resolution model).
type AcceptIncidentRequest struct {
	By        string     `json:"by"`
	Note      string     `json:"note"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// AckIncident transitions an OPEN incident to ACKNOWLEDGED. Idempotent.
func (s *Service) AckIncident(ctx context.Context, id uuid.UUID, req *AckIncidentRequest) (*models.Incident, error) {
	if req == nil || req.By == "" {
		return nil, errors.New("ack: 'by' is required")
	}
	ack := &models.Ack{
		By:   req.By,
		At:   time.Now().UTC(),
		Note: req.Note,
	}
	return s.store.AckIncident(ctx, id, ack)
}

// ResolveIncident applies a manual `fixed_by_booking` resolution (or `auto` if
// the caller is the system path — see AutoResolveIncident at the storage layer).
func (s *Service) ResolveIncident(ctx context.Context, id uuid.UUID, req *ResolveIncidentRequest) (*models.Incident, error) {
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
	return s.store.ResolveIncidentManual(ctx, id, resolution)
}

// AcceptIncident applies the `accepted_by_business` resolution. Snapshots the
// incident's current evidence onto the resolution so the audit trail is
// reproducible after acceptance.
func (s *Service) AcceptIncident(ctx context.Context, id uuid.UUID, req *AcceptIncidentRequest) (*models.Incident, error) {
	if req == nil || req.By == "" {
		return nil, errors.New("accept: 'by' is required")
	}
	if req.Note == "" {
		return nil, errors.New("accept: 'note' is required for business acceptance")
	}

	// Snapshot the current evidence for the audit trail.
	current, err := s.store.GetIncident(ctx, id)
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
	return s.store.AcceptIncident(ctx, id, resolution)
}

// GetIncident returns the incident or storage.ErrNotFound.
func (s *Service) GetIncident(ctx context.Context, id uuid.UUID) (*models.Incident, error) {
	return s.store.GetIncident(ctx, id)
}

// ListIncidents is a passthrough — filters live in storage.
func (s *Service) ListIncidents(ctx context.Context, q storage.GetIncidentsQuery) (*bunpaginate.Cursor[models.Incident], error) {
	return s.store.ListIncidents(ctx, q)
}

// engineErrorFingerprint is the synthetic fingerprint used by the evaluation
// orchestrator when a kernel/resolver failure raises a meta-incident. Lifting
// it to a package-level constant makes the incident layer's "is this a data
// or an engine incident?" check trivial and grep-able.
const engineErrorFingerprint = "engine.error"

func isEngineErrorIncident(in *models.Incident) bool {
	if in == nil {
		return false
	}
	return in.Fingerprint == engineErrorFingerprint
}

// FormatIncidentSummary is a tiny convenience for logs / digests. Kept here so
// the incident-layer concerns aren't littered across the service.
func FormatIncidentSummary(in *models.Incident) string {
	if in == nil {
		return ""
	}
	tag := "data"
	if isEngineErrorIncident(in) {
		tag = "engine.error"
	}
	return fmt.Sprintf("[%s/%s] rule=%s fingerprint=%s status=%s severity=%s",
		tag, in.ID, in.RuleID, in.Fingerprint, in.Status, in.Severity)
}
