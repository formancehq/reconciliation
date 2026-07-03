package ledgerstore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

// AckAlert transitions OPEN → ACKNOWLEDGED with the supplied Ack metadata.
// Idempotent: re-ack of an already-ACKNOWLEDGED alert returns it UNCHANGED and
// writes nothing — the original ack (who/when/why) is the audit trail. Rejects a
// RESOLVED alert with storage.ErrNotFound (matches the Postgres store).
func (s *LedgerStore) AckAlert(ctx context.Context, id uuid.UUID, ack *models.Ack) (*models.Alert, error) {
	alert, fpHash, err := s.loadAlertForTransition(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("ack alert %s: %w", id, err)
	}

	switch alert.Status {
	case models.AlertResolved:
		return nil, fmt.Errorf("ack alert %s: %w", id, storage.ErrNotFound)
	case models.AlertAcknowledged:
		return alert, nil // idempotent no-op, preserve the original ack
	}

	alert.Status = models.AlertAcknowledged
	alert.Ack = ack

	md, err := alertToMetadata(alert)
	if err != nil {
		return nil, fmt.Errorf("ack alert %s: %w", id, err)
	}

	if err := s.moveMarker(ctx, alert, fpHash, schema.StateOpen, schema.StateAck, md, nil,
		alertActionKey("ack", id.String(), strconv.FormatInt(ack.At.UnixMicro(), 10))); err != nil {
		return nil, fmt.Errorf("ack alert %s: %w", id, err)
	}

	return alert, nil
}

// ResolveAlertManual closes the alert with kind "fixed_by_booking" (or "auto"
// from the system path). Rejects an already-resolved alert.
func (s *LedgerStore) ResolveAlertManual(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Alert, error) {
	if resolution.Kind != models.ResolutionFixedByBooking && resolution.Kind != models.ResolutionAuto {
		return nil, fmt.Errorf("ResolveAlertManual: unsupported resolution kind %q", resolution.Kind)
	}

	return s.resolve(ctx, id, resolution, "resolve")
}

// AcceptAlert closes the alert with kind "accepted_by_business". Note is
// required; the service layer captures the evidence snapshot on the resolution.
func (s *LedgerStore) AcceptAlert(ctx context.Context, id uuid.UUID, resolution *models.Resolution) (*models.Alert, error) {
	if resolution.Kind != models.ResolutionAcceptedByBusiness {
		return nil, fmt.Errorf("AcceptAlert: expected kind %q, got %q", models.ResolutionAcceptedByBusiness, resolution.Kind)
	}

	if resolution.Note == "" {
		return nil, fmt.Errorf("AcceptAlert: note is required")
	}

	return s.resolve(ctx, id, resolution, "accept")
}

// resolve is the shared {OPEN,ACK} → RESOLVED transition for the manual /
// accept paths: a guarded marker move to st:resolved, the resolution + status
// mirror set on the item, and any snooze cleared. Rejects a non-active alert.
func (s *LedgerStore) resolve(ctx context.Context, id uuid.UUID, resolution *models.Resolution, action string) (*models.Alert, error) {
	alert, fpHash, err := s.loadAlertForTransition(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%s alert %s: %w", action, id, err)
	}

	if alert.Status == models.AlertResolved {
		return nil, fmt.Errorf("%s alert %s: %w", action, id, storage.ErrNotFound)
	}

	from := statusToState(alert.Status)
	deletes := s.snoozeDelete(alert, fpHash)

	alert.Status = models.AlertResolved
	alert.Resolution = resolution
	alert.Snooze = nil

	md, err := alertToMetadata(alert)
	if err != nil {
		return nil, fmt.Errorf("%s alert %s: %w", action, id, err)
	}

	if err := s.moveMarker(ctx, alert, fpHash, from, schema.StateResolved, md, deletes,
		alertActionKey(action, id.String(), strconv.FormatInt(resolution.At.UnixMicro(), 10))); err != nil {
		return nil, fmt.Errorf("%s alert %s: %w", action, id, err)
	}

	return alert, nil
}

