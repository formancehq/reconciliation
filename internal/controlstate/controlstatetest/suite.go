// Package controlstatetest is the contract suite every controlstate adapter
// must pass: the in-memory reference adapter, the Ledger v3 adapter, and a
// future Ledger key-value adapter.
//
// The suite replays, at the port level, the races reproduced against a live
// ledger in the 2026-10-07 review:
//
//   - a repeat failure that overwrites an acknowledgement (StaleVersion and
//     GuardWithoutWrite);
//   - two evaluations that open the same alert twice (CreateIfAbsent and
//     ConcurrentCreates);
//   - overlapping evaluations of one rule (ConcurrentEvaluationsOfOneRule).
//
// The third reproduced defect, an accepted alert that reopens on the next
// failing run, is planner logic, not storage: the evaluation planner tests
// cover it.
package controlstatetest

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/formancehq/reconciliation/internal/controlstate"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Store is the adapter under test, plus the one observation the port does
// not offer: the records committed about an entity.
type Store interface {
	controlstate.ControlState
	// Records returns the records committed about subject, oldest first.
	Records(ctx context.Context, subject controlstate.Key) ([]controlstate.Record, error)
}

// Run runs the contract. newStore is called once per subtest. It may return
// a shared store: every subtest uses its own random keys.
func Run(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, s Store)
	}{
		{"LoadMissingKeyIsAbsent", testLoadMissingKeyIsAbsent},
		{"CreateIfAbsent", testCreateIfAbsent},
		{"ConcurrentCreates", testConcurrentCreates},
		{"StaleVersion", testStaleVersion},
		{"GuardWithoutWrite", testGuardWithoutWrite},
		{"FailedCommitWritesNothing", testFailedCommitWritesNothing},
		{"RecordsLandWithCommit", testRecordsLandWithCommit},
		{"ConcurrentEvaluationsOfOneRule", testConcurrentEvaluationsOfOneRule},
		{"RetryAfterConflictWithNewKey", testRetryAfterConflictWithNewKey},
		{"RejectedKeyStaysConsumed", testRejectedKeyStaysConsumed},
		{"ReplayReturnsFirstOutcome", testReplayReturnsFirstOutcome},
		{"KeyReusedWithDifferentContent", testKeyReusedWithDifferentContent},
		{"InvalidUnitOfWork", testInvalidUnitOfWork},
		{"LoadReturnsCopies", testLoadReturnsCopies},
		{"CommitReturnsNewVersions", testCommitReturnsNewVersions},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newStore(t)) })
	}
}

func newKey(kind string) controlstate.Key {
	return controlstate.Key{Kind: kind, ID: uuid.NewString()}
}

func newIdemKey() string { return "contract-" + uuid.NewString() }

func load(t *testing.T, s Store, k controlstate.Key) controlstate.Entry {
	t.Helper()
	snap, err := s.Load(t.Context(), k)
	require.NoError(t, err)
	return snap.Get(k)
}

// commitErr drops the versions for scenarios that only check the outcome.
func commitErr(ctx context.Context, s Store, uow controlstate.UnitOfWork) error {
	_, err := s.Commit(ctx, uow)
	return err
}

func records(t *testing.T, s Store, subject controlstate.Key) []controlstate.Record {
	t.Helper()
	rs, err := s.Records(t.Context(), subject)
	require.NoError(t, err)
	return rs
}

// create commits a new entity and returns it at version 1.
func create(t *testing.T, s Store, k controlstate.Key, fields map[string]string) controlstate.Entry {
	t.Helper()
	got, err := s.Commit(t.Context(), controlstate.UnitOfWork{
		IdempotencyKey: newIdemKey(),
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(k)},
		Put:            []controlstate.Write{{Key: k, Fields: fields}},
	})
	require.NoError(t, err)
	require.Equal(t, controlstate.Versions{k: 1}, got)
	e := load(t, s, k)
	require.Equal(t, controlstate.Version(1), e.Version)
	return e
}

