package ledger

import (
	"context"
	"fmt"
	"time"

	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
)

// checkpointReadyProbeInterval / checkpointReadyProbeAttempts bound the wait for
// a freshly-created checkpoint's read index to materialize (see AcquireCheckpoint).
const (
	checkpointReadyProbeInterval = 100 * time.Millisecond
	checkpointReadyProbeAttempts = 50 // ~5s ceiling
)

// Checkpoint is an acquired query checkpoint — a globally consistent cross-ledger
// snapshot (all ledgers frozen at MaxSequence). It is the anchor for reading
// ledgers A and B at the same instant (ADR-002). Checkpoints are NOT
// auto-cleaned and a live one pins SSTs from compaction, so the acquirer MUST
// Release it (create → use → delete).
type Checkpoint struct {
	ID          uint64
	MaxSequence uint64

	client         *Client
	registryLedger string // where this checkpoint's registry entry lives (for Release)
}

// AcquireCheckpoint creates a fresh query checkpoint and waits until it is
// readable, then returns it. The caller owns its lifecycle — pair every Acquire
// with a Release (typically deferred).
//
// controlLedger serves two roles: the readiness probe target and the registry
// ledger. Both only need it to exist — the control ledger is the natural choice.
//
// Readiness wait matters: CreateQueryCheckpoint commits the checkpoint id via
// Raft immediately, but the ledger's index-builder materializes the checkpoint's
// read index *asynchronously* afterwards. A read in that window fails with a
// non-retryable gRPC Unknown (the server logs "opening checkpoint read index …
// does not exist" but returns only "unknown server error", so the client cannot
// distinguish it to retry at the read site). We probe controlLedger at the new
// checkpoint until a read succeeds, so callers get a usable snapshot. [F32]
func (c *Client) AcquireCheckpoint(ctx context.Context, controlLedger string) (*Checkpoint, error) {
	id, maxSeq, err := c.CreateQueryCheckpoint(ctx)
	if err != nil {
		return nil, err
	}

	// Register the live checkpoint so a crash before Release can be reaped (F26).
	// Best-effort: a missed record only risks one un-reaped orphan on a crash.
	_ = c.recordCheckpoint(ctx, controlLedger, id, time.Now())

	if err := c.waitCheckpointReady(ctx, controlLedger, id); err != nil {
		// Don't leave a checkpoint we can't use (it pins SSTs); best-effort cleanup.
		_ = c.DeleteQueryCheckpoint(ctx, id)
		_ = c.forgetCheckpoint(ctx, controlLedger, id)
		return nil, fmt.Errorf("checkpoint %d not readable: %w", id, err)
	}

	return &Checkpoint{ID: id, MaxSequence: maxSeq, client: c, registryLedger: controlLedger}, nil
}

// waitCheckpointReady polls a cheap aggregate at the checkpoint until it
// succeeds, signalling the read index has been materialized. A bounded loop —
// on exhaustion it returns the last probe error so acquisition fails loudly
// rather than handing back an unusable checkpoint.
func (c *Client) waitCheckpointReady(ctx context.Context, probeLedger string, checkpointID uint64) error {
	probe := schema.FilterAddressExact("world") // exists on every ledger; aggregate is NotFound-free

	var lastErr error
	for attempt := 0; attempt < checkpointReadyProbeAttempts; attempt++ {
		if _, err := c.AggregateVolumes(ctx, probeLedger, probe, checkpointID); err == nil {
			return nil
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(checkpointReadyProbeInterval):
		}
	}

	return lastErr
}

// Release deletes the checkpoint (idempotent — a double-release is a no-op) and
// clears its registry entry. Prefer a context that outlives the evaluation's
// cancellation so cleanup still runs on abort (otherwise a cancelled ctx leaks
// the checkpoint). If the checkpoint delete fails, the registry entry is kept so
// the reaper retries it later.
func (cp *Checkpoint) Release(ctx context.Context) error {
	if err := cp.client.DeleteQueryCheckpoint(ctx, cp.ID); err != nil {
		return err
	}

	_ = cp.client.forgetCheckpoint(ctx, cp.registryLedger, cp.ID)

	return nil
}