// AutoResolveAlert closes the active alert (if any) for (rule, fingerprint,
// period) with resolution.kind = "auto". No-op returning (nil, nil) when no
// active alert exists in that period. Addressed structurally (no id lookup).
func (s *LedgerStore) AutoResolveAlert(ctx context.Context, ruleID uuid.UUID, fingerprint, periodID string, evaluationID uuid.UUID, at time.Time) (*models.Alert, error) {
	if periodID == "" {
		periodID = models.ContinuousPeriod
	}

	if at.IsZero() {
		at = time.Now().UTC()
	}

	fpHash := schema.FingerprintHash(fingerprint)
	itemAddr := schema.AlertItemAccount(ruleID.String(), periodID, fpHash)

	acct, err := s.client.GetAccount(ctx, s.controlLedger, itemAddr, 0)
	if err != nil {
		if isNotFound(err) {
			return nil, nil // no alert for this (rule, fingerprint, period)
		}

		return nil, fmt.Errorf("auto-resolve %s: %w", fingerprint, err)
	}

	if len(acct.GetMetadata()) == 0 {
		return nil, nil
	}

	alert, err := alertFromAccount(acct)
	if err != nil {
		return nil, fmt.Errorf("auto-resolve %s: %w", fingerprint, err)
	}

	if alert.Status == models.AlertResolved {
		return nil, nil // already closed — nothing active to resolve
	}

	from := statusToState(alert.Status)
	deletes := s.snoozeDelete(alert, fpHash)

	alert.Status = models.AlertResolved
	alert.Resolution = &models.Resolution{Kind: models.ResolutionAuto, By: "system", At: at}
	alert.Snooze = nil
	alert.LastEvaluationID = evaluationID

	md, err := alertToMetadata(alert)
	if err != nil {
		return nil, fmt.Errorf("auto-resolve %s: %w", fingerprint, err)
	}

	if err := s.moveMarker(ctx, alert, fpHash, from, schema.StateResolved, md, deletes,
		alertActionKey("autoresolve", ruleID.String(), fingerprint, periodID, evaluationID.String())); err != nil {
		return nil, fmt.Errorf("auto-resolve %s: %w", fingerprint, err)
	}

	return alert, nil
}

// loadAlertForTransition resolves an alert by id and decodes it, returning the
// fingerprint hash needed to build its marker addresses.
func (s *LedgerStore) loadAlertForTransition(ctx context.Context, id uuid.UUID) (*models.Alert, string, error) {
	acct, err := s.findAlertItem(ctx, id)
	if err != nil {
		return nil, "", err
	}

	alert, err := alertFromAccount(acct)
	if err != nil {
		return nil, "", err
	}

	return alert, schema.FingerprintHash(alert.Fingerprint), nil
}

// moveMarker runs the guarded alert_move transition: ALERT marker st:{from} →
// st:{to} (bare source = CAS), with the item's descriptive/status metadata set
// (and optional keys deleted) atomically in the same idempotent batch.
func (s *LedgerStore) moveMarker(ctx context.Context, alert *models.Alert, fpHash, from, to string, md map[string]*commonpb.MetadataValue, deletes map[string][]string, key string) error {
	rule := alert.RuleID.String()
	itemAddr := schema.AlertItemAccount(rule, alert.PeriodID, fpHash)

	return s.client.CreateTransaction(ctx, ledger.CreateTransactionInput{
		Ledger:        s.controlLedger,
		ScriptName:    schema.NumscriptAlertMove,
		ScriptVersion: schema.NumscriptVersion,
		Vars: map[string]string{
			schema.VarStFrom: schema.AlertStateAccount(from, rule, alert.PeriodID, fpHash),
			schema.VarStTo:   schema.AlertStateAccount(to, rule, alert.PeriodID, fpHash),
		},
		AccountMetadata: map[string]*commonpb.MetadataMap{itemAddr: {Values: md}},
		DeleteMetadata:  deletes,
		IdempotencyKey:  key,
	})
}

// snoozeDelete returns the delete-metadata set that clears a live snooze on close
// (the ledger rejects deleting an absent key, so only when one is present).
func (s *LedgerStore) snoozeDelete(alert *models.Alert, fpHash string) map[string][]string {
	if alert.Snooze == nil {
		return nil
	}

	itemAddr := schema.AlertItemAccount(alert.RuleID.String(), alert.PeriodID, fpHash)

	return map[string][]string{itemAddr: {schema.MetaSnooze}}
}
