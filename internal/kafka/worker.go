package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/internal/storage"
	kafkago "github.com/segmentio/kafka-go"
)

type Event struct {
	Date    time.Time      `json:"date"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

type Worker struct {
	reader Reader
	store  storage.Store

	stopChan chan chan struct{}
}

func NewWorker(reader Reader, store storage.Store) *Worker {
	return &Worker{
		reader:   reader,
		store:    store,
		stopChan: make(chan chan struct{}),
	}
}

func (w *Worker) Run(ctx context.Context) error {
	msgChan := make(chan kafkago.Message)
	errChan := make(chan error)
	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	go w.fetchMessages(ctxWithCancel, msgChan, errChan)

	for {
		select {
		case ch := <-w.stopChan:
			sharedlogging.GetLogger(ctx).Debug("worker: received from stopChan")
			close(ch)
			return nil
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("worker: context done: %s", ctx.Err())
			return nil
		case err := <-errChan:
			return fmt.Errorf("kafka.Worker.fetchMessages: %w", err)
		case msg := <-msgChan:
			if err := w.processMessage(ctx, msg); err != nil {
				return fmt.Errorf("processMessage: %w", err)
			}
		}
	}
}

func (w *Worker) fetchMessages(ctx context.Context, msgChan chan kafkago.Message, errChan chan error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := w.reader.FetchMessage(ctx)
			if err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					select {
					case errChan <- fmt.Errorf("kafka.Reader.FetchMessage: %w", err):
					case <-ctx.Done():
						return
					}
				}
				continue
			}

			select {
			case msgChan <- msg:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (w *Worker) processMessage(ctx context.Context, msg kafkago.Message) error {
	ctx = sharedlogging.ContextWithLogger(ctx,
		sharedlogging.GetLogger(ctx).WithFields(map[string]any{
			"offset": msg.Offset,
		}))
	sharedlogging.GetLogger(ctx).WithFields(map[string]any{
		"time":      msg.Time.UTC().Format(time.RFC3339),
		"partition": msg.Partition,
		"data":      string(msg.Value),
		"headers":   msg.Headers,
	}).Debug("worker: new kafka message fetched")

	ev := Event{}
	if err := json.Unmarshal(msg.Value, &ev); err != nil {
		return fmt.Errorf("json.Unmarshal: %w", err)
	}

	sharedlogging.GetLogger(ctx).Debugf("worker: new kafka event fetched: %+v", ev)

	if err := w.reader.CommitMessages(ctx, msg); err != nil {
		return fmt.Errorf("kafka.Reader.CommitMessages: %w", err)
	}

	return nil
}

func (w *Worker) Stop(ctx context.Context) {
	ch := make(chan struct{})
	select {
	case <-ctx.Done():
		sharedlogging.GetLogger(ctx).Debugf("worker stopped: context done: %s", ctx.Err())
		return
	case w.stopChan <- ch:
		select {
		case <-ctx.Done():
			sharedlogging.GetLogger(ctx).Debugf("worker stopped via stopChan: context done: %s", ctx.Err())
			return
		case <-ch:
			sharedlogging.GetLogger(ctx).Debug("worker stopped via stopChan")
		}
	}
}