// replace commits new fields over the loaded entry.
func replace(e controlstate.Entry, fields map[string]string) controlstate.UnitOfWork {
	return controlstate.UnitOfWork{
		IdempotencyKey: newIdemKey(),
		Expect:         []controlstate.Precondition{controlstate.Expect(e)},
		Put:            []controlstate.Write{{Key: e.Key, Fields: fields}},
	}
}

func testLoadMissingKeyIsAbsent(t *testing.T, s Store) {
	k := newKey("alert")
	snap, err := s.Load(t.Context(), k)
	require.NoError(t, err)
	e := snap.Get(k)
	assert.False(t, e.Exists())
	assert.Equal(t, controlstate.Absent, e.Version)
	assert.Nil(t, e.Fields)

	empty, err := s.Load(t.Context())
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// Race: two evaluations of one rule both see "no alert" and both open it.
func testCreateIfAbsent(t *testing.T, s Store) {
	k := newKey("alert")
	create(t, s, k, map[string]string{"status": "OPEN", "by": "first"})

	err := commitErr(t.Context(), s, controlstate.UnitOfWork{
		IdempotencyKey: newIdemKey(),
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(k)},
		Put:            []controlstate.Write{{Key: k, Fields: map[string]string{"status": "OPEN", "by": "second"}}},
	})
	require.ErrorIs(t, err, controlstate.ErrConflict)

	e := load(t, s, k)
	assert.Equal(t, controlstate.Version(1), e.Version)
	assert.Equal(t, "first", e.Fields["by"])
}

func testConcurrentCreates(t *testing.T, s Store) {
	const writers = 8
	k := newKey("alert")
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			errs[i] = commitErr(context.WithoutCancel(t.Context()), s, controlstate.UnitOfWork{
				IdempotencyKey: newIdemKey(),
				Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(k)},
				Put:            []controlstate.Write{{Key: k, Fields: map[string]string{"status": "OPEN"}}},
				Records:        []controlstate.Record{{Kind: "activity", Subject: k, Fields: map[string]string{"event": "opened"}}},
			})
		})
	}
	wg.Wait()

	assertOneWinner(t, errs)
	assert.Equal(t, controlstate.Version(1), load(t, s, k).Version)
	assert.Len(t, records(t, s, k), 1, "only the winning open leaves a record")
}

// Race: an evaluation reads OPEN, an operator acknowledges, then the
// evaluation writes its repeat failure over the acknowledgement.
func testStaleVersion(t *testing.T, s Store) {
	k := newKey("alert")
	readByEvaluation := create(t, s, k, map[string]string{"status": "OPEN", "occurrences": "1"})

	readByOperator := load(t, s, k)
	require.NoError(t, commitErr(t.Context(), s, replace(readByOperator, map[string]string{"status": "ACK", "occurrences": "1"})))

	err := commitErr(t.Context(), s, replace(readByEvaluation, map[string]string{"status": "OPEN", "occurrences": "2"}))
	require.ErrorIs(t, err, controlstate.ErrConflict)

	e := load(t, s, k)
	assert.Equal(t, controlstate.Version(2), e.Version)
	assert.Equal(t, "ACK", e.Fields["status"], "the acknowledgement survives")
}

// A precondition with no write on its key guards the state a unit of work
// depends on without changing it, like the Numscript kept guard.
func testGuardWithoutWrite(t *testing.T, s Store) {
	alert := newKey("alert")
	token := newKey("token")
	readAlert := create(t, s, alert, map[string]string{"status": "OPEN"})
	readToken := create(t, s, token, map[string]string{"run": "1"})

	require.NoError(t, commitErr(t.Context(), s, replace(readAlert, map[string]string{"status": "ACK"})))

	err := commitErr(t.Context(), s, controlstate.UnitOfWork{
		IdempotencyKey: newIdemKey(),
		Expect:         []controlstate.Precondition{controlstate.Expect(readAlert), controlstate.Expect(readToken)},
		Put:            []controlstate.Write{{Key: token, Fields: map[string]string{"run": "2"}}},
	})
	require.ErrorIs(t, err, controlstate.ErrConflict)
	assert.Equal(t, controlstate.Version(1), load(t, s, token).Version, "the guarded write did not land")
}

