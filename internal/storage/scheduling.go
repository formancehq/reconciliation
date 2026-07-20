package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type PlanResult struct {
	RulesScanned int
	JobsCreated  int
	Skipped      int
	Initialized  int
	MaxLag       time.Duration
}

// PlanScheduledJobs materializes due cron occurrences and advances each rule's
// durable cursor in the same transaction. SKIP LOCKED lets any number of
// planners share the work without duplicate rows or cross-pod coordination.
func (s *Storage) PlanScheduledJobs(ctx context.Context, catchUpWindow time.Duration, maxOccurrences, batchSize int) (PlanResult, error) {
	if catchUpWindow <= 0 {
		catchUpWindow = 24 * time.Hour
	}
	if maxOccurrences <= 0 {
		maxOccurrences = 100
	}
	if batchSize <= 0 {
		batchSize = 100
	}

	var result PlanResult
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var now time.Time
		if err := tx.NewSelect().ColumnExpr("now()").Scan(ctx, &now); err != nil {
			return fmt.Errorf("read database time: %w", err)
		}
		now = now.UTC()

		var rules []models.Rule
		if err := tx.NewSelect().Model(&rules).
			Where("enabled = TRUE").
			Where("schedule->>'kind' = ?", string(models.ScheduleCron)).
			Where("next_run_at IS NULL OR next_run_at <= ?", now).
			OrderExpr("next_run_at ASC NULLS FIRST").
			Limit(batchSize).
			For("UPDATE SKIP LOCKED").
			Scan(ctx); err != nil {
			return fmt.Errorf("select due schedules: %w", err)
		}

		result.RulesScanned = len(rules)
		for i := range rules {
			rule := &rules[i]
			if rule.Schedule == nil {
				continue
			}

			// Existing rules have no trustworthy pre-v3 cursor. Initialize them
			// at the first future occurrence rather than manufacturing a backlog.
			if rule.NextRunAt == nil {
				next, err := rule.Schedule.Next(now)
				if err != nil {
					return err
				}
				if _, err := tx.NewUpdate().Model((*models.Rule)(nil)).
					Set("next_run_at = ?", nullTime(next)).
					Where("id = ?", rule.ID).Exec(ctx); err != nil {
					return fmt.Errorf("initialize schedule %s: %w", rule.ID, err)
				}
				result.Initialized++
				continue
			}
			if lag := now.Sub(rule.NextRunAt.UTC()); lag > result.MaxLag {
				result.MaxLag = lag
			}

			cutoff := now.Add(-catchUpWindow)
			occurrence := rule.NextRunAt.UTC()
			if occurrence.Before(cutoff) {
				result.Skipped++ // at least one pre-window occurrence was discarded
				var err error
				occurrence, err = rule.Schedule.Next(cutoff.Add(-time.Nanosecond))
				if err != nil {
					return err
				}
			}

			occurrences := make([]time.Time, 0, maxOccurrences)
			for !occurrence.IsZero() && !occurrence.After(now) {
				if len(occurrences) == maxOccurrences {
					copy(occurrences, occurrences[1:])
					occurrences[len(occurrences)-1] = occurrence
					result.Skipped++
				} else {
					occurrences = append(occurrences, occurrence)
				}
				var err error
				occurrence, err = rule.Schedule.Next(occurrence)
				if err != nil {
					return err
				}
			}

			for _, scheduledAt := range occurrences {
				job := &models.EvaluationJob{
					ID:           uuid.New(),
					RuleID:       rule.ID,
					RuleRevision: rule.Revision,
					ScheduledAt:  scheduledAt,
					Status:       models.EvaluationJobPending,
					AvailableAt:  now,
				}
				res, err := tx.NewInsert().Model(job).
					On("CONFLICT (rule_id, rule_revision, scheduled_at) DO NOTHING").
					Exec(ctx)
				if err != nil {
					return fmt.Errorf("insert scheduled job: %w", err)
				}
				if n, _ := res.RowsAffected(); n > 0 {
					result.JobsCreated++
				}
			}

			if _, err := tx.NewUpdate().Model((*models.Rule)(nil)).
				Set("next_run_at = ?", nullTime(occurrence)).
				Where("id = ?", rule.ID).Exec(ctx); err != nil {
				return fmt.Errorf("advance schedule %s: %w", rule.ID, err)
			}
		}
		return nil
	})
	return result, err
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

