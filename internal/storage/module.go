package storage

import (
	"context"

	"github.com/formancehq/go-libs/bun/bunconnect"
	"github.com/formancehq/go-libs/logging"
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

func Module(connectionOptions bunconnect.ConnectionOptions, debug bool) fx.Option {
	return fx.Options(
		fx.Provide(func() *bunconnect.ConnectionOptions {
			return &connectionOptions
		}),
		bunconnect.Module(connectionOptions, debug),
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
