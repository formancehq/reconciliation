package ledgerstore

import (
	"context"
	"fmt"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

// findAlertItem resolves an alert by its UUID to its item account via the
// indexed `id` metadata field (the address is keyed by rule/period/fp, not the
// UUID, so a metadata lookup is the only id→address path). Returns
// storage.ErrNotFound when no item carries that id.
func (s *LedgerStore) findAlertItem(ctx context.Context, id uuid.UUID) (*commonpb.Account, error) {
	accts, err := s.client.QueryAccounts(ctx, s.controlLedger, schema.FilterAll(
		schema.FilterAddressPrefix(schema.ItemPrefix()),
		schema.FilterMetadataString(schema.MetaID, id.String()),
	), 0)
	if err != nil {
		return nil, err
	}

	if len(accts) == 0 {
		return nil, storage.ErrNotFound
	}

	// The id is a generated UUID stored one-per-item, so at most one matches.
	return accts[0], nil
}

// ListActiveAlertFingerprints returns the raw fingerprints of every OPEN /
// ACKNOWLEDGED alert for the rule within the period — the auto-resolve sweep's
// input. Scans the (rule, period) item accounts by address prefix (a builtin
// index, no metadata index needed) and filters status client-side; the raw
// fingerprint lives on the item (the marker address carries only its hash).
// Scoping to periodID is essential: a fresh evaluation of period N must never
// sweep a prior period's open cases.
func (s *LedgerStore) ListActiveAlertFingerprints(ctx context.Context, ruleID uuid.UUID, periodID string) ([]string, error) {
	if periodID == "" {
		periodID = models.ContinuousPeriod
	}

	accts, err := s.client.QueryAccounts(ctx, s.controlLedger,
		schema.FilterAddressPrefix(schema.ItemByRulePeriodPrefix(ruleID.String(), periodID)), 0)
	if err != nil {
		return nil, fmt.Errorf("list active alert fingerprints: %w", err)
	}

	var fps []string

	for _, acct := range accts {
		switch models.AlertStatus(getStr(acct.GetMetadata(), schema.MetaStatus)) {
		case models.AlertOpen, models.AlertAcknowledged:
			fps = append(fps, getStr(acct.GetMetadata(), schema.MetaFingerprint))
		}
	}

	return fps, nil
}

// GetAlert returns the alert by id, or storage.ErrNotFound.
func (s *LedgerStore) GetAlert(ctx context.Context, id uuid.UUID) (*models.Alert, error) {
	acct, err := s.findAlertItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get alert %s: %w", id, err)
	}

	alert, err := alertFromAccount(acct)
	if err != nil {
		return nil, fmt.Errorf("decode alert %s: %w", id, err)
	}

	return alert, nil
}
