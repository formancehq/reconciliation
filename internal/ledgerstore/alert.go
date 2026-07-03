package ledgerstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

// OpenOrUpdateAlert is the single dedup-aware write the evaluation service calls
// for every FAILING outcome. It mirrors the Postgres store's three paths,
// decided from the alert's current state (read from the item account):
//
//  1. No existing alert → open: mint the ALERT marker into st:open (from the
//     issuance pool, unguarded overdraft), mint the first OCC unit, and set the
//     descriptive metadata + status mirror. Created=true.
//  2. Existing OPEN alert → repeat: mint one OCC unit and refresh
//     last_seen/evidence. The marker already sits at st:open, so no move.
//  3. Existing ACK/RESOLVED alert → resurface/reopen: a GUARDED move of the
//     marker st:{from}→st:open (fails the batch if the marker is not there),
//     +OCC, status mirror back to OPEN. A reopen (from RESOLVED) also drops the
//     prior resolution/ack. Reopened=true only on RESOLVED→OPEN.
//
// The lifecycle marker is the guarded source-of-truth; the `status` metadata is
// a denormalised mirror for O(1) reads. Both are written in one atomic,
// idempotent batch, so they never diverge. The idempotency key is deterministic
// on (rule, fingerprint, period, evaluationID): a crash-replay of the same
// evaluation's write for the same fingerprint is deduplicated by the ledger.
//
// Unlike the Postgres store, this does NOT append a separate AlertEvent row: the
// ledger's ordered SAVED_METADATA / COMMITTED_TRANSACTION event stream for the
// control-ledger IS the append-only history (RFC §4.4), so OpenAlertResult.Event
// is left nil.
func (s *LedgerStore) OpenOrUpdateAlert(ctx context.Context, in storage.OpenAlertInput) (*storage.OpenAlertResult, error) {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}

	if in.PeriodID == "" {
		in.PeriodID = models.ContinuousPeriod
	}

	fpHash := schema.FingerprintHash(in.Fingerprint)
	itemAddr := schema.AlertItemAccount(in.RuleID.String(), in.PeriodID, fpHash)

	prior, err := s.readAlertItem(ctx, itemAddr)
	if err != nil {
		return nil, fmt.Errorf("open/update alert %s: %w", in.Fingerprint, err)
	}

	if prior == nil {
		return s.openNewAlert(ctx, in, fpHash, itemAddr)
	}

	return s.updateAlert(ctx, in, prior, fpHash, itemAddr)
}

// readAlertItem loads the current alert at its item address, or (nil, nil) when
// none exists (missing account or no metadata). Reads live state (checkpoint 0);
// the lifecycle write that follows re-derives the transition from it.
func (s *LedgerStore) readAlertItem(ctx context.Context, itemAddr string) (*models.Alert, error) {
	acct, err := s.client.GetAccount(ctx, s.controlLedger, itemAddr, 0)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}

		return nil, err
	}

	if len(acct.GetMetadata()) == 0 {
		return nil, nil
	}

	return alertFromAccount(acct)
}

// openNewAlert handles path 1 — the first fail for this (rule, fingerprint,
// period).
func (s *LedgerStore) openNewAlert(ctx context.Context, in storage.OpenAlertInput, fpHash, itemAddr string) (*storage.OpenAlertResult, error) {
	rule := in.RuleID.String()

	alert := &models.Alert{
		ID:               uuid.New(),
		RuleID:           in.RuleID,
		Fingerprint:      in.Fingerprint,
		PeriodID:         in.PeriodID,
		Status:           models.AlertOpen,
		Severity:         in.Severity,
		FirstSeenAt:      in.OccurredAt,
		LastSeenAt:       in.OccurredAt,
		OccurrenceCount:  1,
		LastEvaluationID: in.EvaluationID,
		Evidence:         in.Evidence,
		Labels:           in.Labels,
		CreatedAt:        in.OccurredAt,
	}

	md, err := alertToMetadata(alert)
	if err != nil {
		return nil, fmt.Errorf("open alert %s: %w", in.Fingerprint, err)
	}

	if err := s.client.CreateTransaction(ctx, ledger.CreateTransactionInput{
		Ledger:        s.controlLedger,
		ScriptName:    schema.NumscriptAlertOpen,
		ScriptVersion: schema.NumscriptVersion,
		Vars: map[string]string{
			schema.VarPool:   schema.PoolAccount(rule),
			schema.VarStOpen: schema.AlertStateAccount(schema.StateOpen, rule, in.PeriodID, fpHash),
			schema.VarItem:   itemAddr,
		},
		AccountMetadata: map[string]*commonpb.MetadataMap{itemAddr: {Values: md}},
		IdempotencyKey:  alertBatchKey(in),
	}); err != nil {
		return nil, fmt.Errorf("open alert %s: %w", in.Fingerprint, err)
	}

	return &storage.OpenAlertResult{Alert: alert, Created: true}, nil
}

