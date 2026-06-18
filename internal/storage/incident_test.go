package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// seedRuleAndEval inserts a minimum-viable rule + a single PASS evaluation
// row so incident tests can reference real foreign keys without setting up
// the full template machinery.
func seedRuleAndEval(t *testing.T, s *Storage) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	rule := &models.Rule{
		ID:           uuid.New(),
		Name:         "test",
		TemplateKind: models.TemplateLedgerInvariant,
		TemplateSpec: json.RawMessage(`{}`),
		CompiledCEL:  "true",
		Enabled:      true,
		Severity:     models.SeverityHigh,
	}
	require.NoError(t, s.CreateRule(ctx, rule))

	ev := &models.Evaluation{
		ID:           uuid.New(),
		RuleID:       rule.ID,
		StartedAt:    time.Now().UTC(),
		EndedAt:      time.Now().UTC(),
		PitPerSource: map[string]time.Time{},
		Result:       models.EvaluationPass,
	}
	require.NoError(t, s.CreateEvaluation(ctx, ev))
	return rule.ID, ev.ID
}

// TestOpenOrUpdateIncident_FirstFailureOpens regression test for the
// `lookup active incident: not found` bug: when no active incident exists for
// (rule_id, fingerprint), OpenOrUpdateIncident must INSERT a fresh row, not
// return an error. The bug was that the function checked for our ErrNotFound
// sentinel after the SELECT, but bun returns sql.ErrNoRows directly.
func TestOpenOrUpdateIncident_FirstFailureOpens(t *testing.T) {
	s := newStore(t)
	ruleID, evID := seedRuleAndEval(t, s)

	res, err := s.OpenOrUpdateIncident(context.Background(), OpenIncidentInput{
		RuleID:       ruleID,
		Fingerprint:  "asset:USD/2",
		Severity:     models.SeverityHigh,
		EvaluationID: evID,
		Evidence:     json.RawMessage(`{"drift":"50"}`),
		OccurredAt:   time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.Created, "first failing eval must create a new incident")
	require.NotNil(t, res.Incident)
	require.Equal(t, models.IncidentOpen, res.Incident.Status)
	require.Equal(t, "asset:USD/2", res.Incident.Fingerprint)
	require.Equal(t, 1, res.Incident.OccurrenceCount)
	require.Nil(t, res.Incident.ParentIncidentID, "no prior resolved incident, no parent link")
}

// TestOpenOrUpdateIncident_RepeatedFailureUpdates the partial unique index
// constraint forces consecutive failing evaluations for the same fingerprint
// to UPDATE the existing OPEN incident rather than open a duplicate.
func TestOpenOrUpdateIncident_RepeatedFailureUpdates(t *testing.T) {
	s := newStore(t)
	ruleID, evID := seedRuleAndEval(t, s)
	in := OpenIncidentInput{
		RuleID:       ruleID,
		Fingerprint:  "asset:USD/2",
		Severity:     models.SeverityHigh,
		EvaluationID: evID,
		Evidence:     json.RawMessage(`{}`),
		OccurredAt:   time.Now().UTC(),
	}
	first, err := s.OpenOrUpdateIncident(context.Background(), in)
	require.NoError(t, err)
	require.True(t, first.Created)

	second, err := s.OpenOrUpdateIncident(context.Background(), in)
	require.NoError(t, err)
	require.False(t, second.Created, "second failure must update, not create")
	require.Equal(t, first.Incident.ID, second.Incident.ID)
	require.Equal(t, 2, second.Incident.OccurrenceCount)
}

// TestOpenOrUpdateIncident_ReopenAfterResolve once an incident has resolved,
// a fresh failure for the same fingerprint opens a NEW row whose
// ParentIncidentID points back at the previous one. The re-open chain is the
// V1 way to make flapping visible without polluting MTTR.
func TestOpenOrUpdateIncident_ReopenAfterResolve(t *testing.T) {
	s := newStore(t)
	ruleID, evID := seedRuleAndEval(t, s)
	in := OpenIncidentInput{
		RuleID:       ruleID,
		Fingerprint:  "asset:USD/2",
		Severity:     models.SeverityHigh,
		EvaluationID: evID,
		Evidence:     json.RawMessage(`{}`),
		OccurredAt:   time.Now().UTC(),
	}
	first, err := s.OpenOrUpdateIncident(context.Background(), in)
	require.NoError(t, err)
	require.True(t, first.Created)

	resolved, err := s.AutoResolveIncident(context.Background(), ruleID, "asset:USD/2", evID, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.Equal(t, models.IncidentResolved, resolved.Status)

	in.OccurredAt = time.Now().UTC()
	reopen, err := s.OpenOrUpdateIncident(context.Background(), in)
	require.NoError(t, err)
	require.True(t, reopen.Created, "post-resolve, same fingerprint must open a new row")
	require.NotEqual(t, first.Incident.ID, reopen.Incident.ID)
	require.NotNil(t, reopen.Incident.ParentIncidentID)
	require.Equal(t, first.Incident.ID, *reopen.Incident.ParentIncidentID)
}
