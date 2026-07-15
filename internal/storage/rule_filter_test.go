package storage

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ListRules with the scheduler's filter (enabled + cron) must return only rules
// the scheduler can fire — pushed into SQL, not filtered in memory after paging.
func TestListRules_FilterEnabledCron(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	mk := func(name string, enabled bool, sched *models.Schedule) {
		require.NoError(t, s.CreateRule(ctx, &models.Rule{
			ID:           uuid.New(),
			Name:         name,
			TemplateKind: models.TemplateLedgerInvariant,
			TemplateSpec: json.RawMessage(`{}`),
			CompiledCEL:  "true",
			Enabled:      enabled,
			Severity:     models.SeverityHigh,
			Schedule:     sched,
		}))
	}
	cron := func() *models.Schedule { return &models.Schedule{Kind: models.ScheduleCron, Expr: "* * * * *"} }
	mk("enabled-cron", true, cron())
	mk("disabled-cron", false, cron())
	mk("enabled-ondemand", true, &models.Schedule{Kind: models.ScheduleOnDemand})
	mk("enabled-nosched", true, nil)

	q := NewGetRulesQuery(NewPaginatedQueryOptions(RulesFilters{
		EnabledOnly:  true,
		ScheduleKind: models.ScheduleCron,
	}).WithPageSize(100))

	cur, err := s.ListRules(ctx, q)
	require.NoError(t, err)
	require.Len(t, cur.Data, 1, "only the enabled cron rule should match")
	require.Equal(t, "enabled-cron", cur.Data[0].Name)

	// No filter → all four rules come back (API default is unfiltered).
	all, err := s.ListRules(ctx, NewGetRulesQuery(NewPaginatedQueryOptions(RulesFilters{}).WithPageSize(100)))
	require.NoError(t, err)
	require.Len(t, all.Data, 4)
}
