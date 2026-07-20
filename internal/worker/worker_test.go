package worker

import (
	"io"
	"testing"
	"time"

	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/stretchr/testify/require"
)

func TestRetryDelay(t *testing.T) {
	tests := map[int]time.Duration{
		1: 5 * time.Second,
		2: 10 * time.Second,
		3: 20 * time.Second,
		5: 80 * time.Second,
		9: 5 * time.Minute,
	}
	for attempt, expected := range tests {
		if got := retryDelay(attempt); got != expected {
			t.Fatalf("retryDelay(%d) = %s, want %s", attempt, got, expected)
		}
	}
}

func TestWorkerRejectsHeartbeatLongerThanLease(t *testing.T) {
	_, err := New(Config{LeaseDuration: time.Second, HeartbeatInterval: 2 * time.Second}, nil, nil,
		v5log.NewDefaultLogger(io.Discard, false, false, false))
	require.Error(t, err)
}
