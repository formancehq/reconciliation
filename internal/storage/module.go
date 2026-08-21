package storage

import (
	"context"

	legacyconnect "github.com/formancehq/go-libs/bun/bunconnect"
	"github.com/formancehq/go-libs/logging"
	v5connect "github.com/formancehq/go-libs/v5/pkg/storage/bun/connect"
	"github.com/formancehq/reconciliation/internal/storage/migrations"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
	"go.uber.org/fx"
)

// storageParams collects the Storage constructor's dependencies. Publisher is
// optional: when messaging is not wired (broker-less local dev, the migrate
// path, tests) it resolves to nil and alert-event emission becomes a no-op.
type storageParams struct {
	fx.In
	DB        *bun.DB
	Publisher AlertEventPublisher `optional:"true"`
}

func Module(connectionOptions legacyconnect.ConnectionOptions, publisherConnectionOptions v5connect.ConnectionOptions, debug bool, auditChain AuditChainSettings) fx.Option {
	return fx.Options(
		fx.Provide(func() *legacyconnect.ConnectionOptions {
			return &connectionOptions
		}),
		// go-libs v5's PostgreSQL circuit breaker owns a separate pgx-backed
		// connection pool. Do not reuse the legacy lib/pq connector: the v5
		// migrator unwraps pgx's stdlib.Conn for its progress listener.
		fx.Provide(func() *v5connect.ConnectionOptions {
			return &publisherConnectionOptions
		}),
		legacyconnect.Module(connectionOptions, debug),
		fx.Provide(func(p storageParams) *Storage {
			return NewStorage(p.DB).WithPublisher(p.Publisher)
		}),
		fx.Invoke(func(lc fx.Lifecycle, repo *Storage, db *bun.DB) {
			lc.Append(fx.Hook{
				// Verify DB reachability and that migrations are applied — but do
				// NOT run them here. Migrations are applied out-of-band (the
				// `migrate` one-shot command) so a rollout across replicas never
				// races the migrator. Startup fails fast if the schema is behind.
				OnStart: func(ctx context.Context) error {
					logging.FromContext(ctx).Debug("Ping database...")
					if err := repo.Ping(ctx); err != nil {
						return errors.Wrap(err, "failed to ping database")
					}

					logging.FromContext(ctx).Debug("Checking migrations state...")
					upToDate, err := migrations.IsUpToDate(ctx, db)
					if err != nil {
						return errors.Wrap(err, "failed to check migrations state")
					}
					if !upToDate {
						return errors.New("database is not up to date, please run migrations")
					}

					// Bootstrap or validate the audit journal's key material.
					// Deliberately fatal on mismatch: starting under a key that
					// cannot verify the existing chain would produce a journal
					// that reports itself broken forever, from a configuration
					// mistake rather than from tampering — and a tamper signal
					// that fires for benign reasons is a tamper signal nobody
					// will act on.
					logging.FromContext(ctx).Debug("Initialising audit journal...")
					if err := repo.InitAuditChain(ctx, auditChain); err != nil {
						return errors.Wrap(err, "failed to initialise the audit journal")
					}

					// After the chain, because the first closure's range starts at
					// the journal head: opening it before the chain exists would
					// give it a boundary derived from a journal that is not there
					// yet.
					if err := repo.InitClosures(ctx); err != nil {
						return errors.Wrap(err, "failed to open the first closure")
					}

					return nil
				},
			})
		}),
	)
}
