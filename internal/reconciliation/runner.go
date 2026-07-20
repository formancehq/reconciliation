// Package reconciliation owns the rule-evaluation module shared by the HTTP
// API and the durable worker. Callers only choose a trigger; locking, source
// reads, alert transitions, fencing and persistence remain behind Runner.
package reconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	ErrValidation = errors.New("validation error")
	ErrRuleBusy   = errors.New("rule evaluation already in progress")
)

const EngineErrorFingerprint = "engine.error"

var concurrentEvaluationsRefused metric.Int64Counter

func init() {
	concurrentEvaluationsRefused, _ = otel.Meter("github.com/formancehq/reconciliation/internal/reconciliation").
		Int64Counter("reconciliation.evaluations.concurrent_refused")
}

type EvaluateRequest struct {
	PIT          time.Time
	SafetyMargin time.Duration
	SourcePITs   map[string]time.Time
}

type Store interface {
	GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error)
	CreateEvaluation(ctx context.Context, ev *models.Evaluation) error
	OpenOrUpdateAlert(ctx context.Context, in storage.OpenAlertInput) (*storage.OpenAlertResult, error)
	AutoResolveAlert(ctx context.Context, ruleID uuid.UUID, fingerprint, periodID string, evaluationID uuid.UUID, at time.Time) (*models.Alert, error)
	ListActiveAlertFingerprints(ctx context.Context, ruleID uuid.UUID, periodID string) ([]string, error)
}

type Runner struct {
	store     Store
	engine    *engine.Engine
	templates *templates.Registry
	resolvers engine.Resolvers
}

func NewRunner(store Store, eng *engine.Engine, registry *templates.Registry, resolvers engine.Resolvers) *Runner {
	return &Runner{store: store, engine: eng, templates: registry, resolvers: resolvers}
}

