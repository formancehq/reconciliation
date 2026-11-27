package tick_test

import (
	"context"
	"testing"

	"github.com/formancehq/reconciliation/internal/controlstate"
	"github.com/formancehq/reconciliation/internal/controlstate/memstate"
	"github.com/formancehq/reconciliation/internal/controlstate/tick"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// A plan that does not record the tick on the sequence could capture the same
// tick twice, so Commit refuses it before writing.
func TestCommit_RefusesAPlanWithoutTheTick(t *testing.T) {
	s := memstate.New()
	seq := controlstate.Key{Kind: "eval", ID: uuid.NewString()}
	_, err := s.Commit(t.Context(), controlstate.UnitOfWork{
		IdempotencyKey: uuid.NewString(),
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(seq)},
		Put:            []controlstate.Write{{Key: seq, Fields: map[string]string{"last": "created"}}},
	})
	require.NoError(t, err)

	noTick := func(_ context.Context, e controlstate.Entry) (controlstate.UnitOfWork, error) {
		return controlstate.UnitOfWork{
			Expect: []controlstate.Precondition{controlstate.Expect(e)},
			Put:    []controlstate.Write{{Key: e.Key, Fields: map[string]string{"last": "capture"}}},
		}, nil
	}
	_, err = tick.Commit(t.Context(), s, seq, "2026-11-27T15:15:00Z", "replica-a", noTick, 3)
	require.ErrorIs(t, err, controlstate.ErrInvalid)

	_, err = tick.Commit(t.Context(), s, seq, "", "replica-a", noTick, 3)
	require.ErrorIs(t, err, controlstate.ErrInvalid)
}
