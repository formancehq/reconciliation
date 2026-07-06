package ledgerstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
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
		return nil, fmt.Errorf("snooze alert %s: %w", id, store.ErrNotFound)
	}

	snooze := &models.Snooze{Until: until.UTC(), By: by, At: now, Note: note}

	b, err := json.Marshal(snooze)
	if err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	alert.Snooze = snooze

	// Status-neutral: prev == new. The snooze value and the transition record land
	// in one metadata write, so the SAVED_METADATA event is self-describing.
	md := map[string]*commonpb.MetadataValue{schema.MetaSnooze: strVal(string(b))}
	if err := stampTransition(md, transitionSnoozed, alert, alert.Status, "", now, map[string]any{"snooze": snooze}); err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	itemAddr := schema.AlertItemAccount(alert.RuleID.String(), alert.PeriodID, fpHash)
	if err := s.client.SaveAccountMetadataValues(ctx, s.controlLedger, itemAddr, md); err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	return alert, nil
}

// UnsnoozeAlert lifts a snooze early. Idempotent: an alert with no snooze is
// returned unchanged with no write. A NotFound is swallowed (the snooze key is
// already gone → the effect is done), which also makes a gRPC retransmit safe.
//
// The snooze delete and the self-describing transition record land in one atomic
// batch, so `by` is now attributed (in the last_transition payload) — the
// DELETED_METADATA event alone carries no actor.
func (s *LedgerStore) UnsnoozeAlert(ctx context.Context, id uuid.UUID, by string) (*models.Alert, error) {
	alert, fpHash, err := s.loadAlertForTransition(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("unsnooze alert %s: %w", id, err)
	}

	if alert.Snooze == nil {
		return alert, nil // no-op — nothing to lift
	}

	// Status-neutral: prev == new. Record the transition (with the actor) and
	// clear the snooze key atomically.
	md := map[string]*commonpb.MetadataValue{}
	if err := stampTransition(md, transitionUnsnoozed, alert, alert.Status, "", time.Now().UTC(), map[string]any{"by": by}); err != nil {
		return nil, fmt.Errorf("unsnooze alert %s: %w", id, err)
	}

	itemAddr := schema.AlertItemAccount(alert.RuleID.String(), alert.PeriodID, fpHash)
	if err := s.client.ApplyMetadata(ctx, s.controlLedger, itemAddr, md, schema.MetaSnooze); err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("unsnooze alert %s: %w", id, err)
	}

	alert.Snooze = nil

	return alert, nil
}
