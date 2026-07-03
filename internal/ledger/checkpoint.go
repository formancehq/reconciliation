package ledger

import "context"

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

// AcquireCheckpoint creates a fresh query checkpoint. The caller owns its
// lifecycle — pair every Acquire with a Release (typically deferred).
func (c *Client) AcquireCheckpoint(ctx context.Context) (*Checkpoint, error) {
	id, maxSeq, err := c.CreateQueryCheckpoint(ctx)
	if err != nil {
		return nil, err
	}

	return &Checkpoint{ID: id, MaxSequence: maxSeq, client: c}, nil
}

// Release deletes the checkpoint (idempotent — a double-release is a no-op).
// Prefer a context that outlives the evaluation's cancellation so cleanup still
// runs on abort (otherwise a cancelled ctx leaks the checkpoint).
func (cp *Checkpoint) Release(ctx context.Context) error {
	return cp.client.DeleteQueryCheckpoint(ctx, cp.ID)
}