func testFailedCommitWritesNothing(t *testing.T, s Store) {
	rule := newKey("rule")
	alert := newKey("alert")
	readRule := create(t, s, rule, map[string]string{"name": "before"})
	readAlert := create(t, s, alert, map[string]string{"status": "OPEN"})
	require.NoError(t, commitErr(t.Context(), s, replace(readAlert, map[string]string{"status": "ACK"})))

	err := commitErr(t.Context(), s, controlstate.UnitOfWork{
		IdempotencyKey: newIdemKey(),
		Expect:         []controlstate.Precondition{controlstate.Expect(readRule), controlstate.Expect(readAlert)},
		Put: []controlstate.Write{
			{Key: rule, Fields: map[string]string{"name": "after"}},
			{Key: alert, Fields: map[string]string{"status": "RESOLVED"}},
		},
		Records: []controlstate.Record{{Kind: "capture", Subject: rule, Fields: map[string]string{"verdict": "pass"}}},
	})
	require.ErrorIs(t, err, controlstate.ErrConflict)

	r := load(t, s, rule)
	assert.Equal(t, controlstate.Version(1), r.Version)
	assert.Equal(t, "before", r.Fields["name"])
	assert.Equal(t, "ACK", load(t, s, alert).Fields["status"])
	assert.Empty(t, records(t, s, rule), "no record from a failed commit")
}

func testRecordsLandWithCommit(t *testing.T, s Store) {
	rule := newKey("rule")
	e := create(t, s, rule, map[string]string{"name": "r"})

	for i, verdict := range []string{"fail", "pass"} {
		require.NoError(t, commitErr(t.Context(), s, controlstate.UnitOfWork{
			IdempotencyKey: newIdemKey(),
			Expect:         []controlstate.Precondition{controlstate.Expect(e)},
			Put:            []controlstate.Write{{Key: rule, Fields: map[string]string{"name": "r", "runs": strconv.Itoa(i + 1)}}},
			Records:        []controlstate.Record{{Kind: "capture", Subject: rule, Fields: map[string]string{"verdict": verdict}}},
		}))
		e = load(t, s, rule)
	}

	rs := records(t, s, rule)
	require.Len(t, rs, 2)
	assert.Equal(t, "capture", rs[0].Kind)
	assert.Equal(t, rule, rs[0].Subject)
	assert.Equal(t, "fail", rs[0].Fields["verdict"], "oldest first")
	assert.Equal(t, "pass", rs[1].Fields["verdict"])
	assert.Empty(t, records(t, s, newKey("rule")))
}

// Race: a scheduled run and a manual evaluate of the same rule overlap. Each
// evaluation moves the rule's token, so only one of them commits.
func testConcurrentEvaluationsOfOneRule(t *testing.T, s Store) {
	const evaluations = 8
	token := newKey("token")
	rule := newKey("rule")
	read := create(t, s, token, map[string]string{"run": "1"})

	errs := make([]error, evaluations)
	var wg sync.WaitGroup
	for i := range evaluations {
		wg.Go(func() {
			errs[i] = commitErr(context.WithoutCancel(t.Context()), s, controlstate.UnitOfWork{
				IdempotencyKey: newIdemKey(),
				Expect:         []controlstate.Precondition{controlstate.Expect(read)},
				Put:            []controlstate.Write{{Key: token, Fields: map[string]string{"run": "2"}}},
				Records:        []controlstate.Record{{Kind: "capture", Subject: rule, Fields: map[string]string{"evaluation": uuid.NewString()}}},
			})
		})
	}
	wg.Wait()

	assertOneWinner(t, errs)
	assert.Equal(t, controlstate.Version(2), load(t, s, token).Version)
	assert.Len(t, records(t, s, rule), 1, "one capture per token move")
}

