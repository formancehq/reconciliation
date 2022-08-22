package worker

import (
	"context"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/spf13/cobra"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/fx"
	"net/http"
	"syscall"
)

//TODO: Method who gets a payment from kafka and check if there is a pay-in/payout tx linked to it

//TODO: Method who gets a tx from kafka and checks if there is a flow id, and redo a recon if there is

func Start(cmd *cobra.Command, args []string) {
	sharedlogging.Infof("env: %+v", syscall.Environ())

	app := fx.New(StartModule(cmd.Context(), http.DefaultClient))
	app.Run()
}

func StartModule(ctx context.Context, httpClient *http.Client) fx.Option {
	return fx.Module("webhooks worker module",
		fx.Provide(
			func() context.Context { return ctx },
			func() *http.Client { return httpClient },
			mongo.NewConfigStore,
			svix.New,
			newKafkaWorker,
			newWorkerHandler,
			mux.NewWorker,
		),
		fx.Invoke(register),
	)
}

func newKafkaWorker(lc fx.Lifecycle, store mongo.Database) (*kafka.Worker, error) {
	cfg, err := kafka.NewKafkaReaderConfig()
	if err != nil {
		return nil, err
	}

	reader := kafkago.NewReader(cfg)

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			err1 := store.Close(ctx)
			err2 := reader.Close()
			if err1 != nil || err2 != nil {
				return fmt.Errorf("[closing store: %s] [closing reader: %s]", err1, err2)
			}
			return nil
		},
	})

	return kafka.NewWorker(reader, store, svixClient, svixAppId), nil
}
