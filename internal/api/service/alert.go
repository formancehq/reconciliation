package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/contractversion"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
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
	if req == nil {
		return nil, errors.New("ack: request is required")
	}
	by, actor, err := resolveActor(ctx, req.By)
	if err != nil {
		return nil, fmt.Errorf("ack: %w", err)
	}
	if _, err := s.getAlertForContract(ctx, id); err != nil {
		return nil, err
	}
	ack := &models.Ack{
		By:    by,
		At:    time.Now().UTC(),
		Note:  req.Note,
		Actor: actor,
	}
	return s.store.AckAlert(ctx, id, ack)
}

// ResolveAlert applies a manual `fixed_by_booking` resolution. The system
// `auto` path lives at the storage layer (AutoResolveAlert), invoked by the
// evaluation loop on PASS.
func (s *Service) ResolveAlert(ctx context.Context, id uuid.UUID, req *ResolveAlertRequest) (*models.Alert, error) {
	if req == nil {
		return nil, errors.New("resolve: request is required")
	}
	by, actor, err := resolveActor(ctx, req.By)
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}
	if _, err := s.getAlertForContract(ctx, id); err != nil {
		return nil, err
	}
	resolution := &models.Resolution{
		Kind:            models.ResolutionFixedByBooking,
		By:              by,
		At:              time.Now().UTC(),
		Note:            req.Note,
		TransactionRefs: req.TransactionRefs,
		Actor:           actor,
	}
	return s.store.ResolveAlertManual(ctx, id, resolution)
}

// AcceptAlert applies the `accepted_by_business` resolution. Snapshots the
// alert's current evidence onto the resolution so the audit trail is
// reproducible even after the underlying balances change.
func (s *Service) AcceptAlert(ctx context.Context, id uuid.UUID, req *AcceptAlertRequest) (*models.Alert, error) {
	if req == nil {
		return nil, errors.New("accept: request is required")
	}
	if req.Note == "" {
		return nil, errors.New("accept: 'note' is required for business acceptance")
	}
	by, actor, err := resolveActor(ctx, req.By)
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}

	current, err := s.getAlertForContract(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshot := current.Evidence
	if len(snapshot) == 0 {
		snapshot = json.RawMessage("null")
	}

	resolution := &models.Resolution{
		Kind:             models.ResolutionAcceptedByBusiness,
		By:               by,
		At:               time.Now().UTC(),
		Note:             req.Note,
		EvidenceSnapshot: snapshot,
		ExpiresAt:        req.ExpiresAt,
		Actor:            actor,
	}
	return s.store.AcceptAlert(ctx, id, resolution)
}

// SnoozeAlert mutes an alert's notifications until req.Until. The alert keeps
// failing and stays counted against period-green; only its webhooks go quiet.
func (s *Service) SnoozeAlert(ctx context.Context, id uuid.UUID, req *SnoozeAlertRequest) (*models.Alert, error) {
	if req == nil {
		return nil, errors.New("snooze: request is required")
	}
	if req.Until.IsZero() {
		return nil, errors.New("snooze: 'until' is required")
	}
	if !req.Until.After(time.Now().UTC()) {
		return nil, errors.New("snooze: 'until' must be in the future")
	}
	by, actor, err := resolveActor(ctx, req.By)
	if err != nil {
		return nil, fmt.Errorf("snooze: %w", err)
	}
	if _, err := s.getAlertForContract(ctx, id); err != nil {
		return nil, err
	}
	return s.store.SnoozeAlert(ctx, id, &models.Snooze{
		Until: req.Until.UTC(),
		By:    by,
		At:    time.Now().UTC(),
		Note:  req.Note,
		Actor: actor,
	})
}

// UnsnoozeAlert lifts a snooze early. Idempotent — unsnoozing an alert that is
// not snoozed returns it unchanged.
func (s *Service) UnsnoozeAlert(ctx context.Context, id uuid.UUID, req *UnsnoozeAlertRequest) (*models.Alert, error) {
	if req == nil {
		return nil, errors.New("unsnooze: request is required")
	}
	by, actor, err := resolveActor(ctx, req.By)
	if err != nil {
		return nil, fmt.Errorf("unsnooze: %w", err)
	}
	if _, err := s.getAlertForContract(ctx, id); err != nil {
		return nil, err
	}
	return s.store.UnsnoozeAlert(ctx, id, by, actor)
}

// GetAlert returns the alert or store.ErrNotFound.
func (s *Service) GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error) {
	return s.getAlertForContract(ctx, id)
}

// ListAlerts is a passthrough — filters live in store.
func (s *Service) ListAlerts(ctx context.Context, q store.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error) {
	if version, ok := contractversion.FromContext(ctx); ok {
		q.Options.Options.ContractVersion = &version
	}
	return s.store.ListAlerts(ctx, q)
}

// ListAlertEvents returns a page of one alert's append-only history,
// most-recent-first. Paginated because a long-lived alert's timeline is
// unbounded (one row per failing evaluation). This is the API surface for the
// "timeline" view and any future audit-export tooling.
func (s *Service) ListAlertEvents(ctx context.Context, alertID uuid.UUID, q store.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error) {
	if _, err := s.getAlertForContract(ctx, alertID); err != nil {
		return nil, err
	}
	return s.store.ListAlertEvents(ctx, alertID, q)
}

// ListCaptures returns a rule's evaluation history — the immutable capture
// records (ADR-003), most-recent-first. This is the API surface for the
// "reconciliation history" view: every run of a rule with its verdict, trigger
// and observed evidence, read live from the control ledger.
func (s *Service) ListCaptures(ctx context.Context, ruleID uuid.UUID, q store.GetCapturesQuery) (*bunpaginate.Cursor[models.Capture], error) {
	if _, err := s.getRuleForContract(ctx, ruleID); err != nil {
		return nil, err
	}
	if version, ok := contractversion.FromContext(ctx); ok {
		q.Options.Options.ContractVersion = &version
	}
	return s.store.ListCaptures(ctx, ruleID, q)
}

// engineErrorFingerprint is the synthetic fingerprint used by the evaluation
// orchestrator when a kernel/resolver failure raises a meta-alert (see
// openEngineErrorAlert in evaluation.go). Promoted to a package-level
// constant so the value lives in one place — anywhere we route or filter
// engine-health alerts can match on this literal.
const engineErrorFingerprint = "engine.error"

// alertCapFingerprint is the synthetic fingerprint for the meta-alert raised
// when an evaluation would open more new alerts than the service allows and its
// alert transitions are withheld (see withholdAlertTransitions in
// evaluation.go). Like engine.error it is a fact about the module's behaviour
// rather than about the money, and routing rules match on this literal.
const alertCapFingerprint = "alert.cap"