type ClaimedEvaluationJob struct {
	Job       *models.EvaluationJob
	Reclaimed bool
}

func (s *Storage) ClaimEvaluationJob(ctx context.Context, lease time.Duration) (*ClaimedEvaluationJob, error) {
	var claimed *ClaimedEvaluationJob
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var now time.Time
		if err := tx.NewSelect().ColumnExpr("now()").Scan(ctx, &now); err != nil {
			return err
		}
		var job models.EvaluationJob
		err := tx.NewSelect().Model(&job).
			Where("(status = ? AND available_at <= ?) OR (status = ? AND lease_until < ?)",
				models.EvaluationJobPending, now, models.EvaluationJobRunning, now).
			OrderExpr("scheduled_at ASC").
			Limit(1).
			For("UPDATE SKIP LOCKED").
			Scan(ctx)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			// Bun returns sql.ErrNoRows; normalize through e for callers.
			if errors.Is(e("claim job", err), ErrNotFound) {
				return nil
			}
			return err
		}

		reclaimed := job.Status == models.EvaluationJobRunning
		token := uuid.New()
		leaseUntil := now.Add(lease)
		if _, err := tx.NewUpdate().Model((*models.EvaluationJob)(nil)).
			Set("status = ?", models.EvaluationJobRunning).
			Set("claim_token = ?", token).
			Set("lease_until = ?", leaseUntil).
			Where("id = ?", job.ID).Exec(ctx); err != nil {
			return err
		}
		job.Status = models.EvaluationJobRunning
		job.ClaimToken = &token
		job.LeaseUntil = &leaseUntil
		claimed = &ClaimedEvaluationJob{Job: &job, Reclaimed: reclaimed}
		return nil
	})
	return claimed, err
}

func (s *Storage) StartEvaluationJob(ctx context.Context, id, token uuid.UUID) (int, error) {
	var attempts int
	err := s.db.NewUpdate().Model((*models.EvaluationJob)(nil)).
		Set("attempts = attempts + 1").
		Where("id = ? AND status = ? AND claim_token = ? AND lease_until > now()", id, models.EvaluationJobRunning, token).
		Returning("attempts").
		Scan(ctx, &attempts)
	if err != nil {
		normalized := e("start evaluation job", err)
		if errors.Is(normalized, ErrNotFound) {
			return 0, ErrStaleClaim
		}
		return 0, normalized
	}
	return attempts, nil
}

func (s *Storage) HeartbeatEvaluationJob(ctx context.Context, id, token uuid.UUID, lease time.Duration) error {
	res, err := s.db.NewUpdate().Model((*models.EvaluationJob)(nil)).
		Set("lease_until = now() + (? * interval '1 microsecond')", lease.Microseconds()).
		Where("id = ? AND status = ? AND claim_token = ? AND lease_until > now()", id, models.EvaluationJobRunning, token).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleClaim
	}
	return nil
}

func (s *Storage) ReleaseEvaluationJob(ctx context.Context, id, token uuid.UUID, delay time.Duration) error {
	res, err := s.db.NewUpdate().Model((*models.EvaluationJob)(nil)).
		Set("status = ?", models.EvaluationJobPending).
		Set("available_at = now() + (? * interval '1 microsecond')", delay.Microseconds()).
		Set("claim_token = NULL").Set("lease_until = NULL").
		Where("id = ? AND status = ? AND claim_token = ?", id, models.EvaluationJobRunning, token).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleClaim
	}
	return nil
}

