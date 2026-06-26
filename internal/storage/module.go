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

func Module(connectionOptions bunconnect.ConnectionOptions, debug bool) fx.Option {
	return fx.Options(
		fx.Provide(func() *bunconnect.ConnectionOptions {
			return &connectionOptions
		}),
		bunconnect.Module(connectionOptions, debug),
		fx.Provide(func(db *bun.DB) *Storage {
			return NewStorage(db)
		}),
		fx.Invoke(func(lc fx.Lifecycle, repo *Storage, db *bun.DB) {
			lc.Append(fx.Hook{
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
