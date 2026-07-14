package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/health"
	"github.com/formancehq/go-libs/httpserver"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/storage"
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
			// Intentionally a process-only liveness check: it must NOT ping the
			// database or any dependency. The operator wires /_healthcheck to
			// BOTH the liveness and readiness probes, and the liveness probe
			// restarts the pod after ~40s of failures. Checking the DB here
			// would turn a transient DB outage into a restart storm across all
			// replicas. DB reachability and migration state are verified once at
			// startup (storage OnStart hook), which fails fast instead.
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
		fx.Provide(fx.Annotate(service.NewService, fx.As(new(backend.Service)))),
		fx.Provide(backend.NewDefaultBackend),
		fx.Provide(newRouter),
	)
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
	default:
		api.InternalServerError(w, r, err)
	}
}
