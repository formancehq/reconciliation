package worker

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	logging "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
	domain "github.com/formancehq/reconciliation/internal/reconciliation"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

const (
	catchUpWindow      = 24 * time.Hour
	maxCatchUpPerRule  = 100
	plannerBatchSize   = 100
	maxAttempts        = 5
	ruleBusyRetryDelay = 5 * time.Second
	cleanupInterval    = 24 * time.Hour
	metricsInterval    = 15 * time.Second
	claimSettleTimeout = 10 * time.Second
)

type Worker struct {
	config  Config
	store   *storage.Storage
	runner  *domain.Runner
	logger  logging.Logger
	metrics *workerMetrics
	wg      sync.WaitGroup
}

func New(config Config, store *storage.Storage, runner *domain.Runner, logger logging.Logger) (*Worker, error) {
	if config.Concurrency <= 0 {
		config.Concurrency = 4
	}
	if config.PollingInterval <= 0 {
		config.PollingInterval = time.Second
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 2 * time.Minute
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 30 * time.Second
		if config.HeartbeatInterval >= config.LeaseDuration {
			config.HeartbeatInterval = config.LeaseDuration / 4
		}
	}
	if config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseDuration {
		return nil, fmt.Errorf("worker heartbeat interval %s must be shorter than lease %s", config.HeartbeatInterval, config.LeaseDuration)
	}
	if config.Retention <= 0 {
		config.Retention = 30 * 24 * time.Hour
	}
	if config.PlannerInterval <= 0 {
		config.PlannerInterval = time.Minute
	}
	if store != nil {
		maxOpen := store.MaxOpenConnections()
		if maxOpen > 0 && config.Concurrency >= maxOpen {
			return nil, fmt.Errorf(
				"worker concurrency %d must leave at least one of %d PostgreSQL connections available for lease heartbeats",
				config.Concurrency, maxOpen,
			)
		}
	}
	metrics, err := newWorkerMetrics()
	if err != nil {
		return nil, err
	}
	return &Worker{config: config, store: store, runner: runner, logger: logger, metrics: metrics}, nil
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Infof("reconciliation worker started (concurrency=%d)", w.config.Concurrency)
	w.wg.Add(1)
	go w.plannerLoop(ctx)
	for range w.config.Concurrency {
		w.wg.Add(1)
		go w.executorLoop(ctx)
	}
	w.wg.Add(1)
	go w.cleanupLoop(ctx)
	w.wg.Add(1)
	go w.metricsLoop(ctx)
}

func (w *Worker) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()
	select {
	case <-done:
		w.logger.Infof("reconciliation worker stopped")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) plannerLoop(ctx context.Context) {
	defer w.wg.Done()
	w.plan(ctx)
	ticker := time.NewTicker(w.config.PlannerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.plan(ctx)
		}
	}
}

func (w *Worker) plan(ctx context.Context) {
	ctx = audit.WithSystemSubject(ctx, audit.ComponentScheduler)
	var total storage.PlanResult
	for ctx.Err() == nil {
		result, err := w.store.PlanScheduledJobs(ctx, catchUpWindow, maxCatchUpPerRule, plannerBatchSize)
		if err != nil {
			w.logger.Errorf("scheduler planner failed: %s", err)
			return
		}
		total.RulesScanned += result.RulesScanned
		total.JobsCreated += result.JobsCreated
		total.Skipped += result.Skipped
		total.Initialized += result.Initialized
		if result.MaxLag > total.MaxLag {
			total.MaxLag = result.MaxLag
		}
		// Drain every currently-due batch during this planner pass. Without
		// this, more than 100 rules would add one minute of lag per batch.
		if result.RulesScanned < plannerBatchSize {
			break
		}
	}
	if total.JobsCreated > 0 || total.Initialized > 0 {
		w.logger.WithFields(map[string]any{
			"jobsCreated": total.JobsCreated, "initialized": total.Initialized,
			"rulesScanned": total.RulesScanned,
		}).Infof("scheduler planner materialized occurrences")
	}
	if total.Skipped > 0 {
		w.logger.WithFields(map[string]any{
			"occurrencesSkipped": total.Skipped,
			"catchUpWindow":      catchUpWindow.String(),
			"maxPerRule":         maxCatchUpPerRule,
		}).Infof("scheduler planner dropped occurrences outside the catch-up limits")
	}
	w.metrics.jobsCreated.Add(ctx, int64(total.JobsCreated))
	w.metrics.skipped.Add(ctx, int64(total.Skipped))
	w.metrics.plannerLag.Record(ctx, total.MaxLag.Seconds())
}

func (w *Worker) executorLoop(ctx context.Context) {
	defer w.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		claimed, err := w.store.ClaimEvaluationJob(ctx, w.config.LeaseDuration)
		if err != nil {
			w.logger.Errorf("claim evaluation job: %s", err)
			if !sleepWithContext(ctx, w.jitteredPolling()) {
				return
			}
			continue
		}
		if claimed == nil {
			if !sleepWithContext(ctx, w.jitteredPolling()) {
				return
			}
			continue
		}
		w.metrics.jobsClaimed.Add(ctx, 1)
		if claimed.Reclaimed {
			w.metrics.jobsReclaimed.Add(ctx, 1)
		}
		w.process(ctx, claimed)
	}
}

