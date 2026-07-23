package worker

import (
	"database/sql"
	"io"
	"testing"
	"time"

	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
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

func TestWorkerRejectsConcurrencyThatStarvesHeartbeats(t *testing.T) {
	sqlDB, err := sql.Open("pgx", "postgres://unused")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	sqlDB.SetMaxOpenConns(4)

	db := bun.NewDB(sqlDB, pgdialect.New())
	store := storage.NewStorage(db)
	_, err = New(Config{Concurrency: 4}, store, nil,
		v5log.NewDefaultLogger(io.Discard, false, false, false))
	require.ErrorContains(t, err, "leave at least one")
}
