package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
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

// TestRunInTx_RollsBackOnError confirms the new transactional helper actually
// rolls back when its callback errors — guarantees that the evaluation +
// incident orchestration is atomic.
func TestRunInTx_RollsBackOnError(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	wantErr := errors.New("callback failed")
	err := s.RunInTx(ctx, func(ctx context.Context, txStore *Storage) error {
		// Open an incident inside the tx — would normally succeed.
		_, _ = txStore.OpenOrUpdateIncident(ctx, OpenIncidentInput{
			RuleID:       ruleID,
			Fingerprint:  "asset:USD/2",
			Severity:     models.SeverityHigh,
			EvaluationID: evID,
			Evidence:     json.RawMessage(`{}`),
			OccurredAt:   time.Now().UTC(),
		})
		// ... then fail the callback. The tx must roll back the insert.
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)

	// Outside the tx, nothing should be visible.
	got, err := s.ListActiveIncidentFingerprints(ctx, ruleID)
	require.NoError(t, err)
	require.Empty(t, got, "rollback must discard the insert")
}

// TestAckIncident_IdempotentPreservesMetadata a second ACK on an already-ACK'd
// incident must not overwrite the original ack-by / ack-at metadata. That
// metadata is the audit trail of who first acknowledged the incident.
func TestAckIncident_IdempotentPreservesMetadata(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	res, err := s.OpenOrUpdateIncident(ctx, OpenIncidentInput{
		RuleID:       ruleID,
		Fingerprint:  "asset:USD/2",
		Severity:     models.SeverityHigh,
		EvaluationID: evID,
		Evidence:     json.RawMessage(`{}`),
		OccurredAt:   time.Now().UTC(),
	})
	require.NoError(t, err)
	id := res.Incident.ID

	firstAt := time.Now().UTC().Add(-time.Hour)
	firstAck := &models.Ack{By: "alice@formance.com", At: firstAt, Note: "looking into it"}
	first, err := s.AckIncident(ctx, id, firstAck)
	require.NoError(t, err)
	require.Equal(t, models.IncidentAcknowledged, first.Status)
	require.NotNil(t, first.Ack)
	require.Equal(t, "alice@formance.com", first.Ack.By)

	secondAck := &models.Ack{By: "bob@formance.com", At: time.Now().UTC(), Note: "second acknowledger"}
	second, err := s.AckIncident(ctx, id, secondAck)
	require.NoError(t, err, "re-ACK must be idempotent, not error")
	require.Equal(t, models.IncidentAcknowledged, second.Status)
	require.NotNil(t, second.Ack)
	require.Equal(t, "alice@formance.com", second.Ack.By, "original ack-by must be preserved")
	require.WithinDuration(t, firstAt, second.Ack.At, time.Second, "original ack-at must be preserved")
	require.Equal(t, "looking into it", second.Ack.Note)
}

// TestIncidentParentSameRule_TriggerRejects the schema trigger that prevents
// parent_incident_id from pointing at an incident belonging to a different
// rule. The lifecycle only makes sense within a single rule's re-open chain.
func TestIncidentParentSameRule_TriggerRejects(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Rule A with one incident → make it RESOLVED so it can be a parent.
	ruleA, evA := seedRuleAndEval(t, s)
	parent, err := s.OpenOrUpdateIncident(ctx, OpenIncidentInput{
		RuleID:       ruleA,
		Fingerprint:  "asset:USD/2",
		Severity:     models.SeverityHigh,
		EvaluationID: evA,
		Evidence:     json.RawMessage(`{}`),
		OccurredAt:   time.Now().UTC(),
	})
	require.NoError(t, err)
	_, err = s.AutoResolveIncident(ctx, ruleA, "asset:USD/2", evA, time.Now().UTC())
	require.NoError(t, err)

	// Rule B + eval B exist separately.
	ruleB, _ := seedRuleAndEval(t, s)

	// Manually craft a rule-B incident whose parent points at rule-A's
	// resolved incident. The trigger must reject this.
	bad := &models.Incident{
		ID:                uuid.New(),
		RuleID:            ruleB,
		Fingerprint:       "asset:USD/2",
		Status:            models.IncidentOpen,
		Severity:          models.SeverityHigh,
		OpenedAt:          time.Now().UTC(),
		LastSeenAt:        time.Now().UTC(),
		OccurrenceCount:   1,
		FirstEvaluationID: evA,
		LastEvaluationID:  evA,
		ParentIncidentID:  &parent.Incident.ID,
	}
	_, err = s.db.NewInsert().Model(bad).Exec(ctx)
	require.Error(t, err, "trigger must reject cross-rule parent_incident_id")
	require.Contains(t, err.Error(), "belongs to rule")
}

// TestOpenOrUpdateIncident_ConcurrentFirstOpen exercises the
// unique-violation retry path: two goroutines call OpenOrUpdateIncident for
// the same (rule_id, fingerprint) at the same time. Without the retry, one
// would error out on the partial unique index; with the retry, both succeed
// and the second one observes occurrence_count == 2.
func TestOpenOrUpdateIncident_ConcurrentFirstOpen(t *testing.T) {
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

	const writers = 6
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = make([]*OpenIncidentResult, 0, writers)
		errs    = make([]error, 0, writers)
		start   = make(chan struct{})
	)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := s.OpenOrUpdateIncident(context.Background(), in)
			mu.Lock()
			results = append(results, res)
			errs = append(errs, err)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	created := 0
	for i, err := range errs {
		require.NoError(t, err, "writer %d", i)
		require.NotNil(t, results[i])
		if results[i].Created {
			created++
		}
	}
	require.Equal(t, 1, created, "exactly one writer must INSERT, the rest must UPDATE")

	final, err := s.GetIncident(context.Background(), results[0].Incident.ID)
	require.NoError(t, err)
	require.Equal(t, writers, final.OccurrenceCount, "every writer must be reflected in occurrence_count")
}
