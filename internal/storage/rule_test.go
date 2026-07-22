package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func makeRule(name string) *models.Rule {
	return &models.Rule{
		ID:             uuid.New(),
		Name:           name,
		TemplateKind:   models.TemplateLedgerInvariant,
		TemplateSpec:   json.RawMessage(`{}`),
		ExplanationCEL: "true",
		Enabled:        true,
		Severity:       models.SeverityHigh,
	}
}

func TestRule_CreateGet(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	rule := makeRule("test-rule")
	require.NoError(t, s.CreateRule(ctx, rule))
	require.False(t, rule.CreatedAt.IsZero())
	require.False(t, rule.UpdatedAt.IsZero())

	got, err := s.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	require.Equal(t, rule.ID, got.ID)
	require.Equal(t, "test-rule", got.Name)
	require.True(t, got.Enabled)
	require.Equal(t, rule.CreatedAt, got.CreatedAt)
	require.Equal(t, rule.UpdatedAt, got.UpdatedAt)
}

func TestRule_CreateGetPreservesExplicitZeroSafetyMargin(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	rule := makeRule("zero-safety-margin")
	rule.Schedule = &models.Schedule{
		Kind: models.ScheduleCron, Expr: "@daily",
		SafetyMargin: 0, SafetyMarginWasProvided: true,
	}

	require.NoError(t, s.CreateRule(ctx, rule))
	got, err := s.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Schedule)
	require.Zero(t, got.Schedule.SafetyMargin)
	require.True(t, got.Schedule.SafetyMarginWasProvided)
}

