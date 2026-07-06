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

	client *Client
}

// AcquireCheckpoint creates a fresh query checkpoint and waits until it is
// readable, then returns it. The caller owns its lifecycle — pair every Acquire
// with a Release (typically deferred).
//
// The wait matters: CreateQueryCheckpoint commits the checkpoint id via Raft
// immediately, but the ledger's index-builder materializes the checkpoint's
// read index *asynchronously* afterwards. A read in that window fails with a
// non-retryable gRPC Unknown (the server logs "opening checkpoint read index …
// does not exist" but returns only "unknown server error", so the client cannot
// distinguish it to retry at the read site). We probe probeLedger at the new
// checkpoint until a read succeeds, so callers get a snapshot that is actually
// usable. probeLedger only needs to exist (the read index is global to the
// checkpoint) — the control ledger is the natural choice. [F32]
func (c *Client) AcquireCheckpoint(ctx context.Context, probeLedger string) (*Checkpoint, error) {
	id, maxSeq, err := c.CreateQueryCheckpoint(ctx)
	if err != nil {
		return nil, err
	}

	if err := c.waitCheckpointReady(ctx, probeLedger, id); err != nil {
		// Don't leak a checkpoint we can't use (it pins SSTs); best-effort delete.
		_ = c.DeleteQueryCheckpoint(ctx, id)
		return nil, fmt.Errorf("checkpoint %d not readable: %w", id, err)
	}

	return &Checkpoint{ID: id, MaxSequence: maxSeq, client: c}, nil
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

// Release deletes the checkpoint (idempotent — a double-release is a no-op).
// Prefer a context that outlives the evaluation's cancellation so cleanup still
// runs on abort (otherwise a cancelled ctx leaks the checkpoint).
func (cp *Checkpoint) Release(ctx context.Context) error {
	return cp.client.DeleteQueryCheckpoint(ctx, cp.ID)
}
