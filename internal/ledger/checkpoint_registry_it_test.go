//go:build it

package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestIntegration_ReapOrphanedCheckpoints proves the F26 reaper: a checkpoint
// whose registry entry is older than the threshold (a crash orphan) is deleted
// and forgotten, while a fresh entry (a live evaluation's checkpoint) is left
// untouched.
//
//	go test -tags it -run TestIntegration_ReapOrphanedCheckpoints ./internal/ledger/...
func TestIntegration_ReapOrphanedCheckpoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	// Fresh, bare control ledger so the registry is isolated from other runs.
	control := "recon-it-reap-" + uuid.NewString()
	require.NoError(t, client.CreateLedger(ctx, control, nil, nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))

	now := time.Now()

	// A real checkpoint recorded as an OLD orphan (beyond the reap threshold).
	orphanID, _, err := client.CreateQueryCheckpoint(ctx)
	require.NoError(t, err)
	require.NoError(t, client.recordCheckpoint(ctx, control, orphanID, now.Add(-1*time.Hour)))

	// A FRESH entry (stands in for a live evaluation's checkpoint) — must survive.
	const freshID = uint64(999_000_001)
	require.NoError(t, client.recordCheckpoint(ctx, control, freshID, now))

	reaped, err := client.ReapOrphanedCheckpoints(ctx, control, 15*time.Minute, now)
	require.NoError(t, err)
	require.Equal(t, 1, reaped, "only the aged orphan is reaped")

	md := commonpb.MetadataToMap(mustGetAccount(ctx, t, client, control).GetMetadata())
	require.NotContains(t, md, checkpointKey(orphanID), "orphan entry cleared")
	require.Contains(t, md, checkpointKey(freshID), "fresh entry retained")

	// Re-reaping is a no-op (the orphan is already gone; the fresh one still young).
	reaped, err = client.ReapOrphanedCheckpoints(ctx, control, 15*time.Minute, now)
	require.NoError(t, err)
	require.Zero(t, reaped)

	// Cleanup the fresh fake entry + its (never-created) checkpoint is a no-op.
	require.NoError(t, client.forgetCheckpoint(ctx, control, freshID))
}

func mustGetAccount(ctx context.Context, t *testing.T, c *Client, ledgerName string) *commonpb.Account {
	t.Helper()

	acct, err := c.GetAccount(ctx, ledgerName, checkpointRegistryAccount, 0)
	require.NoError(t, err)

	return acct
}