func testRetryAfterConflictWithNewKey(t *testing.T, s Store) {
	k := newKey("rule")
	stale := create(t, s, k, map[string]string{"name": "v1"})
	require.NoError(t, commitErr(t.Context(), s, replace(load(t, s, k), map[string]string{"name": "v2"})))

	require.ErrorIs(t, commitErr(t.Context(), s, replace(stale, map[string]string{"name": "mine"})), controlstate.ErrConflict)

	require.NoError(t, commitErr(t.Context(), s, replace(load(t, s, k), map[string]string{"name": "mine"})))
	e := load(t, s, k)
	assert.Equal(t, controlstate.Version(3), e.Version)
	assert.Equal(t, "mine", e.Fields["name"])
}

// Ledger v3 consumes the idempotency key of a rejected batch. The contract
// makes that the rule for every adapter, so a retry always re-plans.
func testRejectedKeyStaysConsumed(t *testing.T, s Store) {
	k := newKey("rule")
	stale := create(t, s, k, map[string]string{"name": "v1"})
	require.NoError(t, commitErr(t.Context(), s, replace(load(t, s, k), map[string]string{"name": "v2"})))

	rejected := replace(stale, map[string]string{"name": "mine"})
	require.ErrorIs(t, commitErr(t.Context(), s, rejected), controlstate.ErrConflict)

	// Same key, same content: the first outcome again.
	require.ErrorIs(t, commitErr(t.Context(), s, rejected), controlstate.ErrConflict)

	// Same key, re-planned content: the key is spent.
	replanned := replace(load(t, s, k), map[string]string{"name": "mine"})
	replanned.IdempotencyKey = rejected.IdempotencyKey
	require.ErrorIs(t, commitErr(t.Context(), s, replanned), controlstate.ErrKeyReused)
	assert.Equal(t, "v2", load(t, s, k).Fields["name"])
}

// A lost response is resolved by replaying the same unit of work. The replay
// must not write again, even after a later commit moved the entity on.
func testReplayReturnsFirstOutcome(t *testing.T, s Store) {
	k := newKey("rule")
	e := create(t, s, k, map[string]string{"name": "v1"})

	first := replace(e, map[string]string{"name": "v2"})
	first.Records = []controlstate.Record{{Kind: "activity", Subject: k, Fields: map[string]string{"event": "updated"}}}
	require.NoError(t, commitErr(t.Context(), s, first))

	require.NoError(t, commitErr(t.Context(), s, replace(load(t, s, k), map[string]string{"name": "v3"})))

	require.NoError(t, commitErr(t.Context(), s, first), "replay inside the retention window")
	got := load(t, s, k)
	assert.Equal(t, controlstate.Version(3), got.Version)
	assert.Equal(t, "v3", got.Fields["name"], "the replay did not write again")
	assert.Len(t, records(t, s, k), 1, "the replay did not append again")
}

func testKeyReusedWithDifferentContent(t *testing.T, s Store) {
	k := newKey("rule")
	e := create(t, s, k, map[string]string{"name": "v1"})

	uow := replace(e, map[string]string{"name": "v2"})
	require.NoError(t, commitErr(t.Context(), s, uow))

	other := replace(load(t, s, k), map[string]string{"name": "v3"})
	other.IdempotencyKey = uow.IdempotencyKey
	require.ErrorIs(t, commitErr(t.Context(), s, other), controlstate.ErrKeyReused)

	got := load(t, s, k)
	assert.Equal(t, controlstate.Version(2), got.Version)
	assert.Equal(t, "v2", got.Fields["name"])
}

