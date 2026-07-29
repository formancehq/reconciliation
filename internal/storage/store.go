package storage

import (
	"context"
	"sync"

	logging "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/uptrace/bun"
)

// Storage wraps a bun handle that may be either a *bun.DB (the root pool) or a
// bun.Tx (a scoped transaction). bun.IDB satisfies both, so every method in
// this package compiles against either — which is what lets `WithTx` produce
// a transactional view of Storage without duplicating method sets.
type Storage struct {
	// db is the bun handle every storage method runs against — either the
	// root pool or a tx, depending on whether this Storage came from
	// NewStorage or WithTx.
	db bun.IDB
	// pool is always the root *bun.DB, retained so connection-level
	// operations (Ping) work even when this Storage instance is tx-scoped.
	pool *bun.DB
	// publisher emits alert lifecycle events to the message bus after a
	// transition commits. nil when messaging is not configured (the service
	// then runs without emitting events) — see recordAlertEvent.
	publisher AlertEventPublisher
	// chain hashes audit entries under this installation's key. Unlike
	// publisher, a nil chain is not a degraded mode that keeps working: every
	// append fails with ErrAuditChainNotConfigured, which fails the operation
	// that was trying to record itself. That is deliberate — a write that
	// cannot be journalled must not happen, and the alternative (skip the
	// journal, keep the write) is exactly the silent hole this feature exists
	// to close. Production wiring calls EnsureAuditChain at boot.
	chain *audit.Chain
	// signingKey signs period seals so a third party can verify them without
	// trusting this service. Verify-only or absent keys are tolerated: sealing
	// still works, the seal simply carries no signature.
	signingKey audit.SigningKey
}

// AlertEventPublisher dispatches an alert lifecycle transition to the message
// bus. Implemented by internal/events.Publisher and injected via fx; a nil
// publisher makes every emission a no-op (tests, messaging disabled).
type AlertEventPublisher interface {
	PublishAlertEvent(ctx context.Context, alert *models.Alert, event *models.AlertEvent)
}

func NewStorage(db *bun.DB) *Storage {
	return &Storage{db: db, pool: db}
}

// MaxOpenConnections exposes the root pool's hard connection limit. A zero
// value means database/sql is unbounded. The worker uses this to ensure its
// long-lived advisory-lock sessions cannot consume every connection and starve
// the independent lease heartbeats that fence those sessions.
func (s *Storage) MaxOpenConnections() int {
	if s == nil || s.pool == nil || s.pool.DB == nil {
		return 0
	}
	return s.pool.DB.Stats().MaxOpenConnections
}

// WithPublisher returns a copy of the storage configured to emit alert events
// through pub. Used by the fx wiring; tests can pass a fake (or leave it unset
// for a silent no-op).
func (s *Storage) WithPublisher(pub AlertEventPublisher) *Storage {
	cp := *s
	cp.publisher = pub
	return &cp
}

// WithAuditChain returns a copy of the storage that journals every state-bearing
// write into the hash chain. Callers normally get this from EnsureAuditChain,
// which also bootstraps the installation's key material.
func (s *Storage) WithAuditChain(chain *audit.Chain, signingKey audit.SigningKey) *Storage {
	cp := *s
	cp.chain = chain
	cp.signingKey = signingKey
	return &cp
}

// SigningKey exposes the seal signing key so the API can publish its public half
// — the one value an external auditor needs.
func (s *Storage) SigningKey() audit.SigningKey { return s.signingKey }

// HasAuditChain reports whether the journal is wired.
func (s *Storage) HasAuditChain() bool { return s != nil && s.chain != nil }

// WithTx returns a Storage rooted on the supplied transaction. All subsequent
// calls on the returned value participate in that tx instead of the root pool.
// Nested calls to RunInTx on a bun.Tx create savepoints, so methods that open
// their own inner transactions (OpenOrUpdateAlert, AckAlert) keep their
// rollback semantics under an outer tx.
func (s *Storage) WithTx(tx bun.Tx) *Storage {
	cp := *s
	cp.db = tx
	return &cp
}

// WithConn returns a storage view rooted on one dedicated database session.
// It is used by rule evaluation so the advisory lock and the final transaction
// live on the same connection.
func (s *Storage) WithConn(conn bun.Conn) *Storage {
	cp := *s
	cp.db = conn
	return &cp
}

