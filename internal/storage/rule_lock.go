package storage

import (
	"context"
	"fmt"
	"hash/crc32"

	logging "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/google/uuid"
)

// ruleEvalLockClass namespaces reconciliation's rule-evaluation advisory locks
// within the database-global pg_advisory_lock keyspace, so a lock taken here can
// never collide with one another Formance module takes on the same database. It
// is the first argument of the two-int4 pg_advisory_lock(classid, objid) form.
var ruleEvalLockClass = int32(crc32.ChecksumIEEE([]byte("reconciliation:rule_eval")))

// ruleAdvisoryLockKey maps a rule id onto the objid half of the advisory-lock
// key. A crc32 collision between two DIFFERENT rules only costs a little extra
// serialisation (they would briefly queue behind each other) — never a
// correctness problem — and is negligibly unlikely at any realistic rule count.
func ruleAdvisoryLockKey(ruleID uuid.UUID) int32 {
	return int32(crc32.ChecksumIEEE(ruleID[:]))
}

// TryWithRuleLock runs fn while holding a session-level Postgres advisory lock
// keyed on ruleID. It never waits for a busy rule: callers can return 409 or
// requeue a job without pinning one pooled connection per waiter.
//
// This is the ordering guard for rule evaluation. An evaluation reads its
// sources and then commits its alert transitions in two separate steps; without
// this lock a slower evaluation that read OLDER source state could commit AFTER
// a newer one and reopen an alert the newer evaluation just resolved — stale
// evidence plus a spurious reopen webhook. Holding the lock across BOTH the read
// and the persist makes same-rule evaluations fully serial, so the one that
// commits last is always the one that read last.
//
// Different rules take different keys and never contend; a same-rule trigger
// observes acquired=false immediately so the API can return 409 and the worker
// can requeue without consuming an attempt.
func (s *Storage) TryWithRuleLock(ctx context.Context, ruleID uuid.UUID, fn func(ctx context.Context, scoped *Storage) error) (bool, error) {
	// A dedicated pooled connection: a session-level advisory lock lives on the
	// connection that took it, so every step must run on this same handle.
	conn, err := s.pool.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("rule lock: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	key := ruleAdvisoryLockKey(ruleID)
	var acquired bool
	if err := conn.NewSelect().ColumnExpr("pg_try_advisory_lock(?, ?)", ruleEvalLockClass, key).Scan(ctx, &acquired); err != nil {
		return false, fmt.Errorf("rule lock: try advisory lock for rule %s: %w", ruleID, err)
	}
	if !acquired {
		return false, nil
	}
	defer func() {
		// A pooled connection is RESET and reused, not physically closed, so a
		// session-level advisory lock would leak if we relied on conn.Close() to
		// drop it — release it explicitly. Use a cancel-free context so an
		// already-cancelled ctx (client hung up, shutdown) still frees the lock.
		unlockCtx := context.WithoutCancel(ctx)
		if _, uerr := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock(?, ?)", ruleEvalLockClass, key); uerr != nil {
			logging.FromContext(ctx).Errorf("reconciliation: releasing rule advisory lock for rule %s: %s", ruleID, uerr)
		}
	}()

	return true, fn(ctx, s.WithConn(conn))
}

// WithRuleLock retains the blocking semantics used by pre-v3 callers. It also
// preserves cross-version rollout coordination because both implementations
// use exactly the same advisory key. New evaluation paths use TryWithRuleLock.
func (s *Storage) WithRuleLock(ctx context.Context, ruleID uuid.UUID, fn func(ctx context.Context) error) error {
	conn, err := s.pool.Conn(ctx)
	if err != nil {
		return fmt.Errorf("rule lock: acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	key := ruleAdvisoryLockKey(ruleID)
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(?, ?)", ruleEvalLockClass, key); err != nil {
		return fmt.Errorf("rule lock: acquire advisory lock for rule %s: %w", ruleID, err)
	}
	defer func() {
		unlockCtx := context.WithoutCancel(ctx)
		if _, uerr := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock(?, ?)", ruleEvalLockClass, key); uerr != nil {
			logging.FromContext(ctx).Errorf("reconciliation: releasing rule advisory lock for rule %s: %s", ruleID, uerr)
		}
	}()
	return fn(ctx)
}
