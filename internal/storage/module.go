package storage

import (
	"context"
	"fmt"

	"github.com/formancehq/go-libs/bun/bunconnect"
	"github.com/formancehq/go-libs/logging"
	"github.com/formancehq/reconciliation/internal/storage/migrations"
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
				// Apply pending migrations at startup. Mirrors the pattern
				// every other Formance service uses (--auto-migrate on the
				// serve binary): no dedicated one-shot migrate container,
				// no operational drift between local dev and prod. The
				// migration registry uses CREATE … IF NOT EXISTS, so this
				// is safe to re-run on an already-migrated database.
				OnStart: func(ctx context.Context) error {
					logging.FromContext(ctx).Debug("Applying database migrations...")
					if err := migrations.Migrate(ctx, db); err != nil {
						return fmt.Errorf("auto-migrate: %w", err)
					}
					logging.FromContext(ctx).Debug("Migrations up to date.")
					return nil
				},
			})
		}),
	)
}