// RunInTx exposes the underlying bun transaction loop so the service layer can
// orchestrate multi-method atomic units (e.g. persist evaluation + drive
// alert transitions). The callback receives a tx-scoped Storage so it
// doesn't need to know about bun internals. Uses bun's default isolation;
// callers needing something stronger should compose at a higher layer.
//
// It is also the boundary that makes alert-event publication transactional:
// the OUTERMOST RunInTx owns a collector (stashed in ctx) onto which every
// nested transition buffers its event, and flushes each buffered publish
// attempt only after the transaction has actually committed. A
// rolled-back unit (e.g. a mid-loop failure in driveAlerts) therefore emits
// nothing. Nested RunInTx calls (bun savepoints) defer to the outer owner.
func (s *Storage) RunInTx(ctx context.Context, fn func(ctx context.Context, store *Storage) error) error {
	run := func(ctx context.Context) error {
		return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return fn(ctx, s.WithTx(tx))
		})
	}
	// Nested: an outer RunInTx already owns the collector and will flush after
	// the real commit.
	if alertEventCollectorFrom(ctx) != nil {
		return run(ctx)
	}
	// Outermost: own a collector and publish only once the tx has committed.
	c := &alertEventCollector{}
	ctx = withAlertEventCollector(ctx, c)
	if err := run(ctx); err != nil {
		return err
	}
	s.flushAlertEvents(ctx, c)
	return nil
}

// pendingAlertEvent is one (alert, event) pair awaiting publication once the
// enclosing transaction commits.
type pendingAlertEvent struct {
	alert *models.Alert
	event *models.AlertEvent
}

// alertEventCollector buffers events produced inside an outer transaction so
// each transition produces one post-commit publish attempt rather than one per
// nested savepoint. Broker replay semantics remain at-least-once.
type alertEventCollector struct {
	mu      sync.Mutex
	pending []pendingAlertEvent
}

type alertEventCollectorKey struct{}

func withAlertEventCollector(ctx context.Context, c *alertEventCollector) context.Context {
	return context.WithValue(ctx, alertEventCollectorKey{}, c)
}

func alertEventCollectorFrom(ctx context.Context) *alertEventCollector {
	c, _ := ctx.Value(alertEventCollectorKey{}).(*alertEventCollector)
	return c
}

// recordAlertEvent is called by every alert-mutating method AFTER its own
// transaction has durably written the event row. It is the one hook that turns
// an appended alert_event into an outbound message, so the invariant — one
// event row with notify=true ⇒ one webhook message — holds by construction.
//
// When the call is nested under an outer Storage.RunInTx (the evaluation path:
// persist the evaluation and drive every alert transition atomically) the
// event is buffered on the ctx collector and published only when that outer
// transaction commits — so a rolled-back evaluation publishes nothing. With no
// outer transaction (the manual ack/resolve/accept API paths) the method's own
// commit IS the real commit, so the event publishes immediately.
//
// event may be nil (an idempotent no-op transition, e.g. re-ack of an
// already-acknowledged alert wrote no row) — nothing is recorded.
//
// event.Notify=false rows (a repeated, materially-identical fail) are the
// single suppression point: the row is already durably in the append-only log,
// but it is neither buffered nor published — the bus only ever sees transitions
// that carry new information. See docs/technical/notification-suppression.md.
func (s *Storage) recordAlertEvent(ctx context.Context, alert *models.Alert, event *models.AlertEvent) {
	if event == nil {
		return
	}
	if !event.Notify {
		logging.FromContext(ctx).WithFields(map[string]any{
			"alert":      alert.ID,
			"alertEvent": event.ID,
		}).Debugf("reconciliation: suppressing notification for repeated identical alert fail")
		return
	}
	if c := alertEventCollectorFrom(ctx); c != nil {
		c.mu.Lock()
		c.pending = append(c.pending, pendingAlertEvent{alert: alert, event: event})
		c.mu.Unlock()
		return
	}
	if s.publisher != nil {
		s.publisher.PublishAlertEvent(ctx, alert, event)
	}
}

// flushAlertEvents publishes everything a collector buffered. Called once, by
// the outermost RunInTx, after the transaction commits.
func (s *Storage) flushAlertEvents(ctx context.Context, c *alertEventCollector) {
	c.mu.Lock()
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	if s.publisher == nil {
		return
	}
	for _, p := range pending {
		s.publisher.PublishAlertEvent(ctx, p.alert, p.event)
	}
}
