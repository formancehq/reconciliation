package ledger

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Checkpoints are not auto-cleaned and a live one pins SSTs (ADR-002 §7), so a
// crash between Acquire and Release leaks disk. Reconciliation can't enumerate
// cluster checkpoints (ListQueryCheckpoints is ClusterService, the client wraps
// BucketService) and the cluster is shared, so it must only reap checkpoints it
// created — tracked in a small registry on the control ledger, reaped by age.
const (
	// checkpointRegistryAccount holds one metadata key per checkpoint recon owns.
	// A bookkeeping-only account (no postings), permitted under the control
	// ledger's AUDIT chart enforcement; the dynamic cp:<id> keys are stored as-is
	// (undeclared metadata, like the alert labels).
	checkpointRegistryAccount = "internal:checkpoints"
	// checkpointKeyPrefix namespaces the per-checkpoint keys: cp:<id> -> unix seconds.
	checkpointKeyPrefix = "cp:"
)

func checkpointKey(id uint64) string {
	return checkpointKeyPrefix + strconv.FormatUint(id, 10)
}

// recordCheckpoint notes a live checkpoint in the control-ledger registry so a
// crash before Release can be reaped. Best-effort at the call site: a missed
// record only risks one un-reaped orphan on a crash.
func (c *Client) recordCheckpoint(ctx context.Context, registryLedger string, id uint64, at time.Time) error {
	return c.SaveAccountMetadata(ctx, registryLedger, checkpointRegistryAccount,
		map[string]string{checkpointKey(id): strconv.FormatInt(at.Unix(), 10)})
}

// forgetCheckpoint removes a checkpoint's registry entry once it is released.
func (c *Client) forgetCheckpoint(ctx context.Context, registryLedger string, id uint64) error {
	return c.DeleteAccountMetadata(ctx, registryLedger, checkpointRegistryAccount, checkpointKey(id))
}

// ReapOrphanedCheckpoints deletes checkpoints recorded in the registry older than
// olderThan — i.e. orphans a crash left behind. A live evaluation holds a
// checkpoint for at most its wall-clock budget (≪ olderThan), so this never reaps
// an in-flight checkpoint, even one held by another instance sharing the control
// ledger. Returns the number reaped. Best-effort per entry: a delete failure
// leaves the entry for the next run. Safe to call at startup before serving.
func (c *Client) ReapOrphanedCheckpoints(ctx context.Context, registryLedger string, olderThan time.Duration, now time.Time) (int, error) {
	acct, err := c.GetAccount(ctx, registryLedger, checkpointRegistryAccount, 0)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return 0, nil // registry never created → nothing to reap
		}

		return 0, fmt.Errorf("read checkpoint registry: %w", err)
	}

	cutoff := now.Add(-olderThan).Unix()
	reaped := 0
	var forget []string

	for key, recordedAt := range commonpb.MetadataToMap(acct.GetMetadata()) {
		if !strings.HasPrefix(key, checkpointKeyPrefix) {
			continue
		}

		id, err := strconv.ParseUint(strings.TrimPrefix(key, checkpointKeyPrefix), 10, 64)
		if err != nil {
			continue // malformed key — skip defensively
		}

		ts, err := strconv.ParseInt(recordedAt, 10, 64)
		if err != nil || ts > cutoff {
			continue // unparseable, or still within the live window
		}

		if derr := c.DeleteQueryCheckpoint(ctx, id); derr != nil {
			continue // leave the entry; retry next run
		}

		forget = append(forget, key)
		reaped++
	}

	if len(forget) > 0 {
		if err := c.DeleteAccountMetadata(ctx, registryLedger, checkpointRegistryAccount, forget...); err != nil {
			return reaped, fmt.Errorf("clear reaped registry entries: %w", err)
		}
	}

	return reaped, nil
}
