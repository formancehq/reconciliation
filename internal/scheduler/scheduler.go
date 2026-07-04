// Package scheduler runs cron-scheduled rule evaluations in-process.
//
// V1 MVP / design-partner scope: a single goroutine ticks once a minute, lists
// enabled rules with a cron schedule, and fires EvaluateRule for any whose cron
// expression came due in the last tick window. It is the automated counterpart
// to the on-demand POST /rules/{id}/evaluate.
//
// SINGLE-ACTIVE-INSTANCE ASSUMPTION. This scheduler fires on every process it
// runs in. With multiple replicas it would fire each schedule N times (N×
// evaluations, N× webhooks). For V1 it must run on exactly one instance (or be
// left disabled). Multi-replica safety — a Postgres advisory lock or a move to
// Temporal schedules — is the planned follow-up; see docs/technical/scheduler.md.
package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// RuleService is the slice of the service the scheduler needs: enumerate rules
// and evaluate one. Satisfied by *service.Service (and backend.Service).
type RuleService interface {
	ListRules(ctx context.Context, q store.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error)
	EvaluateRule(ctx context.Context, id uuid.UUID, req service.EvaluateRuleRequest) (*models.Evaluation, error)
}

// maxRulesPerTick caps how many rules a single tick enumerates. If a deployment
// exceeds it the scheduler logs a warning rather than silently skipping rules.
const maxRulesPerTick = 1000

// Scheduler periodically fires due cron-scheduled rule evaluations.
type Scheduler struct {
	svc      RuleService
	interval time.Duration
	logger   v5log.Logger
	now      func() time.Time // injectable for tests
}

// New builds a Scheduler. interval is the tick granularity (cron is
// minute-granular, so 1m is the natural value).
func New(svc RuleService, interval time.Duration, logger v5log.Logger) *Scheduler {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Scheduler{svc: svc, interval: interval, logger: logger, now: time.Now}
}

// Run loops until ctx is cancelled, ticking every interval. Each tick fires any
// rule whose cron expression came due in the window since the previous tick.
func (s *Scheduler) Run(ctx context.Context) {
	s.logger.Infof("reconciliation scheduler started (tick %s)", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	last := s.now()
	for {
		select {
		case <-ctx.Done():
			s.logger.Infof("reconciliation scheduler stopped")
			return
		case <-ticker.C:
			now := s.now()
			s.tick(ctx, last, now)
			last = now
		}
	}
}

// tick fires every rule that came due in (last, now].
func (s *Scheduler) tick(ctx context.Context, last, now time.Time) {
	rules, err := s.listCronRules(ctx)
	if err != nil {
		s.logger.Errorf("scheduler: list rules: %s", err)
		return
	}
	for i := range rules {
		r := rules[i]
		due, err := dueInWindow(r.Schedule, last, now)
		if err != nil {
			// A bad cron spec shouldn't have passed rule-create validation, but
			// never let one poison the whole tick.
			s.logger.Errorf("scheduler: rule %s has invalid cron %q: %s", r.ID, cronExpr(r.Schedule), err)
			continue
		}
		if due {
			go s.fire(ctx, r)
		}
	}
}

// fire runs one rule's evaluation. Errors are logged, not propagated — a single
// rule's failure must not stop the scheduler (and EvaluateRule already raises an
// engine.error meta-alert on engine-side failures).
func (s *Scheduler) fire(ctx context.Context, r models.Rule) {
	if _, err := s.svc.EvaluateRule(ctx, r.ID, service.EvaluateRuleRequest{
		SafetyMargin: r.Schedule.SafetyMargin,
	}); err != nil {
		s.logger.Errorf("scheduler: evaluate rule %s: %s", r.ID, err)
	}
}

// listCronRules returns enabled rules with a cron schedule. One page, capped at
// maxRulesPerTick; logs a warning if the deployment has more (rather than
// silently dropping the overflow).
func (s *Scheduler) listCronRules(ctx context.Context) ([]models.Rule, error) {
	q := store.NewGetRulesQuery(store.NewPaginatedQueryOptions(store.RulesFilters{}).WithPageSize(maxRulesPerTick))
	cursor, err := s.svc.ListRules(ctx, q)
	if err != nil {
		return nil, err
	}
	if cursor.HasMore {
		s.logger.Errorf("scheduler: more than %d rules — only the first page is scheduled this tick", maxRulesPerTick)
	}
	out := make([]models.Rule, 0, len(cursor.Data))
	for i := range cursor.Data {
		r := cursor.Data[i]
		if r.Enabled && r.Schedule != nil && r.Schedule.Kind == models.ScheduleCron && r.Schedule.Expr != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

func cronExpr(sched *models.Schedule) string {
	if sched == nil {
		return ""
	}
	return sched.Expr
}

// dueInWindow reports whether a cron schedule's next firing after `last` falls
// at or before `now` — i.e. it came due in (last, now]. Timezone: the schedule's
// TZ when set, else UTC (deterministic; never the host's local zone).
func dueInWindow(sched *models.Schedule, last, now time.Time) (bool, error) {
	if sched == nil || sched.Kind != models.ScheduleCron || sched.Expr == "" {
		return false, nil
	}
	tz := sched.TZ
	if tz == "" {
		tz = "UTC"
	}
	spec := fmt.Sprintf("CRON_TZ=%s %s", tz, sched.Expr)
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return false, fmt.Errorf("parse cron %q (tz %s): %w", sched.Expr, tz, err)
	}
	next := schedule.Next(last)
	return !next.After(now), nil
}
