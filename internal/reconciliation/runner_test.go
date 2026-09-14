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
	rule        *models.Rule
	evaluation  *models.Evaluation
	job         *models.EvaluationJob
	attempts    int
	completed   bool
	engineErr   bool
	alertInputs []storage.OpenAlertInput
}

func (s *runnerStore) GetRule(context.Context, uuid.UUID) (*models.Rule, error) {
	if s.rule == nil {
		return nil, storage.ErrNotFound
	}
	copy := *s.rule
	return &copy, nil
}

func (s *runnerStore) AssertRuleRevision(_ context.Context, id uuid.UUID, revision int64) error {
	if s.rule == nil {
		return storage.ErrNotFound
	}
	if s.rule.ID != id || !s.rule.Enabled || s.rule.Revision != revision {
		return storage.ErrObsoleteJob
	}
	return nil
}

func (s *runnerStore) CreateEvaluation(_ context.Context, evaluation *models.Evaluation) error {
	copy := *evaluation
	s.evaluation = &copy
	return nil
}

func (s *runnerStore) OpenOrUpdateAlert(_ context.Context, input storage.OpenAlertInput) (*storage.OpenAlertResult, error) {
	s.engineErr = true
	s.alertInputs = append(s.alertInputs, input)
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
	costUnits int64
	lastInput engine.EvalInput
	afterRead func()
}

func (*testEvaluator) Kind() models.TemplateKind                    { return models.TemplateKind("test") }
func (*testEvaluator) Validate(json.RawMessage) error               { return nil }
func (*testEvaluator) Explain(json.RawMessage) (string, error)      { return "true", nil }
func (*testEvaluator) SourceKeys(json.RawMessage) ([]string, error) { return []string{"source"}, nil }
func (e *testEvaluator) Evaluate(ctx context.Context, _ json.RawMessage, _ *engine.Engine, _ engine.Resolvers, in engine.EvalInput) (*templates.EvaluationResult, error) {
	e.lastInput = in
	result := &templates.EvaluationResult{
		PitPerSource: map[string]time.Time{"source": in.PIT.Add(-in.SafetyMargin)},
		CostUnits:    e.costUnits,
	}
	if e.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if e.afterRead != nil {
		e.afterRead()
	}
	return result, e.err
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
		PeriodType: models.PeriodTypeContinuous, Schedule: &models.Schedule{Kind: models.ScheduleCron},
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

func TestEngineErrorEvidenceIsStableAcrossEvaluations(t *testing.T) {
	store := &runnerStore{}
	rule := &models.Rule{ID: uuid.New(), Labels: map[string]string{"customer": "acme"}}
	errUpstream := errors.New("ledger upstream timeout")

	for range 2 {
		_, err := openEngineErrorAlert(context.Background(), store, rule, &models.Evaluation{
			ID: uuid.New(), EndedAt: time.Now().UTC(),
		}, errUpstream)
		require.NoError(t, err)
	}

	require.Len(t, store.alertInputs, 2)
	require.NotEqual(t, store.alertInputs[0].EvaluationID, store.alertInputs[1].EvaluationID)
	require.JSONEq(t, string(store.alertInputs[0].Evidence), string(store.alertInputs[1].Evidence))
	require.JSONEq(t, `{"error":"ledger upstream timeout"}`, string(store.alertInputs[0].Evidence))
}

func TestScheduledEvaluationPreservesExplicitZeroSafetyMargin(t *testing.T) {
	evaluator := &testEvaluator{}
	runner, store, job := newScheduledRunner(t, evaluator)
	store.rule.Schedule.SafetyMargin = 0
	store.rule.Schedule.SafetyMarginWasProvided = true

	evaluation, _, err := runner.EvaluateScheduled(context.Background(), job)
	require.NoError(t, err)
	require.NotNil(t, evaluation)
	require.Zero(t, evaluator.lastInput.SafetyMargin)
	require.Equal(t, job.ScheduledAt, store.evaluation.PitPerSource["source"])
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

func TestScheduledEvaluationPersistsCELRuntimeCost(t *testing.T) {
	evaluator := &testEvaluator{costUnits: 17}
	runner, store, job := newScheduledRunner(t, evaluator)

	evaluation, _, err := runner.EvaluateScheduled(context.Background(), job)
	require.NoError(t, err)
	require.Equal(t, int64(17), evaluation.CostUnits)
	require.Equal(t, int64(17), store.evaluation.CostUnits)
}

func TestManualEvaluationRejectsResultAfterRuleRevisionChanges(t *testing.T) {
	evaluator := &testEvaluator{}
	runner, store, job := newScheduledRunner(t, evaluator)
	evaluator.afterRead = func() {
		store.rule.Revision++
	}

	evaluation, err := runner.Evaluate(context.Background(), job.RuleID, EvaluateRequest{PIT: time.Now().UTC()})
	require.ErrorIs(t, err, ErrRuleChanged)
	require.Nil(t, evaluation)
	require.Nil(t, store.evaluation)
	require.False(t, store.engineErr)
}

func TestManualEvaluationRejectsSourcePITsWithoutCanonicalPIT(t *testing.T) {
	evaluator := &testEvaluator{}
	runner, _, job := newScheduledRunner(t, evaluator)

	evaluation, err := runner.Evaluate(context.Background(), job.RuleID, EvaluateRequest{
		SourcePITs: map[string]time.Time{"source": time.Now().Add(-time.Hour)},
	})
	require.ErrorIs(t, err, ErrValidation)
	require.ErrorContains(t, err, "at is required when sourcePITs are provided")
	require.Nil(t, evaluation)
}

// marshalOutcomes records the full per-fingerprint roster — passing AND failing.
// A PASS stores only the compact Proof (balance integers); a FAIL stores the
// full Evidence breakdown. So a green tick is a light self-contained proof, not
// an empty [] nor the heavy fail-shaped map.
func TestMarshalOutcomes_PassProofFailEvidence(t *testing.T) {
	raw, err := marshalOutcomes([]templates.Outcome{
		{
			Fingerprint: "asset:USD/2", Passed: true,
			Proof:    map[string]string{"balance": "523500"},
			Evidence: map[string]any{"balance": "523500", "compiledCEL": "…", "min": 100000}, // must NOT be stored on PASS
		},
		{
			Fingerprint: "asset:EUR/2", Passed: false,
			Evidence: map[string]any{"drift": "50", "compiledCEL": "…"},
		},
	})
	require.NoError(t, err)

	var got []struct {
		Fingerprint string            `json:"fingerprint"`
		Passed      bool              `json:"passed"`
		Proof       map[string]string `json:"proof"`
		Evidence    map[string]any    `json:"evidence"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got, 2)

	// PASS: compact proof only, no heavy evidence map.
	require.Equal(t, "asset:USD/2", got[0].Fingerprint)
	require.True(t, got[0].Passed)
	require.Equal(t, "523500", got[0].Proof["balance"])
	require.Empty(t, got[0].Evidence, "PASS must not store the full evidence map")

	// FAIL: full evidence, no proof.
	require.Equal(t, "asset:EUR/2", got[1].Fingerprint)
	require.False(t, got[1].Passed)
	require.Equal(t, "50", got[1].Evidence["drift"])
	require.Empty(t, got[1].Proof, "FAIL carries evidence, not proof")
}

// An evaluation that produced no outcomes (no assets matched) still serialises
// to an empty array, never null.
func TestMarshalOutcomes_NoOutcomesIsEmptyArray(t *testing.T) {
	raw, err := marshalOutcomes(nil)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, string(raw))
}
