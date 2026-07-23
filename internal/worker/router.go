package worker

import (
	"context"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/health"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func newRouter(info sharedapi.ServiceInfo, healthController *health.HealthController) *chi.Mux {
	router := chi.NewRouter()
	router.Use(middleware.Recoverer)
	router.Get("/_healthcheck", healthController.Check)
	router.Get("/_info", sharedapi.InfoHandler(info))
	return router
}

func healthModule() health.NamedCheck {
	return health.NewNamedCheck("worker", health.CheckFn(func(context.Context) error { return nil }))
}
