//go:build it

package worker

import (
	"context"
	"testing"
	"time"

	logging "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/stretchr/testify/require"
)

func newClosingWorker(t *testing.T) *Worker {
	t.Helper()
	store, _ := newWorkerStore(t)
	return &Worker{store: store, logger: logging.Testing()}
}

// Rotation is a convenience, not the mechanism, so it must do nothing at all
// until a schedule says otherwise. An installation that never configures one
// closes by hand and should never find a closure it did not ask for.
func TestRotationDoesNothingWithoutASchedule(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	w := newClosingWorker(t)

	before, err := w.store.CurrentClosure(ctx)
	require.NoError(t, err)

	require.NoError(t, w.rotateClosureIfDue(ctx, time.Now().UTC().Add(365*24*time.Hour)))

	after, err := w.store.CurrentClosure(ctx)
	require.NoError(t, err)
	require.Equal(t, before.ID, after.ID, "no schedule means no rotation")
}

// The due test is "has a closing fallen due since the open closure was opened",
// which is what makes rotation both idempotent and catch-up safe.
func TestRotationClosesOnlyWhenDue(t *testing.T) {
	t.Parallel()
	ctx := logging.TestingContext()
	w := newClosingWorker(t)

	require.NoError(t, w.store.SetClosingSchedule(ctx, "0 0 1 * *")) // monthly

	opened, err := w.store.CurrentClosure(ctx)
	require.NoError(t, err)

	// A minute later, nothing is due.
	require.NoError(t, w.rotateClosureIfDue(ctx, opened.OpenedAt.Add(time.Minute)))
	still, err := w.store.CurrentClosure(ctx)
	require.NoError(t, err)
	require.Equal(t, opened.ID, still.ID)

	// Two months later it plainly is, and the closure rotates exactly once.
	require.NoError(t, w.rotateClosureIfDue(ctx, opened.OpenedAt.AddDate(0, 2, 0)))
	rotated, err := w.store.CurrentClosure(ctx)
	require.NoError(t, err)
	require.NotEqual(t, opened.ID, rotated.ID, "a due closing must rotate")

	// Running again immediately must not rotate a second time: the successor was
	// opened after the fire time, so nothing is due for it. This is what keeps
	// two workers racing on the same tick from closing twice.
	require.NoError(t, w.rotateClosureIfDue(ctx, opened.OpenedAt.AddDate(0, 2, 0)))
	settled, err := w.store.CurrentClosure(ctx)
	require.NoError(t, err)
	require.Equal(t, rotated.ID, settled.ID)
}

// A schedule that does not parse must be reported, not fatal: a bad row would
// otherwise take the worker down with it, turning a configuration mistake into
// an outage of every other loop.
func TestRotationReportsAnUnparseableSchedule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newClosingWorker(t)

	// Written straight to storage, bypassing the service validation, which is the
	// only way this state can arise.
	require.NoError(t, w.store.SetClosingSchedule(ctx, "not a cron"))
	require.Error(t, w.rotateClosureIfDue(ctx, time.Now().UTC()))
}
