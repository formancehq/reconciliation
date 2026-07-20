package worker

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	legacyconnect "github.com/formancehq/go-libs/v3/bun/bunconnect"
	legacylogging "github.com/formancehq/go-libs/v3/logging"
	"github.com/formancehq/go-libs/v3/testing/docker"
	"github.com/formancehq/go-libs/v3/testing/platform/pgtesting"
	"github.com/formancehq/go-libs/v3/testing/utils"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	domain "github.com/formancehq/reconciliation/internal/reconciliation"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/storage/migrations"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

var workerPostgres *pgtesting.PostgresServer

func TestMain(m *testing.M) {
	utils.WithTestMain(func(t *utils.TestingTForMain) int {
		workerPostgres = pgtesting.CreatePostgresServer(t, docker.NewPool(t, legacylogging.Testing()))
		return m.Run()
	})
}

func newWorkerStore(t *testing.T) (*storage.Storage, *bun.DB) {
	t.Helper()
	server := workerPostgres.NewDatabase(t)
	db, err := legacyconnect.OpenSQLDB(context.Background(), server.ConnectionOptions())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, migrations.Migrate(context.Background(), db))
	return storage.NewStorage(db), db
}

type rollingEvaluator struct {
	calls        atomic.Int64
	firstStarted chan struct{}
}

func (*rollingEvaluator) Kind() models.TemplateKind                    { return models.TemplateLedgerInvariant }
func (*rollingEvaluator) Validate(json.RawMessage) error               { return nil }
func (*rollingEvaluator) Explain(json.RawMessage) (string, error)      { return "true", nil }
func (*rollingEvaluator) SourceKeys(json.RawMessage) ([]string, error) { return nil, nil }
func (e *rollingEvaluator) Evaluate(ctx context.Context, _ json.RawMessage, _ *engine.Engine, _ engine.Resolvers, _ engine.EvalInput) (*templates.EvaluationResult, error) {
	if e.calls.Add(1) == 1 {
		close(e.firstStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &templates.EvaluationResult{}, nil
}

type rollingLedger struct{}

func (rollingLedger) AggregateBalance(context.Context, string, json.RawMessage, time.Time) (map[string]*big.Int, error) {
	return nil, nil
}
func (rollingLedger) ListAccounts(context.Context, string, json.RawMessage, time.Time, int) ([]engine.Account, error) {
	return nil, nil
}

type rollingPayments struct{}

func (rollingPayments) PoolBalance(context.Context, string, *time.Time) (map[string]*big.Int, error) {
	return nil, nil
}

func TestTwoWorkersRollingRestartCommitsOccurrenceOnce(t *testing.T) {
	store, db := newWorkerStore(t)
	ctx := context.Background()
	rule := &models.Rule{
		ID: uuid.New(), Name: "rolling", TemplateKind: models.TemplateLedgerInvariant,
		TemplateSpec: json.RawMessage(`{}`), CompiledCEL: "true", Enabled: true,
		Severity: models.SeverityHigh, Cadence: models.CadenceContinuous,
		Schedule: &models.Schedule{Kind: models.ScheduleCron, Expr: "* * * * *", TZ: "UTC"},
	}
	require.NoError(t, store.CreateRule(ctx, rule))
	scheduledAt := time.Now().UTC().Truncate(time.Minute)
	_, err := db.NewUpdate().Model((*models.Rule)(nil)).
		Set("next_run_at = ?", scheduledAt).Where("id = ?", rule.ID).Exec(ctx)
	require.NoError(t, err)

	evaluator := &rollingEvaluator{firstStarted: make(chan struct{})}
	resolvers := engine.Resolvers{Ledger: rollingLedger{}, Payments: rollingPayments{}}
	eng, err := engine.New(resolvers, engine.DefaultLimits)
	require.NoError(t, err)
	runner := domain.NewRunner(store, eng, templates.NewRegistry(evaluator), resolvers)
	logger := v5log.NewDefaultLogger(io.Discard, false, false, false)
	config := Config{
		Concurrency: 1, PollingInterval: 5 * time.Millisecond,
		LeaseDuration: 200 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond,
		Retention: time.Hour, PlannerInterval: 20 * time.Millisecond,
	}
	first, err := New(config, store, runner, logger)
	require.NoError(t, err)
	firstCtx, stopFirst := context.WithCancel(context.Background())
	first.Start(firstCtx)
	select {
	case <-evaluator.firstStarted:
	case <-time.After(10 * time.Second):
		stopFirst()
		t.Fatal("first worker did not start the occurrence")
	}

	// Start the replacement before stopping the old worker, matching a rolling
	// Deployment update. The old worker releases its token on cancellation and
	// the replacement reclaims the same durable occurrence.
	second, err := New(config, store, runner, logger)
	require.NoError(t, err)
	secondCtx, stopSecond := context.WithCancel(context.Background())
	second.Start(secondCtx)
	stopFirst()
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelWait()
	require.NoError(t, first.Wait(waitCtx))

	require.Eventually(t, func() bool {
		count, countErr := db.NewSelect().Model((*models.Evaluation)(nil)).
			Where("rule_id = ? AND rule_revision = ? AND scheduled_at = ?", rule.ID, rule.Revision, scheduledAt).
			Count(context.Background())
		return countErr == nil && count == 1
	}, 10*time.Second, 20*time.Millisecond)

	stopSecond()
	require.NoError(t, second.Wait(waitCtx))
	count, err := db.NewSelect().Model((*models.Evaluation)(nil)).
		Where("rule_id = ? AND rule_revision = ? AND scheduled_at = ?", rule.ID, rule.Revision, scheduledAt).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
