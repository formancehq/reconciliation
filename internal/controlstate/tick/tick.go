// Package tick commits the capture of a scheduled evaluation at most once per
// tick, whatever the number of schedulers.
//
// RFC 0022 (Unresolved Question 8) keeps the scheduler Lease as an
// optimization only. Correctness comes from the rule's sequence: a capture
// claims its next version, so when two replicas run the same tick, one commits
// and the other gets ErrConflict. The loser re-reads the sequence and stops
// when the last capture already carries its tick, instead of re-planning a
// second capture for it.
package tick

import (
	"context"
	"errors"
	"fmt"

	"github.com/formancehq/reconciliation/internal/controlstate"
)

// Field is the sequence field that holds the tick of its last scheduled
// capture. Ticks are UTC instants in RFC 3339 with a fixed width, so they
// compare as strings.
const Field = "lastTick"

// Outcome tells what Commit did.
type Outcome int

const (
	// Committed means this caller committed the capture of the tick.
	Committed Outcome = iota + 1
	// AlreadyDone means a capture for this tick, or a later one, was already
	// committed, by another scheduler or an earlier attempt. Nothing was written.
	AlreadyDone
)

func (o Outcome) String() string {
	switch o {
	case Committed:
		return "committed"
	case AlreadyDone:
		return "already done"
	}
	return fmt.Sprintf("Outcome(%d)", int(o))
}

// Plan builds the capture of a tick from the sequence as loaded: it must
// expect that entry and write it with Field set to the tick. It runs again on
// every attempt, so it reads the sources again after a conflict.
type Plan func(ctx context.Context, seq controlstate.Entry) (controlstate.UnitOfWork, error)

// ErrExhausted means the sequence kept moving for other reasons than this tick
// (rule changes, on-demand runs) for every attempt.
var ErrExhausted = errors.New("tick: sequence kept changing")

// Commit commits the capture of tick on the sequence seq at most once.
// instance names the scheduler replica: it goes into the idempotency key with
// the tick and the version read, so two replicas never share a key and a
// replica's retry of the same attempt replays its first outcome.
func Commit(ctx context.Context, cs controlstate.ControlState, seq controlstate.Key, tick, instance string, plan Plan, maxAttempts int) (Outcome, error) {
	if tick == "" || instance == "" {
		return 0, fmt.Errorf("%w: tick and instance are required", controlstate.ErrInvalid)
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		snap, err := cs.Load(ctx, seq)
		if err != nil {
			return 0, err
		}
		cur := snap.Get(seq)
		if last := cur.Fields[Field]; last != "" && last >= tick {
			return AlreadyDone, nil
		}
		uow, err := plan(ctx, cur)
		if err != nil {
			return 0, err
		}
		if err := checkPlan(uow, cur, tick); err != nil {
			return 0, err
		}
		uow.IdempotencyKey = fmt.Sprintf("tick:%s:%s:%s:v%d", seq.ID, tick, instance, cur.Version)
		_, err = cs.Commit(ctx, uow)
		switch {
		case err == nil:
			return Committed, nil
		case errors.Is(err, controlstate.ErrConflict):
			continue // re-read: another tick's capture or a rule change took the version
		default:
			return 0, err
		}
	}
	return 0, ErrExhausted
}

// checkPlan makes sure the capture claims the sequence as loaded and records
// the tick, which is what makes a second capture of the same tick impossible.
func checkPlan(uow controlstate.UnitOfWork, cur controlstate.Entry, tick string) error {
	expected := false
	for _, p := range uow.Expect {
		if p.Key == cur.Key {
			if p.Version != cur.Version {
				return fmt.Errorf("%w: the plan expects %s at v%d, loaded v%d", controlstate.ErrInvalid, cur.Key, p.Version, cur.Version)
			}
			expected = true
		}
	}
	for _, w := range uow.Put {
		if w.Key == cur.Key {
			if !expected {
				break
			}
			if w.Fields[Field] != tick {
				return fmt.Errorf("%w: the plan writes %s without %s=%s", controlstate.ErrInvalid, cur.Key, Field, tick)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: the plan must expect and write %s", controlstate.ErrInvalid, cur.Key)
}
