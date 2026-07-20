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

func Module(connectionOptions legacyconnect.ConnectionOptions, debug bool) fx.Option {
	return fx.Options(
		fx.Provide(func() *legacyconnect.ConnectionOptions {
			return &connectionOptions
		}),
		// go-libs v5's PostgreSQL circuit breaker owns a separate connection
		// pool and consumes the v5 connection-options type. Keep the service's
		// legacy Bun module for now, but expose the same DSN and pool bounds to
		// the publisher so the breaker is actually wired on API and worker.
		fx.Provide(func() *v5connect.ConnectionOptions {
			return &v5connect.ConnectionOptions{
				DatabaseSourceName: connectionOptions.DatabaseSourceName,
				MaxIdleConns:       connectionOptions.MaxIdleConns,
				MaxOpenConns:       connectionOptions.MaxOpenConns,
				ConnMaxIdleTime:    connectionOptions.ConnMaxIdleTime,
				Connector:          connectionOptions.Connector,
			}
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

					return nil
				},
			})
		}),
	)
}