func (s *Storage) FailEvaluationJob(ctx context.Context, id, token uuid.UUID, attempts, maxAttempts int, retryAfter time.Duration, cause error) error {
	status := models.EvaluationJobPending
	if attempts >= maxAttempts {
		status = models.EvaluationJobFailed
	}
	q := s.db.NewUpdate().Model((*models.EvaluationJob)(nil)).
		Set("status = ?", status).
		// StartEvaluationJob normally persists the increment. GREATEST also
		// covers failures while starting an attempt, where the caller must still
		// consume one retry without risking an endless attempts=0 loop.
		Set("attempts = GREATEST(attempts, ?)", attempts).
		Set("last_error = ?", cause.Error()).
		Set("claim_token = NULL").Set("lease_until = NULL").
		Where("id = ? AND status = ? AND claim_token = ?", id, models.EvaluationJobRunning, token)
	if status == models.EvaluationJobPending {
		q = q.Set("available_at = now() + (? * interval '1 microsecond')", retryAfter.Microseconds())
	}
	res, err := q.Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleClaim
	}
	return nil
}

// AssertEvaluationJobClaim locks and validates the durable fencing state. It
// must be called inside the same transaction that writes the evaluation.
func (s *Storage) AssertEvaluationJobClaim(ctx context.Context, job *models.EvaluationJob) error {
	var current models.EvaluationJob
	if err := s.db.NewSelect().Model(&current).Where("id = ?", job.ID).For("UPDATE").Scan(ctx); err != nil {
		return e("load evaluation job claim", err)
	}
	if current.Status != models.EvaluationJobRunning || current.ClaimToken == nil || job.ClaimToken == nil || *current.ClaimToken != *job.ClaimToken {
		return ErrStaleClaim
	}
	var now time.Time
	if err := s.db.NewSelect().ColumnExpr("now()").Scan(ctx, &now); err != nil {
		return fmt.Errorf("read database time for evaluation claim: %w", err)
	}
	if current.LeaseUntil == nil || !current.LeaseUntil.After(now) {
		return ErrStaleClaim
	}
	var revision int64
	var enabled bool
	if err := s.db.NewSelect().Model((*models.Rule)(nil)).
		Column("revision", "enabled").Where("id = ?", job.RuleID).Scan(ctx, &revision, &enabled); err != nil {
		return e("load rule revision", err)
	}
	if !enabled || revision != job.RuleRevision {
		return ErrObsoleteJob
	}
	return nil
}

func (s *Storage) CompleteEvaluationJob(ctx context.Context, id, token uuid.UUID) error {
	res, err := s.db.NewUpdate().Model((*models.EvaluationJob)(nil)).
		Set("status = ?", models.EvaluationJobSucceeded).
		Set("claim_token = NULL").Set("lease_until = NULL").Set("last_error = NULL").
		Where("id = ? AND status = ? AND claim_token = ?", id, models.EvaluationJobRunning, token).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleClaim
	}
	return nil
}

func (s *Storage) CancelEvaluationJob(ctx context.Context, id, token uuid.UUID, cause string) error {
	_, err := s.db.NewUpdate().Model((*models.EvaluationJob)(nil)).
		Set("status = ?", models.EvaluationJobCancelled).
		Set("last_error = ?", cause).
		Set("claim_token = NULL").Set("lease_until = NULL").
		Where("id = ? AND claim_token = ?", id, token).Exec(ctx)
	return err
}

func (s *Storage) DeleteTerminalEvaluationJobs(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := s.db.NewDelete().Model((*models.EvaluationJob)(nil)).
		Where("status IN (?, ?, ?)", models.EvaluationJobSucceeded, models.EvaluationJobFailed, models.EvaluationJobCancelled).
		Where("updated_at < now() - (? * interval '1 microsecond')", retention.Microseconds()).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Storage) CountEvaluationJobsByStatus(ctx context.Context) (map[models.EvaluationJobStatus]int64, error) {
	type row struct {
		Status models.EvaluationJobStatus
		Count  int64
	}
	var rows []row
	if err := s.db.NewSelect().Model((*models.EvaluationJob)(nil)).
		Column("status").ColumnExpr("count(*) AS count").
		Where("status IN (?, ?, ?)", models.EvaluationJobPending, models.EvaluationJobRunning, models.EvaluationJobFailed).
		Group("status").Scan(ctx, &rows); err != nil {
		return nil, err
	}
	counts := make(map[models.EvaluationJobStatus]int64, len(rows))
	for _, row := range rows {
		counts[row.Status] = row.Count
	}
	return counts, nil
}
