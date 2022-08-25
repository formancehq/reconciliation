package test_test

import (
	"context"
	"encoding/json"
	"github.com/numary/reconciliation/internal/model"
	"net/http"
	"testing"
	"time"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/internal/server"
	"github.com/numary/reconciliation/internal/worker"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx/fxtest"
)

func TestWorker(t *testing.T) {
	workerApp := fxtest.New(t, worker.StartModule(httpClient))
	require.NoError(t, workerApp.Start(context.Background()))

	var err error
	var conn *kafkago.Conn
	for conn == nil {
		conn, err = kafkago.DialLeader(context.Background(), "tcp",
			viper.GetStringSlice(constants.KafkaBrokersFlag)[0],
			viper.GetStringSlice(constants.KafkaTopicsFlag)[0], 0)
		if err != nil {
			sharedlogging.GetLogger(context.Background()).Debug("connecting to kafka: err: ", err)
			time.Sleep(3 * time.Second)
		}
	}
	defer func() {
		require.NoError(t, conn.Close())
	}()

	t.Run("health check", func(t *testing.T) {
		resp, err := http.Get(workerBaseURL + server.PathHealthCheck)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	n := 3
	var messages []kafkago.Message
	for i := 0; i < n; i++ {
		messages = append(messages, newEventMessage(t, "TYPE", i))
	}
	nbBytes, err := conn.WriteMessages(messages...)
	require.NoError(t, err)
	require.NotEqual(t, 0, nbBytes)

	time.Sleep(3 * time.Second)

	require.NoError(t, workerApp.Stop(context.Background()))
}

func newEventMessage(t *testing.T, eventType string, id int) kafkago.Message {
	ev := model.Event{
		Date: time.Now().UTC(),
		Type: eventType,
		Payload: map[string]any{
			"id": id,
		},
	}

	by, err := json.Marshal(ev)
	require.NoError(t, err)

	return kafkago.Message{
		Key:   []byte("key"),
		Value: by,
	}
}
