package worker

import (
	"context"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/health"
	"github.com/formancehq/go-libs/httpserver"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/events"
	domain "github.com/formancehq/reconciliation/internal/reconciliation"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

type Config struct {
	Listen            string
	Version           string
	Debug             bool
	Concurrency       int
	PollingInterval   time.Duration
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	Retention         time.Duration
	PlannerInterval   time.Duration
}

type publisherParams struct {
	fx.In
	Publisher message.Publisher `optional:"true"`
}

func Module(config Config) fx.Option {
	return fx.Options(
		fx.Supply(config),
		fx.Supply(sharedapi.ServiceInfo{Version: config.Version, Debug: config.Debug}),
		health.Module(),
		health.ProvideHealthCheck(healthModule),
		fx.Provide(func(params publisherParams) storage.AlertEventPublisher { return events.NewPublisher(params.Publisher) }),
		fx.Provide(fx.Annotate(service.NewSDKFormance, fx.As(new(service.SDKFormance)))),
		fx.Provide(func(client service.SDKFormance) engine.Resolvers {
			return engine.Resolvers{
				Ledger: engine.NewSDKLedgerResolver(client), Payments: engine.NewSDKPaymentsResolver(client),
			}
		}),
		fx.Provide(func(resolvers engine.Resolvers) (*engine.Engine, error) {
			return engine.New(resolvers, engine.DefaultLimits)
		}),
		fx.Provide(templates.DefaultRegistry),
		fx.Provide(func(store *storage.Storage, eng *engine.Engine, registry *templates.Registry, resolvers engine.Resolvers) *domain.Runner {
			return domain.NewRunner(store, eng, registry, resolvers)
		}),
		fx.Provide(New),
		fx.Provide(fx.Annotate(newRouter, fx.ResultTags(`name:"worker-health"`))),
		fx.Invoke(fx.Annotate(func(router *chi.Mux, lifecycle fx.Lifecycle, config Config) {
			hook := httpserver.NewHook(router, httpserver.WithAddress(config.Listen))
			lifecycle.Append(fx.Hook{OnStart: hook.OnStart, OnStop: hook.OnStop})
		}, fx.ParamTags(`name:"worker-health"`, ``, ``))),
		fx.Invoke(func(lifecycle fx.Lifecycle, worker *Worker) {
			var cancel context.CancelFunc
			lifecycle.Append(fx.Hook{
				OnStart: func(context.Context) error {
					var ctx context.Context
					ctx, cancel = context.WithCancel(context.Background())
					worker.Start(ctx)
					return nil
				},
				OnStop: func(ctx context.Context) error {
					if cancel != nil {
						cancel()
					}
					return worker.Wait(ctx)
				},
			})
		}),
	)
}
