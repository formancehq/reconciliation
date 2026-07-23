package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPlanScheduledJobsConcurrentPlannersCreateOneOccurrence(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("scheduled")
	rule.Schedule = &models.Schedule{Kind: models.ScheduleCron, Expr: "* * * * *", TZ: "UTC"}
	require.NoError(t, store.CreateRule(ctx, rule))

	// Force one deterministic due occurrence; both planners race for the same
	// rule row and the occurrence uniqueness is the second line of defence.
	due := time.Now().UTC().Truncate(time.Minute)
	_, err := store.db.NewUpdate().Model((*models.Rule)(nil)).Set("next_run_at = ?", due).Where("id = ?", rule.ID).Exec(ctx)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.PlanScheduledJobs(ctx, 24*time.Hour, 100, 100)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	count, err := store.db.NewSelect().Model((*models.EvaluationJob)(nil)).Where("rule_id = ?", rule.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestEvaluationJobLeaseReclaimFencesOldToken(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("lease")
	require.NoError(t, store.CreateRule(ctx, rule))
	now := time.Now().UTC()
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: now, Status: models.EvaluationJobPending, AvailableAt: now,
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)

	first, err := store.ClaimEvaluationJob(ctx, time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, first)
	time.Sleep(10 * time.Millisecond)
	second, err := store.ClaimEvaluationJob(ctx, time.Minute)
	require.NoError(t, err)
	require.True(t, second.Reclaimed)
	require.NotEqual(t, *first.Job.ClaimToken, *second.Job.ClaimToken)
	require.ErrorIs(t, store.CompleteEvaluationJob(ctx, job.ID, *first.Job.ClaimToken), ErrStaleClaim)
	require.NoError(t, store.CompleteEvaluationJob(ctx, job.ID, *second.Job.ClaimToken))
}

func TestExpiredLeaseCannotStartOrCommitWithoutReclaim(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("expired")
	require.NoError(t, store.CreateRule(ctx, rule))
	now := time.Now().UTC()
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: now, Status: models.EvaluationJobPending, AvailableAt: now,
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)
	claimed, err := store.ClaimEvaluationJob(ctx, time.Millisecond)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)
	require.ErrorIs(t, store.HeartbeatEvaluationJob(ctx, job.ID, *claimed.Job.ClaimToken, time.Minute), ErrStaleClaim)
	_, err = store.StartEvaluationJob(ctx, job.ID, *claimed.Job.ClaimToken)
	require.ErrorIs(t, err, ErrStaleClaim)
	require.ErrorIs(t, store.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
		return scoped.AssertEvaluationJobClaim(ctx, claimed.Job)
	}), ErrStaleClaim)
}

