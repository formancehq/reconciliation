package scheduler

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func cronSched(expr string) *models.Schedule {
	return &models.Schedule{Kind: models.ScheduleCron, Expr: expr}
}

func testLogger() v5log.Logger { return v5log.NewDefaultLogger(io.Discard, false, false, false) }

func TestDueInWindow(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 3, 15, 8, 59, 0, 0, time.UTC)
	now := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		sched *models.Schedule
		due   bool
		isErr bool
	}{
		{"every minute → due", cronSched("* * * * *"), true, false},
		{"daily 09:00 UTC → due at the 09:00 window", cronSched("0 9 * * *"), true, false},
		{"daily 10:00 → not due at 09:00", cronSched("0 10 * * *"), false, false},
		{"nil schedule → not due", nil, false, false},
		{"on_demand → not due", &models.Schedule{Kind: models.ScheduleOnDemand, Expr: "* * * * *"}, false, false},
		{"empty expr → not due", &models.Schedule{Kind: models.ScheduleCron}, false, false},
		{"invalid expr → error", cronSched("not a cron"), false, true},
		{"with timezone parses", &models.Schedule{Kind: models.ScheduleCron, Expr: "0 9 * * *", TZ: "America/New_York"}, false, false}, // 9am NY ≠ 09:00 UTC
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			due, err := dueInWindow(tc.sched, last, now)
			if tc.isErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.due, due)
		})
	}
}

type fakeRuleSvc struct {
	rules     []models.Rule
	mu        sync.Mutex
	evaluated []uuid.UUID
	hasMore   bool
	listErr   error // injected: ListRules fails
	evalErr   error // injected: EvaluateRule fails (id is still recorded)
}

func (f *fakeRuleSvc) ListRules(_ context.Context, q storage.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	// Faithfully apply the same filters the real storage query does, so the
	// scheduler's reliance on SQL-side filtering is exercised here too.
	filters := q.Options.Options
	out := make([]models.Rule, 0, len(f.rules))
	for _, r := range f.rules {
		if filters.EnabledOnly && !r.Enabled {
			continue
		}
		if filters.ScheduleKind != "" && (r.Schedule == nil || r.Schedule.Kind != filters.ScheduleKind) {
			continue
		}
		out = append(out, r)
	}
	return &bunpaginate.Cursor[models.Rule]{Data: out, HasMore: f.hasMore}, nil
}
func (f *fakeRuleSvc) EvaluateRule(_ context.Context, id uuid.UUID, _ service.EvaluateRuleRequest) (*models.Evaluation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evaluated = append(f.evaluated, id)
	if f.evalErr != nil {
		return nil, f.evalErr
	}
	return &models.Evaluation{ID: uuid.New(), RuleID: id}, nil
}
func (f *fakeRuleSvc) fired(id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.evaluated {
		if e == id {
			return true
		}
	}
	return false
}
func (f *fakeRuleSvc) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.evaluated)
}

// tick fires only enabled, cron-scheduled, currently-due rules.
func TestTick_FiresOnlyDueCronRules(t *testing.T) {
	due := models.Rule{ID: uuid.New(), Enabled: true, Schedule: cronSched("* * * * *")}     // due
	notDue := models.Rule{ID: uuid.New(), Enabled: true, Schedule: cronSched("0 10 * * *")} // not due at 09:00
	onDemand := models.Rule{ID: uuid.New(), Enabled: true, Schedule: &models.Schedule{Kind: models.ScheduleOnDemand}}
	disabled := models.Rule{ID: uuid.New(), Enabled: false, Schedule: cronSched("* * * * *")} // disabled

	svc := &fakeRuleSvc{rules: []models.Rule{due, notDue, onDemand, disabled}}
	s := New(svc, time.Minute, testLogger())

	last := time.Date(2026, 3, 15, 8, 59, 0, 0, time.UTC)
	now := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	s.tick(context.Background(), last, now)

	require.Eventually(t, func() bool { return svc.fired(due.ID) }, time.Second, 5*time.Millisecond,
		"the due cron rule should be evaluated")
	// Give any stray goroutines a moment, then assert nothing else fired.
	time.Sleep(50 * time.Millisecond)
	require.False(t, svc.fired(notDue.ID), "rule not due this window")
	require.False(t, svc.fired(onDemand.ID), "on-demand rule is never scheduled")
	require.False(t, svc.fired(disabled.ID), "disabled rule is never scheduled")
	require.Equal(t, 1, svc.count())
}
