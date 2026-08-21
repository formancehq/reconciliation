package worker

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/formancehq/reconciliation/internal/storage"
)

// closingCheckInterval is how often the schedule is re-read and the next fire
// time recomputed. A minute is enough for cron's finest supported granularity,
// and re-reading rather than caching is what makes the cadence changeable at
// runtime: an operator who moves from monthly to weekly sees it take effect
// without a restart.
const closingCheckInterval = time.Minute

// closingLoop rotates closures on the configured cron.
//
// Rotation is a convenience, not the mechanism: closing by hand through
// POST /closures does exactly the same thing. What the schedule adds is that
// "the books are closed every month" stops depending on somebody remembering.
//
// Changing the schedule mid-flight only makes the next closure longer or
// shorter. Closures already closed are unaffected — which is the property that
// makes the cadence safe to change at all, and the reason this is a stored
// setting rather than a boot flag.
func (w *Worker) closingLoop(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(closingCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.rotateClosureIfDue(ctx, time.Now().UTC()); err != nil {
				w.logger.Errorf("closing schedule: %s", err)
			}
		}
	}
}

// rotateClosureIfDue closes the journal when the cron says a closing was due
// since the open closure was opened.
//
// The "since it was opened" test is what makes this safe with several workers
// running and with a worker that was down when the cron should have fired. Two
// workers racing both see the same due closing; the first commits, the second
// then reads a closure opened after the fire time and does nothing. A worker
// starting late still closes, once, rather than skipping the period entirely —
// a missed closing is the failure that matters here, since a period nobody
// closed is a period nobody attested.
func (w *Worker) rotateClosureIfDue(ctx context.Context, now time.Time) error {
	spec, err := w.store.GetClosingSchedule(ctx)
	if err != nil {
		return err
	}
	if spec == "" {
		return nil
	}
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		// Stored schedules are validated before they are written, so this means
		// the row was edited underneath us. Reported rather than fatal: a bad
		// schedule must not take the worker down with it.
		return err
	}

	current, err := w.store.CurrentClosure(ctx)
	if err != nil {
		return err
	}
	next := schedule.Next(current.OpenedAt)
	if next.After(now) {
		return nil
	}

	closed, err := w.closeJournal(ctx, now)
	if err != nil {
		return err
	}
	w.logger.Infof(
		"closed the journal on schedule: closure %d covering sequences %d..%d, %d periods",
		closed.ID, closed.FirstSequence, derefSequence(closed.LastSequence), closed.Periods)
	return nil
}

// closeJournal stamps the closing with the instant rotation decided, not with a
// later wall-clock read. The successor opens at that same instant, which is what
// makes the next due test exact: a second worker on the same tick sees a closure
// opened at the fire time and finds nothing due.
func (w *Worker) closeJournal(ctx context.Context, at time.Time) (*closedClosure, error) {
	var out closedClosure
	err := w.store.RunInTx(ctx, func(ctx context.Context, tx *storage.Storage) error {
		// No subject: a scheduled closing was not made by a person, and saying so
		// is the point. The subject snapshot encodes that as a distinct source
		// tag, so "no human closed these books" is a cryptographic claim rather
		// than an absence anyone could later fill in.
		closure, err := tx.CloseCurrentClosure(ctx, storage.CloseClosureInput{At: at})
		if err != nil {
			return err
		}
		out = closedClosure{
			ID:            closure.ID,
			FirstSequence: closure.FirstSequence,
			LastSequence:  closure.LastSequence,
			Periods:       len(closure.Periods),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// closedClosure is the little of a closure the log line needs, so the worker
// does not hold a full model just to format one message.
type closedClosure struct {
	ID            int64
	FirstSequence int64
	LastSequence  *int64
	Periods       int
}

func derefSequence(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