func testInvalidUnitOfWork(t *testing.T, s Store) {
	a, b := newKey("alert"), newKey("alert")
	put := func(k controlstate.Key) controlstate.Write {
		return controlstate.Write{Key: k, Fields: map[string]string{"status": "OPEN"}}
	}
	for name, uow := range map[string]controlstate.UnitOfWork{
		"empty idempotency key": {
			Expect: []controlstate.Precondition{controlstate.ExpectAbsent(a)},
			Put:    []controlstate.Write{put(a)},
		},
		"nothing to commit": {
			Expect: []controlstate.Precondition{controlstate.ExpectAbsent(a)},
		},
		"write without precondition": {
			Expect: []controlstate.Precondition{controlstate.ExpectAbsent(a)},
			Put:    []controlstate.Write{put(a), put(b)},
		},
		"absent precondition without a write": {
			Expect: []controlstate.Precondition{controlstate.ExpectAbsent(a), controlstate.ExpectAbsent(b)},
			Put:    []controlstate.Write{put(b)},
		},
		"two preconditions on one key": {
			Expect: []controlstate.Precondition{controlstate.ExpectAbsent(a), {Key: a, Version: 1}},
			Put:    []controlstate.Write{put(a)},
		},
		"two writes to one key": {
			Expect: []controlstate.Precondition{controlstate.ExpectAbsent(a)},
			Put:    []controlstate.Write{put(a), put(a)},
		},
		"incomplete key": {
			Expect: []controlstate.Precondition{controlstate.ExpectAbsent(controlstate.Key{Kind: "alert"})},
			Put:    []controlstate.Write{put(controlstate.Key{Kind: "alert"})},
		},
		"record without kind": {
			Records: []controlstate.Record{{Subject: a}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			key := newIdemKey()
			if name != "empty idempotency key" {
				uow.IdempotencyKey = key
			}
			require.ErrorIs(t, commitErr(t.Context(), s, uow), controlstate.ErrInvalid)

			// Validation runs before acceptance, so the key is still free.
			require.NoError(t, commitErr(t.Context(), s, controlstate.UnitOfWork{
				IdempotencyKey: key,
				Records:        []controlstate.Record{{Kind: "activity", Subject: newKey("rule")}},
			}))
		})
	}
	assert.False(t, load(t, s, a).Exists())
	assert.False(t, load(t, s, b).Exists())
}

func testLoadReturnsCopies(t *testing.T, s Store) {
	k := newKey("rule")
	fields := map[string]string{"name": "v1"}
	create(t, s, k, fields)
	fields["name"] = "mutated after commit"

	e := load(t, s, k)
	e.Fields["name"] = "mutated after load"
	assert.Equal(t, "v1", load(t, s, k).Fields["name"])
}

func assertOneWinner(t *testing.T, errs []error) {
	t.Helper()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, controlstate.ErrConflict):
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, won, "exactly one writer commits")
}

// Commit returns the version of every entity it wrote, for the ETag. A
// guarded key that is not written is not in the result. A replay returns the
// first result.
func testCommitReturnsNewVersions(t *testing.T, s Store) {
	rule := newKey("rule")
	token := newKey("token")

	created := controlstate.UnitOfWork{
		IdempotencyKey: newIdemKey(),
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(rule), controlstate.ExpectAbsent(token)},
		Put: []controlstate.Write{
			{Key: rule, Fields: map[string]string{"name": "v1"}},
			{Key: token, Fields: map[string]string{"run": "1"}},
		},
	}
	got, err := s.Commit(t.Context(), created)
	require.NoError(t, err)
	assert.Equal(t, controlstate.Versions{rule: 1, token: 1}, got)

	update := controlstate.UnitOfWork{
		IdempotencyKey: newIdemKey(),
		Expect:         []controlstate.Precondition{controlstate.Expect(load(t, s, rule)), controlstate.Expect(load(t, s, token))},
		Put:            []controlstate.Write{{Key: rule, Fields: map[string]string{"name": "v2"}}},
	}
	got, err = s.Commit(t.Context(), update)
	require.NoError(t, err)
	assert.Equal(t, controlstate.Versions{rule: 2}, got, "the guarded token is not written")
	assert.Equal(t, load(t, s, rule).Version, got[rule], "the result matches what Load returns")

	require.NoError(t, commitErr(t.Context(), s, replace(load(t, s, rule), map[string]string{"name": "v3"})))
	got, err = s.Commit(t.Context(), update)
	require.NoError(t, err)
	assert.Equal(t, controlstate.Versions{rule: 2}, got, "a replay returns the first result")

	got, err = s.Commit(t.Context(), replace(controlstate.Entry{Key: rule, Version: 1}, map[string]string{"name": "stale"}))
	require.ErrorIs(t, err, controlstate.ErrConflict)
	assert.Nil(t, got)
}
