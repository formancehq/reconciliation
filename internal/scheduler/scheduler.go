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
	"github.com/formancehq/go-libs/query"
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

// rulesPageSize is the batch size of the per-tick rule scan. Every page is
// drained, so this is a batching knob — not a ceiling on how many rules a
// deployment can schedule.
const rulesPageSize = 1000

// maxRulePages bounds the drain loop so a store that never stops reporting
// another page cannot spin a tick forever. At rulesPageSize per page it allows
// 100k enabled rules; past that the tick fails loudly rather than scheduling an
// arbitrary subset.
const maxRulePages = 100

// filterKeyEnabled is the rule-list filter key for the enabled flag, as the
// store's rule leaf mapper spells it (internal/ledgerstore/filter.go).
const filterKeyEnabled = "enabled"

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
	// PIT defaults to now inside EvaluateRule; ledger reads are live (ADR-003), so
	// a scheduled fire carries no extra read knobs. Trigger marks the capture record.
	if _, err := s.svc.EvaluateRule(ctx, r.ID, service.EvaluateRuleRequest{Trigger: service.TriggerScheduled}); err != nil {
		s.logger.Errorf("scheduler: evaluate rule %s: %s", r.ID, err)
	}
}

// listCronRules returns the enabled rules that carry a cron schedule.
//
// `enabled` is filtered **server-side**: it is a declared, indexed metadata field
// (ledgerschema.MetadataIndexes) and the rule filter translator maps the key, so
// the ledger returns candidates rather than every rule in the deployment. Cron-ness
// stays a client-side check — `schedule` is opaque JSON with no indexed
// discriminator — and the Enabled re-check is kept as a cheap guard so correctness
// does not rest on the filter alone.
//
// Every page is drained. A scheduler that quietly enumerates a subset is a control
// that stops running without saying so, which is the failure mode this product
// exists to catch, so an implausibly large rule set fails the tick instead of
// firing an arbitrary slice of it.
//
// Paging is not free: the ledger store fetches the whole matching set and then
// offset-slices it, so each extra page re-reads everything. Below rulesPageSize
// enabled rules — every deployment we expect — this is exactly one call, and past
// it correctness is worth more than the second read.
func (s *Scheduler) listCronRules(ctx context.Context) ([]models.Rule, error) {
	opts := store.NewPaginatedQueryOptions(store.RulesFilters{}).
		WithQueryBuilder(query.Match(filterKeyEnabled, true)).
		WithPageSize(rulesPageSize)

	var (
		out    []models.Rule
		offset uint64
	)

	for page := 0; page < maxRulePages; page++ {
		q := store.NewGetRulesQuery(opts)
		q.Offset = offset

		cursor, err := s.svc.ListRules(ctx, q)
		if err != nil {
			return nil, err
		}

		for i := range cursor.Data {
			r := cursor.Data[i]
			if r.Enabled && r.Schedule != nil && r.Schedule.Kind == models.ScheduleCron && r.Schedule.Expr != "" {
				out = append(out, r)
			}
		}

		if !cursor.HasMore || len(cursor.Data) == 0 {
			return out, nil
		}

		offset += uint64(len(cursor.Data))
	}

	return nil, fmt.Errorf("rule scan exceeded %d pages of %d: refusing to schedule a partial set", maxRulePages, rulesPageSize)
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
