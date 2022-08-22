package worker

import (
	"context"
	"fmt"
	"net/http"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/internal/kafka"
	"github.com/numary/reconciliation/internal/mux"
	"github.com/numary/reconciliation/internal/storage"
	"github.com/numary/reconciliation/internal/storage/mongo"
	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/fx"
)

func StartModule(httpClient *http.Client) fx.Option {
	return fx.Module("reconciliation worker",
		fx.Provide(
			func() *http.Client { return httpClient },
			mongo.NewStore,
			newKafkaReaderWorker,
			newWorkerHandler,
			mux.NewWorker,
		),
		fx.Invoke(registerHandler),
		fx.Invoke(runWorker),
	)
}

func newKafkaReaderWorker(lc fx.Lifecycle, store storage.Store) (reader *kafkago.Reader, worker *kafka.Worker, err error) {
	cfg, err := kafka.NewKafkaReaderConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("kafka.NewKafkaReaderConfig: %w", err)
	}

	reader = kafkago.NewReader(cfg)
	worker = kafka.NewWorker(reader, store)
	return reader, worker, nil
}

func registerHandler(mux *http.ServeMux, h http.Handler) {
	mux.Handle("/", h)
}

func runWorker(lc fx.Lifecycle, worker *kafka.Worker, store storage.Store, reader *kafkago.Reader) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			sharedlogging.GetLogger(ctx).Debugf("starting worker...")
			go func() {
				if err := worker.Run(ctx); err != nil {
					sharedlogging.GetLogger(ctx).Errorf("kafka.Worker.Run: %s", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			sharedlogging.GetLogger(ctx).Debugf("stopping worker...")
			worker.Stop(ctx)
			err1 := store.Close(ctx)
			err2 := reader.Close()
			if err1 != nil || err2 != nil {
				return fmt.Errorf("[closing store: %s] [closing reader: %s]", err1, err2)
			}
			return nil
		},
	})
}
