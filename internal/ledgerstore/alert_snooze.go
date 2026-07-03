package ledgerstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

// SnoozeAlert mutes an active alert's notifications until `until`. Status-neutral
// — no marker move, just the `snooze` metadata set on the item (a plain,
// retransmit-safe LWW write). Re-snoozing overwrites the window. Rejects a
// non-future `until` and any non-active (RESOLVED) alert.
func (s *LedgerStore) SnoozeAlert(ctx context.Context, id uuid.UUID, until time.Time, by, note string) (*models.Alert, error) {
	now := time.Now().UTC()
	if !until.After(now) {
		return nil, fmt.Errorf("SnoozeAlert: until must be in the future")
	}

	alert, fpHash, err := s.loadAlertForTransition(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	if alert.Status == models.AlertResolved {
		return nil, fmt.Errorf("snooze alert %s: %w", id, storage.ErrNotFound)
	}

	snooze := &models.Snooze{Until: until.UTC(), By: by, At: now, Note: note}

	b, err := json.Marshal(snooze)
	if err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	itemAddr := schema.AlertItemAccount(alert.RuleID.String(), alert.PeriodID, fpHash)
	if err := s.client.SaveAccountMetadataValues(ctx, s.controlLedger, itemAddr,
		map[string]*commonpb.MetadataValue{schema.MetaSnooze: strVal(string(b))}); err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	alert.Snooze = snooze

	return alert, nil
}

// UnsnoozeAlert lifts a snooze early. Idempotent: an alert with no snooze is
// returned unchanged with no write. A NotFound on the delete is swallowed (the
// key is already gone → the effect is done), which also makes a gRPC retransmit
// of the delete safe.
//
// `by` attributes the action; the ledger's DELETED_METADATA event carries no
// actor field, so attribution is deferred to the future semantic event-log
// (RFC §4.4). Accepted here to satisfy the Store contract.
func (s *LedgerStore) UnsnoozeAlert(ctx context.Context, id uuid.UUID, by string) (*models.Alert, error) {
	_ = by // no actor field on DELETED_METADATA; see doc comment

	alert, fpHash, err := s.loadAlertForTransition(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("unsnooze alert %s: %w", id, err)
	}

	if alert.Snooze == nil {
		return alert, nil // no-op — nothing to lift
	}

	itemAddr := schema.AlertItemAccount(alert.RuleID.String(), alert.PeriodID, fpHash)
	if err := s.client.DeleteAccountMetadata(ctx, s.controlLedger, itemAddr, schema.MetaSnooze); err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("unsnooze alert %s: %w", id, err)
	}

	alert.Snooze = nil

	return alert, nil
}
