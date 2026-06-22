package storage

import (
	"context"
	"sync"

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

// WithPublisher returns a copy of the storage configured to emit alert events
// through pub. Used by the fx wiring; tests can pass a fake (or leave it unset
// for a silent no-op).
func (s *Storage) WithPublisher(pub AlertEventPublisher) *Storage {
	cp := *s
	cp.publisher = pub
	return &cp
}

// WithTx returns a Storage rooted on the supplied transaction. All subsequent
// calls on the returned value participate in that tx instead of the root pool.
// Nested calls to RunInTx on a bun.Tx create savepoints, so methods that open
// their own inner transactions (OpenOrUpdateAlert, AckAlert) keep their
// rollback semantics under an outer tx.
func (s *Storage) WithTx(tx bun.Tx) *Storage {
	return &Storage{db: tx, pool: s.pool, publisher: s.publisher}
}

// RunInTx exposes the underlying bun transaction loop so the service layer can
// orchestrate multi-method atomic units (e.g. persist evaluation + drive
// alert transitions). The callback receives a tx-scoped Storage so it
// doesn't need to know about bun internals. Uses bun's default isolation;
// callers needing something stronger should compose at a higher layer.
//
// It is also the boundary that makes alert-event publication transactional:
// the OUTERMOST RunInTx owns a collector (stashed in ctx) onto which every
// nested transition buffers its event, and flushes it — publishing to the
// message bus — only after the transaction has actually committed. A
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
// they publish exactly once — after the OUTERMOST commit — rather than after
// each inner savepoint releases.
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
// an appended alert_event into an outbound message, so the 1:1 invariant
// (one event row ⇒ one webhook message) holds by construction.
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
func (s *Storage) recordAlertEvent(ctx context.Context, alert *models.Alert, event *models.AlertEvent) {
	if event == nil {
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
