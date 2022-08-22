package server

import (
	"net/http"

	"github.com/numary/reconciliation/internal/mux"
	"github.com/numary/reconciliation/internal/storage/mongo"
	"go.uber.org/fx"
)

func StartModule(httpClient *http.Client) fx.Option {
	return fx.Module("reconciliation server",
		fx.Provide(
			func() *http.Client { return httpClient },
			mongo.NewStore,
			newServerHandler,
			mux.NewServer,
		),
		fx.Invoke(registerHandler),
	)
}

func registerHandler(mux *http.ServeMux, h http.Handler) {
	mux.Handle("/", h)
}
