package api

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/health"
	"github.com/formancehq/go-libs/httpserver"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"go.uber.org/fx"
)

const (
	ErrInvalidID            = "INVALID_ID"
	ErrMissingOrInvalidBody = "MISSING_OR_INVALID_BODY"
	ErrValidation           = "VALIDATION"
)

func healthCheckModule() fx.Option {
	return fx.Options(
		health.Module(),
		health.ProvideHealthCheck(func() health.NamedCheck {
			return health.NewNamedCheck("default", health.CheckFn(func(ctx context.Context) error {
				return nil
			}))
		}),
	)
}

func HTTPModule(serviceInfo api.ServiceInfo, bind string) fx.Option {
	return fx.Options(
		healthCheckModule(),
		fx.Invoke(func(m *chi.Mux, lc fx.Lifecycle) {
			lc.Append(httpserver.NewHook(m, httpserver.WithAddress(bind)))
		}),
		fx.Provide(func(store *storage.Storage) service.Store {
			return store
		}),
		fx.Supply(serviceInfo),
		fx.Provide(fx.Annotate(service.NewSDKFormance, fx.As(new(service.SDKFormance)))),

		// V1 engine + templates wiring. The SDKFormance interface is a superset
		// of engine.SDKClient (it adds the legacy GetPoolBalances for the
		// /policies path), so it satisfies the engine's narrower contract via
		// Go's structural subtyping at this assignment.
		fx.Provide(provideResolvers),
		fx.Provide(provideEngine),
		fx.Provide(templates.DefaultRegistry),

		// Bridge: the repo mixes go-libs (v3, used by service.New) and
		// go-libs/v5 (used by messagingfx + the new SDK paths). v3's
		// service.New supplies a v3 logging.Logger, but v5's messagingfx
		// consumes v5's observe/log.Logger — a different type at the same
		// nominal path. Provide a v5 logger explicitly so the fx graph
		// has both. NB: legacy /policies still uses the v3 logger from
		// service.New; only V1-touching v5 modules read this one.
		fx.Provide(func(info api.ServiceInfo) v5log.Logger {
			return v5log.NewDefaultLogger(os.Stdout, info.Debug, false, false)
		}),

		fx.Provide(fx.Annotate(service.NewService, fx.As(new(backend.Service)))),
		fx.Provide(backend.NewDefaultBackend),
		fx.Provide(newRouter),
	)
}

// provideResolvers builds the engine.Resolvers fan-out from a single SDK
// client. Kept here (and not in the engine package) because the SDK type
// conversion is an api-layer wiring concern.
func provideResolvers(client service.SDKFormance) engine.Resolvers {
	var sdkClient engine.SDKClient = client
	return engine.Resolvers{
		Ledger:   engine.NewSDKLedgerResolver(sdkClient),
		Payments: engine.NewSDKPaymentsResolver(sdkClient),
	}
}

func provideEngine(resolvers engine.Resolvers) (*engine.Engine, error) {
	return engine.New(resolvers, engine.DefaultLimits)
}

func handleServiceErrors(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrValidation):
		api.BadRequest(w, ErrValidation, err)
	case errors.Is(err, service.ErrInvalidID):
		api.BadRequest(w, ErrInvalidID, err)
	case errors.Is(err, storage.ErrInvalidQuery):
		api.BadRequest(w, ErrValidation, err)
	case errors.Is(err, storage.ErrNotFound):
		api.NotFound(w, err)
	// V1 error classes
	case errors.Is(err, templates.ErrInvalidSpec):
		api.BadRequest(w, ErrValidation, err)
	case errors.Is(err, templates.ErrUnknownTemplate):
		api.BadRequest(w, ErrValidation, err)
	case errors.Is(err, engine.ErrCompile):
		api.BadRequest(w, ErrValidation, err)
	default:
		api.InternalServerError(w, r, err)
	}
}
