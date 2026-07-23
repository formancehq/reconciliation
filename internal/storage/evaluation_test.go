package storage

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestEvaluation_GetNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.GetEvaluation(context.Background(), uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

// TestEvaluation_List_FilterByRuleAndResult — the two filters the API exposes
// today (ruleID for "history of this rule" and result for "show me FAILs").
func TestEvaluation_List_FilterByRuleAndResult(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleA, _ := seedRuleAndEval(t, s) // seeds 1 PASS evaluation against ruleA
	ruleB, _ := seedRuleAndEval(t, s) // seeds 1 PASS evaluation against ruleB

	// Add a FAIL eval against ruleA so we can filter by result too.
	fail := &models.Evaluation{
		ID:           uuid.New(),
		RuleID:       ruleA,
		StartedAt:    time.Now().UTC(),
		EndedAt:      time.Now().UTC(),
		PitPerSource: map[string]time.Time{},
		Result:       models.EvaluationFail,
	}
	require.NoError(t, s.CreateEvaluation(ctx, fail))
	require.False(t, fail.CreatedAt.IsZero())

	// Filter by ruleID returns both ruleA evals (PASS + FAIL), not ruleB's.
	byRule := NewGetEvaluationsQuery(
		NewPaginatedQueryOptions(EvaluationsFilters{}).
			WithQueryBuilder(query.Match("ruleID", ruleA)).
			WithPageSize(15),
	)
	cursor, err := s.ListEvaluations(ctx, byRule)
	require.NoError(t, err)
	require.Len(t, cursor.Data, 2)
	for _, ev := range cursor.Data {
		require.Equal(t, ruleA, ev.RuleID)
	}
	_ = ruleB // referenced for clarity; ruleB shouldn't appear above

	// Filter by result=FAIL across all rules — just the one we added.
	byResult := NewGetEvaluationsQuery(
		NewPaginatedQueryOptions(EvaluationsFilters{}).
			WithQueryBuilder(query.Match("result", "FAIL")).
			WithPageSize(15),
	)
	cursor, err = s.ListEvaluations(ctx, byResult)
	require.NoError(t, err)
	require.Len(t, cursor.Data, 1)
	require.Equal(t, fail.ID, cursor.Data[0].ID)
}

// TestEvaluation_List_RejectsUnknownKey — defends the query builder.
func TestEvaluation_List_RejectsUnknownKey(t *testing.T) {
	s := newStore(t)
	q := NewGetEvaluationsQuery(
		NewPaginatedQueryOptions(EvaluationsFilters{}).
			WithQueryBuilder(query.Match("not_a_column", true)).
			WithPageSize(15),
	)
	_, err := s.ListEvaluations(context.Background(), q)
	require.ErrorIs(t, err, ErrInvalidQuery)
}

// TestEvaluation_List_TimeRange — startedAt with $gt / $lt operates as a range
// filter; the time-comparison branch of evaluationQueryContext.
func TestEvaluation_List_TimeRange(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, _ := seedRuleAndEval(t, s)

	now := time.Now().UTC()
	old := &models.Evaluation{
		ID:           uuid.New(),
		RuleID:       ruleID,
		StartedAt:    now.Add(-2 * time.Hour),
		EndedAt:      now.Add(-2*time.Hour + time.Second),
		PitPerSource: map[string]time.Time{},
		Result:       models.EvaluationPass,
	}
	require.NoError(t, s.CreateEvaluation(ctx, old))

	// Filter startedAt > now-1h → drops both seeded one and old one is excluded;
	// seedRuleAndEval's eval is "now"-ish, so it stays.
	cutoff := now.Add(-time.Hour)
	cursor, err := s.ListEvaluations(ctx, NewGetEvaluationsQuery(
		NewPaginatedQueryOptions(EvaluationsFilters{}).
			WithQueryBuilder(query.Gt("startedAt", cutoff)).
			WithPageSize(15),
	))
	require.NoError(t, err)
	for _, ev := range cursor.Data {
		require.True(t, ev.StartedAt.After(cutoff), "filter must exclude older evaluations")
	}
}