func TestReclaimedWorkerIsFencedFromCommittingEvaluation(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("rolling-restart")
	require.NoError(t, store.CreateRule(ctx, rule))
	scheduledAt := time.Now().UTC().Truncate(time.Second)
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: scheduledAt, Status: models.EvaluationJobPending, AvailableAt: scheduledAt,
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)

	oldWorker, err := store.ClaimEvaluationJob(ctx, time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, oldWorker)
	time.Sleep(10 * time.Millisecond)
	newWorker, err := store.ClaimEvaluationJob(ctx, time.Minute)
	require.NoError(t, err)
	require.True(t, newWorker.Reclaimed)

	commit := func(claim *ClaimedEvaluationJob) error {
		revision := claim.Job.RuleRevision
		evaluation := &models.Evaluation{
			ID: uuid.New(), RuleID: rule.ID, StartedAt: scheduledAt, EndedAt: scheduledAt,
			PitPerSource: map[string]time.Time{}, Result: models.EvaluationPass,
			ScheduledAt: &scheduledAt, RuleRevision: &revision,
		}
		return store.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
			if err := scoped.AssertEvaluationJobClaim(ctx, claim.Job); err != nil {
				return err
			}
			if err := scoped.CreateEvaluation(ctx, evaluation); err != nil {
				return err
			}
			return scoped.CompleteEvaluationJob(ctx, job.ID, *claim.Job.ClaimToken)
		})
	}

	require.NoError(t, commit(newWorker))
	require.ErrorIs(t, commit(oldWorker), ErrStaleClaim)
	count, err := store.db.NewSelect().Model((*models.Evaluation)(nil)).
		Where("rule_id = ? AND rule_revision = ? AND scheduled_at = ?", rule.ID, rule.Revision, scheduledAt).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestEvaluationJobClaimCompletesWithEvaluationAtomically(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("complete")
	require.NoError(t, store.CreateRule(ctx, rule))
	scheduledAt := time.Now().UTC().Truncate(time.Second)
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: scheduledAt, Status: models.EvaluationJobPending, AvailableAt: scheduledAt,
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)
	claimed, err := store.ClaimEvaluationJob(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	attempts, err := store.StartEvaluationJob(ctx, job.ID, *claimed.Job.ClaimToken)
	require.NoError(t, err)
	require.Equal(t, 1, attempts)
	revision := rule.Revision
	evaluation := &models.Evaluation{
		ID: uuid.New(), RuleID: rule.ID, StartedAt: scheduledAt, EndedAt: scheduledAt,
		PitPerSource: map[string]time.Time{}, Result: models.EvaluationPass,
		ScheduledAt: &scheduledAt, RuleRevision: &revision,
	}
	require.NoError(t, store.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
		if err := scoped.AssertEvaluationJobClaim(ctx, claimed.Job); err != nil {
			return err
		}
		if err := scoped.CreateEvaluation(ctx, evaluation); err != nil {
			return err
		}
		return scoped.CompleteEvaluationJob(ctx, job.ID, *claimed.Job.ClaimToken)
	}))
	stored, err := store.GetEvaluation(ctx, evaluation.ID)
	require.NoError(t, err)
	require.True(t, scheduledAt.Equal(*stored.ScheduledAt))
}

func TestConcurrentExecutorsClaimOccurrenceOnce(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("claim-once")
	require.NoError(t, store.CreateRule(ctx, rule))
	now := time.Now().UTC()
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: now, Status: models.EvaluationJobPending, AvailableAt: now,
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)

	var wg sync.WaitGroup
	claims := make(chan *ClaimedEvaluationJob, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := store.ClaimEvaluationJob(ctx, time.Minute)
			require.NoError(t, err)
			claims <- claimed
		}()
	}
	wg.Wait()
	close(claims)
	nonNil := 0
	for claim := range claims {
		if claim != nil {
			nonNil++
		}
	}
	require.Equal(t, 1, nonNil)
}

func TestPlanScheduledJobsBoundsCatchUpToMostRecentHundred(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("catch-up")
	rule.Schedule = &models.Schedule{Kind: models.ScheduleCron, Expr: "* * * * *", TZ: "UTC"}
	require.NoError(t, store.CreateRule(ctx, rule))
	due := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	_, err := store.db.NewUpdate().Model((*models.Rule)(nil)).Set("next_run_at = ?", due).Where("id = ?", rule.ID).Exec(ctx)
	require.NoError(t, err)

	result, err := store.PlanScheduledJobs(ctx, 24*time.Hour, 100, 100)
	require.NoError(t, err)
	require.Equal(t, 100, result.JobsCreated)
	require.Greater(t, result.Skipped, 0)
	count, err := store.db.NewSelect().Model((*models.EvaluationJob)(nil)).Where("rule_id = ?", rule.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 100, count)
}

