package server

import (
	"net/http"

	"github.com/numary/reconciliation/pkg/httpserver"
	"github.com/numary/reconciliation/pkg/storage/mongo"
	"go.uber.org/fx"
)

func StartModule(httpClient *http.Client, addr string) fx.Option {
	return fx.Module("reconciliation server",
		fx.Provide(
			func() (*http.Client, string) { return httpClient, addr },
			httpserver.NewMuxServer,
			mongo.NewStore,
			newServerHandler,
		),
		fx.Invoke(httpserver.RegisterHandler),
		fx.Invoke(httpserver.Run),
	)
}
