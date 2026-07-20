package reconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type runnerStore struct {
	rule       *models.Rule
	evaluation *models.Evaluation
	job        *models.EvaluationJob
	attempts   int
	completed  bool
	engineErr  bool
}

func (s *runnerStore) GetRule(context.Context, uuid.UUID) (*models.Rule, error) {
	if s.rule == nil {
		return nil, storage.ErrNotFound
	}
	copy := *s.rule
	return &copy, nil
}

func (s *runnerStore) CreateEvaluation(_ context.Context, evaluation *models.Evaluation) error {
	copy := *evaluation
	s.evaluation = &copy
	return nil
}

func (s *runnerStore) OpenOrUpdateAlert(context.Context, storage.OpenAlertInput) (*storage.OpenAlertResult, error) {
	s.engineErr = true
	return &storage.OpenAlertResult{Alert: &models.Alert{}}, nil
}

func (*runnerStore) AutoResolveAlert(context.Context, uuid.UUID, string, string, uuid.UUID, time.Time) (*models.Alert, error) {
	return nil, nil
}

func (*runnerStore) ListActiveAlertFingerprints(context.Context, uuid.UUID, string) ([]string, error) {
	return nil, nil
}

func (s *runnerStore) StartEvaluationJob(_ context.Context, id, token uuid.UUID) (int, error) {
	if s.job.ID != id || s.job.ClaimToken == nil || *s.job.ClaimToken != token {
		return 0, storage.ErrStaleClaim
	}
	s.attempts++
	return s.attempts, nil
}

func (s *runnerStore) AssertEvaluationJobClaim(_ context.Context, job *models.EvaluationJob) error {
	if s.job.ID != job.ID || s.job.ClaimToken == nil || job.ClaimToken == nil || *s.job.ClaimToken != *job.ClaimToken {
		return storage.ErrStaleClaim
	}
	return nil
}

func (s *runnerStore) CompleteEvaluationJob(_ context.Context, id, token uuid.UUID) error {
	if s.job.ID != id || s.job.ClaimToken == nil || *s.job.ClaimToken != token {
		return storage.ErrStaleClaim
	}
	s.completed = true
	return nil
}

type testEvaluator struct {
	err       error
	wait      bool
	lastInput engine.EvalInput
}

func (*testEvaluator) Kind() models.TemplateKind                    { return models.TemplateKind("test") }
func (*testEvaluator) Validate(json.RawMessage) error               { return nil }
func (*testEvaluator) Explain(json.RawMessage) (string, error)      { return "true", nil }
func (*testEvaluator) SourceKeys(json.RawMessage) ([]string, error) { return []string{"source"}, nil }
func (e *testEvaluator) SourcePITs(_ json.RawMessage, in engine.EvalInput) (map[string]time.Time, error) {
	e.lastInput = in
	return map[string]time.Time{"source": in.PIT.Add(-in.SafetyMargin)}, nil
}
func (e *testEvaluator) Evaluate(ctx context.Context, _ json.RawMessage, _ *engine.Engine, _ engine.Resolvers, in engine.EvalInput) ([]templates.Outcome, error) {
	e.lastInput = in
	if e.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, e.err
}

type testLedgerResolver struct{}

func (testLedgerResolver) AggregateBalance(context.Context, string, json.RawMessage, time.Time) (map[string]*big.Int, error) {
	return nil, nil
}
func (testLedgerResolver) ListAccounts(context.Context, string, json.RawMessage, time.Time, int) ([]engine.Account, error) {
	return nil, nil
}

type testPaymentsResolver struct{}

func (testPaymentsResolver) PoolBalance(context.Context, string, *time.Time) (map[string]*big.Int, error) {
	return nil, nil
}

func newScheduledRunner(t *testing.T, evaluator *testEvaluator) (*Runner, *runnerStore, *models.EvaluationJob) {
	t.Helper()
	token := uuid.New()
	scheduledAt := time.Date(2026, 7, 20, 9, 30, 0, 0, time.UTC)
	rule := &models.Rule{
		ID: uuid.New(), Enabled: true, Revision: 7, TemplateKind: evaluator.Kind(),
		Cadence: models.CadenceContinuous, Schedule: &models.Schedule{Kind: models.ScheduleCron},
	}
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: scheduledAt, Status: models.EvaluationJobRunning, ClaimToken: &token,
	}
	store := &runnerStore{rule: rule, job: job}
	resolvers := engine.Resolvers{Ledger: testLedgerResolver{}, Payments: testPaymentsResolver{}}
	eng, err := engine.New(resolvers, engine.DefaultLimits)
	require.NoError(t, err)
	return NewRunner(store, eng, templates.NewRegistry(evaluator), resolvers), store, job
}

func TestScheduledEngineErrorIsPersistedAndCompletesJob(t *testing.T) {
	evaluator := &testEvaluator{err: errors.New("resolver failed")}
	runner, store, job := newScheduledRunner(t, evaluator)

	evaluation, attempts, err := runner.EvaluateScheduled(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, 1, attempts)
	require.Equal(t, models.EvaluationError, evaluation.Result)
	require.True(t, store.completed)
	require.True(t, store.engineErr)
	require.Equal(t, job.ScheduledAt, evaluator.lastInput.PIT)
	require.Equal(t, 30*time.Second, evaluator.lastInput.SafetyMargin)
	require.Equal(t, job.ScheduledAt.Add(-30*time.Second), store.evaluation.PitPerSource["source"])
}

func TestCancelledScheduledEvaluationDoesNotCommitEngineError(t *testing.T) {
	evaluator := &testEvaluator{wait: true}
	runner, store, job := newScheduledRunner(t, evaluator)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	evaluation, attempts, err := runner.EvaluateScheduled(ctx, job)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, evaluation)
	require.Equal(t, 1, attempts)
	require.Nil(t, store.evaluation)
	require.False(t, store.completed)
	require.False(t, store.engineErr)
}