func TestScheduledEvaluationOccurrenceIsUniqueAfterJobCompletion(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("dedupe")
	require.NoError(t, store.CreateRule(ctx, rule))
	scheduledAt := time.Now().UTC().Truncate(time.Second)
	revision := rule.Revision
	newEvaluation := func() *models.Evaluation {
		return &models.Evaluation{
			ID: uuid.New(), RuleID: rule.ID, StartedAt: scheduledAt, EndedAt: scheduledAt,
			PitPerSource: map[string]time.Time{}, Result: models.EvaluationPass,
			ScheduledAt: &scheduledAt, RuleRevision: &revision,
		}
	}
	require.NoError(t, store.CreateEvaluation(ctx, newEvaluation()))
	require.ErrorIs(t, store.CreateEvaluation(ctx, newEvaluation()), ErrDuplicateKeyValue)
}

func TestPatchRuleCancelsPendingJobsAndAdvancesRevision(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("revise")
	require.NoError(t, store.CreateRule(ctx, rule))
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: time.Now().UTC(), Status: models.EvaluationJobPending, AvailableAt: time.Now().UTC(),
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)
	name := "revised"
	require.NoError(t, store.PatchRule(ctx, rule.ID, RulePatch{Name: &name}))
	updated, err := store.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	require.Equal(t, rule.Revision+1, updated.Revision)
	var stored models.EvaluationJob
	require.NoError(t, store.db.NewSelect().Model(&stored).Where("id = ?", job.ID).Scan(ctx))
	require.Equal(t, models.EvaluationJobCancelled, stored.Status)
}

func TestPatchRuleFencesAlreadyRunningJob(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("running-revision")
	require.NoError(t, store.CreateRule(ctx, rule))
	now := time.Now().UTC()
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: now, Status: models.EvaluationJobPending, AvailableAt: now,
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)
	claimed, err := store.ClaimEvaluationJob(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed)

	disabled := false
	require.NoError(t, store.PatchRule(ctx, rule.ID, RulePatch{Enabled: &disabled}))
	require.ErrorIs(t, store.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
		return scoped.AssertEvaluationJobClaim(ctx, claimed.Job)
	}), ErrObsoleteJob)
}

func TestClaimValidationLocksRuleUntilScheduledCommitFinishes(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("atomic-revision-fence")
	require.NoError(t, store.CreateRule(ctx, rule))
	now := time.Now().UTC()
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: now, Status: models.EvaluationJobPending, AvailableAt: now,
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)
	claimed, err := store.ClaimEvaluationJob(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed)

	validated := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- store.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
			if err := scoped.AssertEvaluationJobClaim(ctx, claimed.Job); err != nil {
				return err
			}
			close(validated)
			<-release
			return nil
		})
	}()
	<-validated

	name := "revised-after-claim"
	patchDone := make(chan error, 1)
	go func() { patchDone <- store.PatchRule(ctx, rule.ID, RulePatch{Name: &name}) }()
	select {
	case err := <-patchDone:
		t.Fatalf("rule patch completed before the fenced transaction committed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-txDone)
	require.NoError(t, <-patchDone)
	updated, err := store.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	require.Equal(t, rule.Revision+1, updated.Revision)
}

func TestInfrastructureFailurePersistsAttemptWhenStartDidNot(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	rule := makeRule("start-failure")
	require.NoError(t, store.CreateRule(ctx, rule))
	now := time.Now().UTC()
	job := &models.EvaluationJob{
		ID: uuid.New(), RuleID: rule.ID, RuleRevision: rule.Revision,
		ScheduledAt: now, Status: models.EvaluationJobPending, AvailableAt: now,
	}
	_, err := store.db.NewInsert().Model(job).Exec(ctx)
	require.NoError(t, err)
	claimed, err := store.ClaimEvaluationJob(ctx, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.NoError(t, store.FailEvaluationJob(ctx, job.ID, *claimed.Job.ClaimToken, 1, 5, time.Second, errors.New("database unavailable")))

	var stored models.EvaluationJob
	require.NoError(t, store.db.NewSelect().Model(&stored).Where("id = ?", job.ID).Scan(ctx))
	require.Equal(t, 1, stored.Attempts)
	require.Equal(t, models.EvaluationJobPending, stored.Status)
}