func (r *Runner) Evaluate(ctx context.Context, ruleID uuid.UUID, req EvaluateRequest) (*models.Evaluation, error) {
	var evaluation *models.Evaluation
	acquired, err := r.withRuleLock(ctx, ruleID, func(ctx context.Context, store Store) error {
		rule, evaluator, err := r.loadRule(ctx, store, ruleID, nil)
		if err != nil {
			return err
		}
		if err := validateSourcePITs(evaluator, rule, req.SourcePITs); err != nil {
			return err
		}
		evaluation, err = r.run(ctx, store, rule, evaluator, req, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !acquired {
		concurrentEvaluationsRefused.Add(ctx, 1, metric.WithAttributes(attribute.String("trigger", "manual")))
		return nil, ErrRuleBusy
	}
	return evaluation, nil
}

// EvaluateScheduled evaluates one already-claimed occurrence. attempts is zero
// when the rule lock was busy, otherwise it is the persisted attempt number.
func (r *Runner) EvaluateScheduled(ctx context.Context, job *models.EvaluationJob) (*models.Evaluation, int, error) {
	if job == nil || job.ClaimToken == nil {
		return nil, 0, storage.ErrStaleClaim
	}
	var evaluation *models.Evaluation
	var attempts int
	acquired, err := r.withRuleLock(ctx, job.RuleID, func(ctx context.Context, store Store) error {
		jobs, ok := store.(interface {
			StartEvaluationJob(context.Context, uuid.UUID, uuid.UUID) (int, error)
		})
		if !ok {
			return errors.New("scheduled evaluation store is not job-aware")
		}
		var err error
		attempts, err = jobs.StartEvaluationJob(ctx, job.ID, *job.ClaimToken)
		if err != nil {
			return err
		}
		rule, evaluator, err := r.loadRule(ctx, store, job.RuleID, &job.RuleRevision)
		if err != nil {
			return err
		}
		margin := 30 * time.Second
		if rule.Schedule != nil && rule.Schedule.SafetyMargin > 0 {
			margin = rule.Schedule.SafetyMargin
		}
		revision := job.RuleRevision
		evaluation, err = r.run(ctx, store, rule, evaluator, EvaluateRequest{
			PIT: job.ScheduledAt, SafetyMargin: margin,
		}, &scheduledCompletion{job: job, revision: &revision})
		return err
	})
	if err != nil {
		return nil, attempts, err
	}
	if !acquired {
		concurrentEvaluationsRefused.Add(ctx, 1, metric.WithAttributes(attribute.String("trigger", "scheduled")))
		return nil, 0, ErrRuleBusy
	}
	return evaluation, attempts, nil
}

func (r *Runner) loadRule(ctx context.Context, store Store, id uuid.UUID, expectedRevision *int64) (*models.Rule, templates.Evaluator, error) {
	if r.engine == nil || r.templates == nil {
		return nil, nil, errors.New("reconciliation runner requires engine and templates")
	}
	rule, err := store.GetRule(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !rule.Enabled {
		if expectedRevision != nil {
			return nil, nil, storage.ErrObsoleteJob
		}
		return nil, nil, fmt.Errorf("%w: rule %s is disabled", ErrValidation, rule.ID)
	}
	if expectedRevision != nil && rule.Revision != *expectedRevision {
		return nil, nil, storage.ErrObsoleteJob
	}
	evaluator, err := r.templates.Get(rule.TemplateKind)
	return rule, evaluator, err
}

func (r *Runner) withRuleLock(ctx context.Context, id uuid.UUID, fn func(context.Context, Store) error) (bool, error) {
	type locker interface {
		TryWithRuleLock(context.Context, uuid.UUID, func(context.Context, *storage.Storage) error) (bool, error)
	}
	if lock, ok := r.store.(locker); ok {
		return lock.TryWithRuleLock(ctx, id, func(ctx context.Context, scoped *storage.Storage) error {
			return fn(ctx, scoped)
		})
	}
	return true, fn(ctx, r.store)
}

type scheduledCompletion struct {
	job      *models.EvaluationJob
	revision *int64
}

func (r *Runner) run(ctx context.Context, store Store, rule *models.Rule, evaluator templates.Evaluator, req EvaluateRequest, completion *scheduledCompletion) (*models.Evaluation, error) {
	pitExplicit := !req.PIT.IsZero()
	if req.PIT.IsZero() {
		req.PIT = time.Now().UTC()
	}

	evalCtx := ctx
	if wallClock := r.engine.MaxWallClock(); wallClock > 0 {
		var cancel context.CancelFunc
		evalCtx, cancel = context.WithTimeout(ctx, wallClock)
		defer cancel()
	}
	input := engine.EvalInput{PIT: req.PIT, PITExplicit: pitExplicit, SourcePITs: req.SourcePITs, SafetyMargin: req.SafetyMargin}
	sourcePITs, err := evaluator.SourcePITs(rule.TemplateSpec, input)
	if err != nil {
		return nil, err
	}
	started := time.Now().UTC()
	outcomes, evalErr := evaluator.Evaluate(evalCtx, rule.TemplateSpec, r.engine, r.resolvers, input)
	ended := time.Now().UTC()
	// A cancellation from the caller is an infrastructure/lifecycle failure,
	// not an engine result. In particular the worker cancels this context when
	// it can no longer renew the claim lease; persisting an ERROR at that point
	// would turn a lost lease into a successful job.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pitPerSource, mergeErr := mergePitPerSource(outcomes, sourcePITs)
	if mergeErr != nil {
		evalErr = mergeErr
	}

	evaluation := &models.Evaluation{
		ID: uuid.New(), RuleID: rule.ID, StartedAt: started, EndedAt: ended, PitPerSource: pitPerSource,
	}
	if completion != nil {
		scheduledAt := completion.job.ScheduledAt
		evaluation.ScheduledAt = &scheduledAt
		evaluation.RuleRevision = completion.revision
	}

	if evalErr != nil {
		evaluation.Result = models.EvaluationError
		evaluation.Error = evalErr.Error()
		evaluation.Evidence, err = marshalErrorEvidence(evalErr)
	} else {
		evaluation.Result = models.EvaluationPass
		for _, outcome := range outcomes {
			if !outcome.Passed {
				evaluation.Result = models.EvaluationFail
				break
			}
		}
		evaluation.Evidence, err = marshalOutcomes(outcomes)
	}
	if err != nil {
		return nil, err
	}

	err = runInTx(ctx, store, func(ctx context.Context, txStore Store) error {
		if completion != nil {
			jobs := txStore.(interface {
				AssertEvaluationJobClaim(context.Context, *models.EvaluationJob) error
				CompleteEvaluationJob(context.Context, uuid.UUID, uuid.UUID) error
			})
			if err := jobs.AssertEvaluationJobClaim(ctx, completion.job); err != nil {
				return err
			}
		}
		if err := txStore.CreateEvaluation(ctx, evaluation); err != nil {
			return err
		}
		if evalErr != nil {
			if _, err := openEngineErrorAlert(ctx, txStore, rule, evaluation, evalErr); err != nil {
				return err
			}
		} else {
			periodID := rule.Cadence.PeriodID(req.PIT.Add(-req.SafetyMargin))
			if err := driveAlerts(ctx, txStore, rule, evaluation, outcomes, periodID, ended); err != nil {
				return err
			}
			if _, err := txStore.AutoResolveAlert(ctx, rule.ID, EngineErrorFingerprint, models.ContinuousPeriod, evaluation.ID, ended); err != nil {
				return fmt.Errorf("auto-resolve engine.error: %w", err)
			}
		}
		if completion != nil {
			jobs := txStore.(interface {
				CompleteEvaluationJob(context.Context, uuid.UUID, uuid.UUID) error
			})
			return jobs.CompleteEvaluationJob(ctx, completion.job.ID, *completion.job.ClaimToken)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return evaluation, nil
}

func runInTx(ctx context.Context, store Store, fn func(context.Context, Store) error) error {
	type runner interface {
		RunInTx(context.Context, func(context.Context, *storage.Storage) error) error
	}
	if tx, ok := store.(runner); ok {
		return tx.RunInTx(ctx, func(ctx context.Context, scoped *storage.Storage) error { return fn(ctx, scoped) })
	}
	return fn(ctx, store)
}

func validateSourcePITs(evaluator templates.Evaluator, rule *models.Rule, overrides map[string]time.Time) error {
	if len(overrides) == 0 {
		return nil
	}
	keys, err := evaluator.SourceKeys(rule.TemplateSpec)
	if err != nil {
		return err
	}
	valid := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		valid[key] = struct{}{}
	}
	for key := range overrides {
		if _, ok := valid[key]; !ok {
			return fmt.Errorf("%w: sourcePITs: unknown source key %q for template %s (valid keys: %v)", ErrValidation, key, rule.TemplateKind, keys)
		}
	}
	return nil
}

func driveAlerts(ctx context.Context, store Store, rule *models.Rule, evaluation *models.Evaluation, outcomes []templates.Outcome, periodID string, ended time.Time) error {
	seen := make(map[string]struct{}, len(outcomes))
	for _, outcome := range outcomes {
		seen[outcome.Fingerprint] = struct{}{}
		if outcome.Passed {
			if _, err := store.AutoResolveAlert(ctx, rule.ID, outcome.Fingerprint, periodID, evaluation.ID, ended); err != nil {
				return err
			}
			continue
		}
		evidence, err := json.Marshal(outcome.Evidence)
		if err != nil {
			return err
		}
		if _, err := store.OpenOrUpdateAlert(ctx, storage.OpenAlertInput{
			RuleID: rule.ID, Fingerprint: outcome.Fingerprint, PeriodID: periodID,
			Severity: rule.Severity, EvaluationID: evaluation.ID, Evidence: evidence,
			Labels: rule.Labels, OccurredAt: ended,
		}); err != nil {
			return err
		}
	}
	active, err := store.ListActiveAlertFingerprints(ctx, rule.ID, periodID)
	if err != nil {
		return err
	}
	for _, fingerprint := range active {
		if _, ok := seen[fingerprint]; ok {
			continue
		}
		if _, err := store.AutoResolveAlert(ctx, rule.ID, fingerprint, periodID, evaluation.ID, ended); err != nil {
			return err
		}
	}
	return nil
}

func openEngineErrorAlert(ctx context.Context, store Store, rule *models.Rule, evaluation *models.Evaluation, evalErr error) (*models.Alert, error) {
	labels := make(map[string]string, len(rule.Labels)+1)
	for key, value := range rule.Labels {
		labels[key] = value
	}
	labels["kind"] = EngineErrorFingerprint
	evidence, err := json.Marshal(map[string]any{"error": evalErr.Error(), "evaluationId": evaluation.ID.String()})
	if err != nil {
		return nil, err
	}
	result, err := store.OpenOrUpdateAlert(ctx, storage.OpenAlertInput{
		RuleID: rule.ID, Fingerprint: EngineErrorFingerprint, PeriodID: models.ContinuousPeriod,
		Severity: models.SeverityHigh, EvaluationID: evaluation.ID, Evidence: evidence,
		Labels: labels, OccurredAt: evaluation.EndedAt,
	})
	if err != nil {
		return nil, err
	}
	return result.Alert, nil
}

func mergePitPerSource(outcomes []templates.Outcome, sourcePITs map[string]time.Time) (map[string]time.Time, error) {
	merged := make(map[string]time.Time, len(sourcePITs))
	for key, value := range sourcePITs {
		merged[key] = value
	}
	for _, outcome := range outcomes {
		for key, value := range outcome.PitPerSource {
			if current, ok := merged[key]; ok && !current.Equal(value) {
				return nil, fmt.Errorf("kernel/template contract violation: source %q reported PIT %s and %s", key, current, value)
			}
			merged[key] = value
		}
	}
	return merged, nil
}

func marshalOutcomes(outcomes []templates.Outcome) (json.RawMessage, error) {
	type encoded struct {
		Fingerprint string         `json:"fingerprint"`
		Passed      bool           `json:"passed"`
		Evidence    map[string]any `json:"evidence,omitempty"`
	}
	values := make([]encoded, 0, len(outcomes))
	for _, outcome := range outcomes {
		if !outcome.Passed {
			values = append(values, encoded{Fingerprint: outcome.Fingerprint, Passed: false, Evidence: outcome.Evidence})
		}
	}
	if len(values) == 0 {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(values)
}

func marshalErrorEvidence(err error) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"error": err.Error()})
}