func (w *Worker) process(parent context.Context, claimed *storage.ClaimedEvaluationJob) {
	job := claimed.Job
	if job.ClaimToken == nil {
		return
	}
	token := *job.ClaimToken
	// Everything this job journals is attributed to the scheduler. A system
	// component hashes distinctly from a caller-less request, so "no human was
	// involved in this control run" is a checkable property of the record rather
	// than an inference from a blank field.
	parent = audit.WithSystemSubject(parent, audit.ComponentScheduler)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	heartbeatErr := make(chan error, 1)
	go w.heartbeat(ctx, job.ID, token, cancel, heartbeatErr)

	_, attempts, err := w.runner.EvaluateScheduled(ctx, job)
	cancel()
	select {
	case hbErr := <-heartbeatErr:
		if hbErr != nil {
			err = hbErr
		}
	default:
	}

	if err == nil {
		return
	}
	// The evaluation context was cancelled above to stop the heartbeat. Use a
	// short, cancellation-independent context for the durable claim transition;
	// otherwise every requeue/failure update would immediately fail with
	// context.Canceled and wait for lease expiry instead.
	settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(parent), claimSettleTimeout)
	defer settleCancel()
	if parent.Err() != nil {
		if releaseErr := w.store.ReleaseEvaluationJob(settleCtx, job.ID, token, 0); releaseErr != nil && !errors.Is(releaseErr, storage.ErrStaleClaim) {
			w.logger.Errorf("release evaluation job %s during shutdown: %s", job.ID, releaseErr)
		}
		return
	}
	if errors.Is(err, domain.ErrRuleBusy) {
		w.metrics.rulesBusy.Add(settleCtx, 1)
		if releaseErr := w.store.ReleaseEvaluationJob(settleCtx, job.ID, token, ruleBusyRetryDelay); releaseErr != nil && !errors.Is(releaseErr, storage.ErrStaleClaim) {
			w.logger.Errorf("requeue busy evaluation job %s: %s", job.ID, releaseErr)
		}
		return
	}
	if errors.Is(err, storage.ErrObsoleteJob) {
		if cancelErr := w.store.CancelEvaluationJob(settleCtx, job.ID, token, err.Error()); cancelErr != nil {
			w.logger.Errorf("cancel obsolete evaluation job %s: %s", job.ID, cancelErr)
		}
		return
	}
	if errors.Is(err, storage.ErrStaleClaim) || errors.Is(err, storage.ErrNotFound) {
		return
	}
	if attempts == 0 {
		attempts = job.Attempts + 1
	}
	retry := retryDelay(attempts)
	if attempts >= maxAttempts {
		w.metrics.jobsFailed.Add(settleCtx, 1)
	} else {
		w.metrics.jobsRetried.Add(settleCtx, 1)
	}
	if failErr := w.store.FailEvaluationJob(settleCtx, job.ID, token, attempts, maxAttempts, retry, err); failErr != nil && !errors.Is(failErr, storage.ErrStaleClaim) {
		w.logger.Errorf("record evaluation job failure %s: %s", job.ID, failErr)
	}
	w.logger.WithFields(map[string]any{"job": job.ID, "rule": job.RuleID, "attempt": attempts}).Errorf("scheduled evaluation failed: %s", err)
}

func (w *Worker) recordJobCounts(ctx context.Context) {
	counts, err := w.store.CountEvaluationJobsByStatus(ctx)
	if err != nil {
		w.logger.Errorf("count evaluation jobs: %s", err)
		return
	}
	for _, status := range []models.EvaluationJobStatus{
		models.EvaluationJobPending, models.EvaluationJobRunning, models.EvaluationJobFailed,
	} {
		w.metrics.recordStatus(ctx, string(status), counts[status])
	}
}

func (w *Worker) metricsLoop(ctx context.Context) {
	defer w.wg.Done()
	w.recordJobCounts(ctx)
	ticker := time.NewTicker(metricsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.recordJobCounts(ctx)
		}
	}
}

func (w *Worker) heartbeat(ctx context.Context, id, token uuid.UUID, cancel context.CancelFunc, result chan<- error) {
	ticker := time.NewTicker(w.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			if err := w.store.HeartbeatEvaluationJob(ctx, id, token, w.config.LeaseDuration); err != nil {
				result <- err
				cancel()
				return
			}
		}
	}
}

func (w *Worker) cleanupLoop(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := w.store.DeleteTerminalEvaluationJobs(ctx, w.config.Retention)
			if err != nil {
				w.logger.Errorf("cleanup terminal evaluation jobs: %s", err)
			} else if deleted > 0 {
				w.logger.Infof("cleaned %d terminal evaluation jobs", deleted)
			}
		}
	}
}

func retryDelay(attempt int) time.Duration {
	delay := 5 * time.Second
	for i := 1; i < attempt && delay < 5*time.Minute; i++ {
		delay *= 2
	}
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func (w *Worker) jitteredPolling() time.Duration {
	// +/-20% avoids synchronized empty-queue polling across replicas.
	base := w.config.PollingInterval
	delta := base / 5
	if delta <= 0 {
		return base
	}
	return base - delta + time.Duration(rand.Int64N(int64(2*delta)+1))
}

func sleepWithContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