func TestRule_GetNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.GetRule(context.Background(), uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRuleRevisionFenceBlocksConcurrentPatch(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	rule := makeRule("manual-evaluation-fence")
	require.NoError(t, s.CreateRule(ctx, rule))

	validated := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- s.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
			if err := scoped.AssertRuleRevision(ctx, rule.ID, rule.Revision); err != nil {
				return err
			}
			close(validated)
			<-release
			return nil
		})
	}()
	<-validated

	name := "patched-during-evaluation"
	patchDone := make(chan error, 1)
	go func() { patchDone <- s.PatchRule(ctx, rule.ID, RulePatch{Name: &name}) }()
	select {
	case err := <-patchDone:
		t.Fatalf("rule patch completed before the evaluation transaction committed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-txDone)
	require.NoError(t, <-patchDone)
	require.ErrorIs(t, s.RunInTx(ctx, func(ctx context.Context, scoped *Storage) error {
		return scoped.AssertRuleRevision(ctx, rule.ID, rule.Revision)
	}), ErrObsoleteJob)
}

func TestRulePatchRejectsStaleExpectedRevision(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	rule := makeRule("optimistic-patch-fence")
	require.NoError(t, s.CreateRule(ctx, rule))

	originalRevision := rule.Revision
	firstName := "first-patch"
	require.NoError(t, s.PatchRule(ctx, rule.ID, RulePatch{Name: &firstName}))

	staleName := "stale-patch"
	err := s.PatchRule(ctx, rule.ID, RulePatch{
		Name:             &staleName,
		ExpectedRevision: &originalRevision,
	})
	require.ErrorIs(t, err, ErrRuleRevisionConflict)

	stored, err := s.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	require.Equal(t, firstName, stored.Name)
	require.Equal(t, originalRevision+1, stored.Revision)
}

func TestRule_DeleteCascadesAndNotFound(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// First delete on a never-existed id is ErrNotFound.
	require.ErrorIs(t, s.DeleteRule(ctx, uuid.New()), ErrNotFound)

	// Rule + its OWN evaluation + an alert + an alert_event whose
	// evaluation_id points at that same rule's evaluation. Deleting the rule
	// must cascade through all of them. The alert_event → evaluation FK is the
	// trap: the rule→evaluation cascade deletes the evaluation, so without
	// ON DELETE CASCADE on that FK the surviving event row blocks the delete
	// (regression: "no rule that ever fired can be deleted"). Using the rule's
	// own eval here is load-bearing — a foreign eval would never exercise it.
	ruleID, evID := seedRuleAndEval(t, s)

	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)
	require.NotEmpty(t, res.Alert.ID)

	require.NoError(t, s.DeleteRule(ctx, ruleID))

	// Rule + evaluation + alert + alert_event should all be gone.
	_, err = s.GetRule(ctx, ruleID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.GetEvaluation(ctx, evID)
	require.ErrorIs(t, err, ErrNotFound, "evaluation must be cascaded away with the rule")
	_, err = s.GetAlert(ctx, res.Alert.ID)
	require.ErrorIs(t, err, ErrNotFound)
	eventCount, err := s.db.NewSelect().Model((*models.AlertEvent)(nil)).
		Where("alert_id = ?", res.Alert.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, eventCount, "alert_event rows must be cascaded away with the rule")
}

func TestRule_PatchFields(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	rule := makeRule("orig")
	require.NoError(t, s.CreateRule(ctx, rule))

	newName := "updated"
	enabled := false
	newSev := models.SeverityCritical
	newLabels := map[string]string{"team": "treasury"}
	require.NoError(t, s.PatchRule(ctx, rule.ID, RulePatch{
		Name:     &newName,
		Enabled:  &enabled,
		Severity: &newSev,
		Labels:   &newLabels,
	}))

	got, err := s.GetRule(ctx, rule.ID)
	require.NoError(t, err)
	require.Equal(t, "updated", got.Name)
	require.False(t, got.Enabled)
	require.Equal(t, models.SeverityCritical, got.Severity)
	require.Equal(t, "treasury", got.Labels["team"])
}

// TestRule_PatchEmpty_NotFoundCheck the "empty patch" branch still verifies
// existence so a typo'd id doesn't silently return 200 OK.
func TestRule_PatchEmpty_NotFoundCheck(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	require.ErrorIs(t, s.PatchRule(ctx, uuid.New(), RulePatch{}), ErrNotFound)

	rule := makeRule("present")
	require.NoError(t, s.CreateRule(ctx, rule))
	require.NoError(t, s.PatchRule(ctx, rule.ID, RulePatch{}), "empty patch on existing rule is a no-op")
}

// TestRule_PatchNonexistent — non-empty patch against a missing id reaches the
// UPDATE branch and ErrNotFounds via RowsAffected = 0.
func TestRule_PatchNonexistent(t *testing.T) {
	s := newStore(t)
	name := "x"
	err := s.PatchRule(context.Background(), uuid.New(), RulePatch{Name: &name})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRule_List_Filters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	enabled := makeRule("alpha")
	enabled.Severity = models.SeverityHigh
	require.NoError(t, s.CreateRule(ctx, enabled))

	disabled := makeRule("beta")
	disabled.Enabled = false
	require.NoError(t, s.CreateRule(ctx, disabled))

	// Unfiltered list returns both
	q := NewGetRulesQuery(NewPaginatedQueryOptions(RulesFilters{}).WithPageSize(15))
	cursor, err := s.ListRules(ctx, q)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cursor.Data), 2)

	// Filter by enabled = true
	filtered := NewGetRulesQuery(
		NewPaginatedQueryOptions(RulesFilters{}).
			WithQueryBuilder(query.Match("enabled", true)).
			WithPageSize(15),
	)
	cursor, err = s.ListRules(ctx, filtered)
	require.NoError(t, err)
	for _, r := range cursor.Data {
		require.True(t, r.Enabled, "filter must exclude disabled rules")
	}

	// Filter by name
	byName := NewGetRulesQuery(
		NewPaginatedQueryOptions(RulesFilters{}).
			WithQueryBuilder(query.Match("name", "alpha")).
			WithPageSize(15),
	)
	cursor, err = s.ListRules(ctx, byName)
	require.NoError(t, err)
	require.Len(t, cursor.Data, 1)
	require.Equal(t, "alpha", cursor.Data[0].Name)
}

// TestRule_List_RejectsUnknownFilterKey — query builder errors must reach the
// caller as ErrInvalidQuery so the API layer surfaces a 400.
func TestRule_List_RejectsUnknownFilterKey(t *testing.T) {
	s := newStore(t)
	q := NewGetRulesQuery(
		NewPaginatedQueryOptions(RulesFilters{}).
			WithQueryBuilder(query.Match("definitely_not_a_column", true)).
			WithPageSize(15),
	)
	_, err := s.ListRules(context.Background(), q)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidQuery), "expected ErrInvalidQuery, got %v", err)
}

// TestRule_List_RejectsNonMatchOnEnumKey — keys that only accept $match must
// reject other operators, e.g. $gt on `enabled` should fail.
func TestRule_List_RejectsNonMatchOnEnumKey(t *testing.T) {
	s := newStore(t)
	q := NewGetRulesQuery(
		NewPaginatedQueryOptions(RulesFilters{}).
			WithQueryBuilder(query.Gt("enabled", true)).
			WithPageSize(15),
	)
	_, err := s.ListRules(context.Background(), q)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidQuery))
}
