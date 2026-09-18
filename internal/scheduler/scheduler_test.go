package scheduler

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
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
	queries   []store.GetRulesQuery
	// pageSize, when > 0, is the page the fake serves regardless of what the
	// caller asked for — a store may always return fewer rows than requested.
	pageSize uint64
	// alwaysMore models a store that never stops claiming another page.
	alwaysMore bool
}

func (f *fakeRuleSvc) ListRules(_ context.Context, q store.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error) {
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()

	if f.alwaysMore {
		return &bunpaginate.Cursor[models.Rule]{Data: f.rules, HasMore: true}, nil
	}

	size := f.pageSize
	if size == 0 {
		size = q.PageSize
	}

	total := uint64(len(f.rules))
	if q.Offset >= total {
		return &bunpaginate.Cursor[models.Rule]{}, nil
	}

	end := total
	hasMore := false
	if size > 0 && q.Offset+size < total {
		end = q.Offset + size
		hasMore = true
	}

	return &bunpaginate.Cursor[models.Rule]{Data: f.rules[q.Offset:end], HasMore: hasMore}, nil
}

// filters returns the (operator, key, value) triples of the nth call's query
// builder, so a test can assert what was pushed down to the store.
func (f *fakeRuleSvc) filters(n int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string
	qb := f.queries[n].Options.QueryBuilder
	if qb == nil {
		return nil
	}
	_ = qb.Walk(func(operator, key string, value any) error {
		out = append(out, fmt.Sprintf("%s %s=%v", operator, key, value))
		return nil
	})

	return out
}

func (f *fakeRuleSvc) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.queries)
}
func (f *fakeRuleSvc) EvaluateRule(_ context.Context, id uuid.UUID, _ service.EvaluateRuleRequest) (*models.Evaluation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evaluated = append(f.evaluated, id)
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

// The enabled flag is pushed down to the store rather than filtered in Go: it is
// an indexed metadata field, and a client-side filter spends the page budget on
// rules that can never fire (EN-2239).
func TestListCronRules_FiltersEnabledServerSide(t *testing.T) {
	t.Parallel()

	svc := &fakeRuleSvc{rules: []models.Rule{{ID: uuid.New(), Enabled: true, Schedule: cronSched("* * * * *")}}}
	s := New(svc, time.Minute, testLogger())

	_, err := s.listCronRules(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, svc.calls())
	require.Equal(t, []string{"$match enabled=true"}, svc.filters(0))
}

// Every page is drained: an enabled cron rule that sorts past the first page must
// still fire. Before EN-2239 the scheduler read one page and logged.
func TestListCronRules_DrainsEveryPage(t *testing.T) {
	t.Parallel()

	first := models.Rule{ID: uuid.New(), Enabled: true, Schedule: cronSched("* * * * *")}
	second := models.Rule{ID: uuid.New(), Enabled: true, Schedule: cronSched("* * * * *")}
	last := models.Rule{ID: uuid.New(), Enabled: true, Schedule: cronSched("* * * * *")}

	svc := &fakeRuleSvc{rules: []models.Rule{first, second, last}, pageSize: 2}
	s := New(svc, time.Minute, testLogger())

	rules, err := s.listCronRules(context.Background())

	require.NoError(t, err)
	require.Len(t, rules, 3, "a rule on the second page must not be dropped")
	require.Equal(t, last.ID, rules[2].ID)
	require.Equal(t, 2, svc.calls())
}

// A store that never stops offering another page fails the tick rather than
// firing whatever subset happened to be read — silently scheduling part of the
// rule set is the failure mode this product exists to catch.
func TestListCronRules_RefusesAPartialSet(t *testing.T) {
	t.Parallel()

	svc := &fakeRuleSvc{
		rules:      []models.Rule{{ID: uuid.New(), Enabled: true, Schedule: cronSched("* * * * *")}},
		alwaysMore: true,
	}
	s := New(svc, time.Minute, testLogger())

	rules, err := s.listCronRules(context.Background())

	require.Error(t, err)
	require.Nil(t, rules)
	require.Equal(t, maxRulePages, svc.calls())
}