// updateAlert handles paths 2 & 3 — an alert already exists for this
// (rule, fingerprint, period).
func (s *LedgerStore) updateAlert(ctx context.Context, in storage.OpenAlertInput, prior *models.Alert, fpHash, itemAddr string) (*storage.OpenAlertResult, error) {
	rule := in.RuleID.String()
	fromStatus := prior.Status
	reopened := fromStatus == models.AlertResolved

	// Mutate to the post-transition state. occurrence++, status back to OPEN,
	// fresh evidence/eval/seen. Severity and labels are intentionally NOT
	// refreshed — the case keeps the identity it opened with (matches Postgres).
	prior.OccurrenceCount++
	prior.Status = models.AlertOpen
	prior.LastSeenAt = in.OccurredAt
	prior.LastEvaluationID = in.EvaluationID
	prior.Evidence = in.Evidence

	var deletes map[string][]string
	if reopened {
		// A reopen drops the prior closure — its audit lives in the event
		// stream. Delete only keys actually present (the ledger rejects deleting
		// an absent key).
		if keys := presentClosureKeys(prior); len(keys) > 0 {
			deletes = map[string][]string{itemAddr: keys}
		}

		prior.Resolution = nil
		prior.Ack = nil
	}

	md, err := alertToMetadata(prior)
	if err != nil {
		return nil, fmt.Errorf("update alert %s: %w", in.Fingerprint, err)
	}

	pool := schema.PoolAccount(rule)
	tx := ledger.CreateTransactionInput{
		Ledger:          s.controlLedger,
		ScriptVersion:   schema.NumscriptVersion,
		AccountMetadata: map[string]*commonpb.MetadataMap{itemAddr: {Values: md}},
		DeleteMetadata:  deletes,
		IdempotencyKey:  alertBatchKey(in),
	}

	if fromStatus == models.AlertOpen {
		// Marker already at st:open — a plain repeat, no move.
		tx.ScriptName = schema.NumscriptAlertBump
		tx.Vars = map[string]string{
			schema.VarPool: pool,
			schema.VarItem: itemAddr,
		}
	} else {
		// Resurface (ack→open) or reopen (resolved→open): guarded marker move.
		tx.ScriptName = schema.NumscriptAlertReopen
		tx.Vars = map[string]string{
			schema.VarPool:   pool,
			schema.VarStFrom: schema.AlertStateAccount(statusToState(fromStatus), rule, in.PeriodID, fpHash),
			schema.VarStOpen: schema.AlertStateAccount(schema.StateOpen, rule, in.PeriodID, fpHash),
			schema.VarItem:   itemAddr,
		}
	}

	if err := s.client.CreateTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("update alert %s: %w", in.Fingerprint, err)
	}

	return &storage.OpenAlertResult{Alert: prior, Created: false, Reopened: reopened}, nil
}

// presentClosureKeys returns the resolution/ack metadata keys currently set on
// the alert, so a reopen deletes only what exists.
func presentClosureKeys(a *models.Alert) []string {
	var keys []string
	if a.Resolution != nil {
		keys = append(keys, schema.MetaResolution)
	}

	if a.Ack != nil {
		keys = append(keys, schema.MetaAck)
	}

	return keys
}

// alertBatchKey derives the idempotency key for an OpenOrUpdateAlert batch,
// deterministic on (ruleID, fingerprint, periodID, evaluationID).
func alertBatchKey(in storage.OpenAlertInput) string {
	return alertActionKey("openorupdate", in.RuleID.String(), in.Fingerprint, in.PeriodID, in.EvaluationID.String())
}

// alertActionKey builds a batch idempotency key from length-prefixed parts (so a
// component containing the separator can't collide with a different split). The
// leading action discriminator keeps distinct operations on the same entity
// (open-or-update vs auto-resolve vs ack/resolve) from sharing a key — which,
// given the ledger's content-sensitive idempotency (F17), would otherwise
// conflict.
func alertActionKey(parts ...string) string {
	for i, p := range parts {
		parts[i] = fmt.Sprintf("%d:%s", len(p), p)
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))

	return hex.EncodeToString(sum[:])
}
