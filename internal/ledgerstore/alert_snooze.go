package ledgerstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
)

// SnoozeAlert mutes an active alert's notifications until `until`. Status-neutral
// — no marker move. An activity transaction sets the `snooze` metadata on the
// item and records the manual interaction atomically. Re-snoozing overwrites the window. Rejects a
// non-future `until` and any non-active (RESOLVED) alert.
func (s *LedgerStore) SnoozeAlert(ctx context.Context, id uuid.UUID, snooze *models.Snooze) (*models.Alert, error) {
	now := snooze.At
	if !snooze.Until.After(now) {
		return nil, fmt.Errorf("SnoozeAlert: until must be in the future")
	}

	alert, fpHash, err := s.loadAlertForTransition(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	if alert.Status == models.AlertResolved {
		return nil, fmt.Errorf("snooze alert %s: %w", id, store.ErrNotFound)
	}

	b, err := json.Marshal(snooze)
	if err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	alert.Snooze = snooze
	alert.UpdatedAt = now

	// Status-neutral: prev == new. The snooze value and the transition record land
	// in one activity transaction, so the committed event is self-describing.
	md := map[string]*commonpb.MetadataValue{schema.MetaSnooze: strVal(string(b)), schema.MetaUpdatedAt: dtVal(now)}
	if err := stampTransition(md, transitionSnoozed, alert, alert.Status, "", now, map[string]any{"snooze": snooze}); err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	itemAddr := schema.AlertItemAccount(alert.RuleID.String(), alert.PeriodID, fpHash)
	txmd, err := alertActivityMetadata(alert, md)
	if err != nil {
		return nil, fmt.Errorf("snooze alert %s activity: %w", id, err)
	}
	if err := s.client.CreateTransaction(ctx, ledger.CreateTransactionInput{Ledger: s.controlLedger, ScriptName: schema.NumscriptActivity, ScriptVersion: schema.NumscriptVersion, Vars: activityVars(alert.RuleID.String()), TxMetadata: txmd, AccountMetadata: map[string]*commonpb.MetadataMap{itemAddr: {Values: md}}, IdempotencyKey: uniqueActionKey("snooze", id.String(), snooze.Until.UTC().Format(time.RFC3339Nano))}); err != nil {
		return nil, fmt.Errorf("snooze alert %s: %w", id, err)
	}

	return alert, nil
}

// UnsnoozeAlert lifts a snooze early. Idempotent: an alert with no snooze is
// returned unchanged with no write. A NotFound is swallowed (the snooze key is
// already gone → the effect is done), which also makes a gRPC retransmit safe.
//
// The snooze delete and the self-describing transition record land in one atomic
// activity transaction, so `by` is attributed in the history payload.
func (s *LedgerStore) UnsnoozeAlert(ctx context.Context, id uuid.UUID, by string, actor *models.Actor) (*models.Alert, error) {
	alert, fpHash, err := s.loadAlertForTransition(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("unsnooze alert %s: %w", id, err)
	}

	if alert.Snooze == nil {
		return alert, nil // no-op — nothing to lift
	}

	// Status-neutral: prev == new. Record the transition (with the actor) and
	// clear the snooze key atomically. Unsnooze has no durable model to hang the
	// actor on (it clears the snooze), so it rides the transition payload.
	unsnoozePayload := map[string]any{"by": by}
	if actor != nil {
		unsnoozePayload["actor"] = actor
	}
	md := map[string]*commonpb.MetadataValue{}
	now := time.Now().UTC()
	alert.UpdatedAt = now
	md[schema.MetaUpdatedAt] = dtVal(now)
	if err := stampTransition(md, transitionUnsnoozed, alert, alert.Status, "", now, unsnoozePayload); err != nil {
		return nil, fmt.Errorf("unsnooze alert %s: %w", id, err)
	}

	itemAddr := schema.AlertItemAccount(alert.RuleID.String(), alert.PeriodID, fpHash)
	txmd, err := alertActivityMetadata(alert, md)
	if err != nil {
		return nil, fmt.Errorf("unsnooze alert %s activity: %w", id, err)
	}
	if err := s.client.CreateTransaction(ctx, ledger.CreateTransactionInput{Ledger: s.controlLedger, ScriptName: schema.NumscriptActivity, ScriptVersion: schema.NumscriptVersion, Vars: activityVars(alert.RuleID.String()), TxMetadata: txmd, AccountMetadata: map[string]*commonpb.MetadataMap{itemAddr: {Values: md}}, DeleteMetadata: map[string][]string{itemAddr: {schema.MetaSnooze}}, IdempotencyKey: alertActionKey("unsnooze", id.String(), alert.Snooze.At.UTC().Format(time.RFC3339Nano))}); err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("unsnooze alert %s: %w", id, err)
	}

	alert.Snooze = nil

	return alert, nil
}
