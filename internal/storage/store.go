package storage

import (
	"context"

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
}

func NewStorage(db *bun.DB) *Storage {
	return &Storage{db: db, pool: db}
}

// WithTx returns a Storage rooted on the supplied transaction. All subsequent
// calls on the returned value participate in that tx instead of the root pool.
// Nested calls to RunInTx on a bun.Tx create savepoints, so methods that open
// their own inner transactions (OpenOrUpdateIncident, AckIncident) keep their
// rollback semantics under an outer tx.
func (s *Storage) WithTx(tx bun.Tx) *Storage {
	return &Storage{db: tx, pool: s.pool}
}

// RunInTx exposes the underlying bun transaction loop so the service layer can
// orchestrate multi-method atomic units (e.g. persist evaluation + drive
// incident transitions). The callback receives a tx-scoped Storage so it
// doesn't need to know about bun internals. Uses bun's default isolation;
// callers needing something stronger should compose at a higher layer.
func (s *Storage) RunInTx(ctx context.Context, fn func(ctx context.Context, store *Storage) error) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return fn(ctx, s.WithTx(tx))
	})
}
