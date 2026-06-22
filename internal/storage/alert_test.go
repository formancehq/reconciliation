package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// seedRuleAndEval inserts a minimum-viable rule + a single evaluation row so
// alert tests can reference real foreign keys without setting up the full
// template machinery.
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

func defaultOpenInput(t *testing.T, ruleID, evID uuid.UUID) OpenAlertInput {
	t.Helper()
	return OpenAlertInput{
		RuleID:       ruleID,
		Fingerprint:  "asset:USD/2",
		Severity:     models.SeverityHigh,
		EvaluationID: evID,
		Evidence:     json.RawMessage(`{"drift":"50"}`),
		OccurredAt:   time.Now().UTC(),
	}
}

// TestOpenOrUpdateAlert_FirstFailureOpens — the first failing eval for a
// fingerprint INSERTs a fresh alert AND writes one inaugural alert_event with
// prev_status=NULL.
func TestOpenOrUpdateAlert_FirstFailureOpens(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)
	require.True(t, res.Created)
	require.False(t, res.Reopened)
	require.Equal(t, models.AlertOpen, res.Alert.Status)
	require.Equal(t, int64(1), res.Alert.OccurrenceCount)

	events, err := s.ListAlertEvents(ctx, res.Alert.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, models.AlertEventFail, events[0].Type)
	require.Nil(t, events[0].PrevStatus, "inaugural event has no prev_status")
	require.Equal(t, models.AlertOpen, events[0].NewStatus)
}

// TestOpenOrUpdateAlert_RepeatedFailureUpdates — same fingerprint, alert
// stays at the same ID, occurrence_count++, fresh fail event appended each
// time.
func TestOpenOrUpdateAlert_RepeatedFailureUpdates(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	in := defaultOpenInput(t, ruleID, evID)

	first, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)

	second, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	require.False(t, second.Created, "second fail must update, not insert")
	require.Equal(t, first.Alert.ID, second.Alert.ID)
	require.Equal(t, int64(2), second.Alert.OccurrenceCount)

	events, err := s.ListAlertEvents(ctx, first.Alert.ID)
	require.NoError(t, err)
	require.Len(t, events, 2, "every fail evaluation appends one event")
	open := models.AlertOpen
	for _, e := range events {
		require.Equal(t, models.AlertEventFail, e.Type)
		if e.ID == first.Event.ID {
			require.Nil(t, e.PrevStatus)
		} else {
			require.NotNil(t, e.PrevStatus)
			require.Equal(t, open, *e.PrevStatus)
		}
	}
}

// TestOpenOrUpdateAlert_ReopenInPlace — the headline behavioural change:
// after a RESOLVED alert sees another FAIL, the same row reopens (no new ID,
// no chain) and a fail event with prev_status=RESOLVED lands in the log.
func TestOpenOrUpdateAlert_ReopenInPlace(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	in := defaultOpenInput(t, ruleID, evID)

	first, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	originalID := first.Alert.ID

	resolved, err := s.AutoResolveAlert(ctx, ruleID, "asset:USD/2", evID, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.Equal(t, models.AlertResolved, resolved.Status)
	require.Equal(t, originalID, resolved.ID, "auto-resolve keeps the same alert id")

	in.OccurredAt = time.Now().UTC()
	reopen, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	require.False(t, reopen.Created, "reopen must NOT create a new alert")
	require.True(t, reopen.Reopened)
	require.Equal(t, originalID, reopen.Alert.ID)
	require.Equal(t, models.AlertOpen, reopen.Alert.Status)
	require.Equal(t, int64(2), reopen.Alert.OccurrenceCount, "lifetime count carries across the cycle")
	require.Nil(t, reopen.Alert.Resolution, "reopen clears the prior resolution")

	// Event log captures: initial fail (open), pass (auto-resolve), fail (reopen).
	events, err := s.ListAlertEvents(ctx, originalID)
	require.NoError(t, err)
	require.Len(t, events, 3)

	// Events are returned most-recent-first.
	require.Equal(t, models.AlertEventFail, events[0].Type)
	require.True(t, events[0].IsReopen(), "newest event must be the reopen")
	require.Equal(t, models.AlertEventPass, events[1].Type)
	require.Equal(t, models.AlertEventFail, events[2].Type)
	require.Nil(t, events[2].PrevStatus, "oldest event is the inaugural open")
}

// TestOpenOrUpdateAlert_ConcurrentFirstOpen exercises the unique-violation
// retry path: many goroutines call OpenOrUpdateAlert for the same
// (rule, fingerprint) simultaneously. Without the retry, one would error out
// on the UNIQUE constraint; with the retry, exactly one INSERTs and the rest
// UPDATE the same row.
func TestOpenOrUpdateAlert_ConcurrentFirstOpen(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	in := defaultOpenInput(t, ruleID, evID)

	const writers = 6
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = make([]*OpenAlertResult, 0, writers)
		errs    = make([]error, 0, writers)
		start   = make(chan struct{})
	)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := s.OpenOrUpdateAlert(ctx, in)
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

	final, err := s.GetAlert(ctx, results[0].Alert.ID)
	require.NoError(t, err)
	require.Equal(t, int64(writers), final.OccurrenceCount)

	events, err := s.ListAlertEvents(ctx, final.ID)
	require.NoError(t, err)
	require.Len(t, events, writers, "every concurrent writer appends one fail event")
}

// TestAckAlert_IdempotentPreservesMetadata — a second ACK on an already-ACK'd
// alert must NOT overwrite the original ack metadata AND must NOT append a
// second ack event. That metadata is the audit trail of who first
// acknowledged the alert.
func TestAckAlert_IdempotentPreservesMetadata(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)
	id := res.Alert.ID

	firstAt := time.Now().UTC().Add(-time.Hour)
	firstAck := &models.Ack{By: "alice@formance.com", At: firstAt, Note: "looking into it"}
	_, err = s.AckAlert(ctx, id, firstAck)
	require.NoError(t, err)

	secondAck := &models.Ack{By: "bob@formance.com", At: time.Now().UTC(), Note: "second acknowledger"}
	second, err := s.AckAlert(ctx, id, secondAck)
	require.NoError(t, err, "re-ACK must be idempotent")
	require.Equal(t, models.AlertAcknowledged, second.Status)
	require.NotNil(t, second.Ack)
	require.Equal(t, "alice@formance.com", second.Ack.By)
	require.WithinDuration(t, firstAt, second.Ack.At, time.Second)
	require.Equal(t, "looking into it", second.Ack.Note)

	events, err := s.ListAlertEvents(ctx, id)
	require.NoError(t, err)
	ackEvents := 0
	for _, e := range events {
		if e.Type == models.AlertEventAck {
			ackEvents++
		}
	}
	require.Equal(t, 1, ackEvents, "idempotent re-ACK must not append a second ack event")
}

// TestResolveAlertManual_FixedByBooking — manual close path with kind=fixed,
// records a `resolve` event, and rejects on already-resolved.
func TestResolveAlertManual_FixedByBooking(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)

	resolution := &models.Resolution{
		Kind:            models.ResolutionFixedByBooking,
		By:              "ops@formance.com",
		At:              time.Now().UTC(),
		Note:            "posted correction tx_abc",
		TransactionRefs: []string{"tx_abc"},
	}
	closed, err := s.ResolveAlertManual(ctx, res.Alert.ID, resolution)
	require.NoError(t, err)
	require.Equal(t, models.AlertResolved, closed.Status)
	require.NotNil(t, closed.Resolution)
	require.Equal(t, models.ResolutionFixedByBooking, closed.Resolution.Kind)
	require.Equal(t, []string{"tx_abc"}, closed.Resolution.TransactionRefs)

	events, err := s.ListAlertEvents(ctx, res.Alert.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 2) // initial fail + resolve
	require.Equal(t, models.AlertEventResolve, events[0].Type, "resolve event must be the latest")

	// Second manual resolve on the already-resolved row must error (no row matches the WHERE).
	_, err = s.ResolveAlertManual(ctx, res.Alert.ID, resolution)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestResolveAlertManual_RejectsWrongKind — only fixed_by_booking or auto.
// Belt-and-braces; the API layer also gates this.
func TestResolveAlertManual_RejectsWrongKind(t *testing.T) {
	s := newStore(t)
	_, err := s.ResolveAlertManual(context.Background(), uuid.New(), &models.Resolution{
		Kind: models.ResolutionAcceptedByBusiness,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported resolution kind")
}

// TestAcceptAlert_Path — accepted_by_business writes the right event + the
// resolution carries note + evidence snapshot.
func TestAcceptAlert_Path(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	res, err := s.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
	require.NoError(t, err)

	resolution := &models.Resolution{
		Kind:             models.ResolutionAcceptedByBusiness,
		By:               "treasurer@formance.com",
		At:               time.Now().UTC(),
		Note:             "settlement lag confirmed",
		EvidenceSnapshot: json.RawMessage(`{"asset":"USD/2","drift":"50"}`),
	}
	closed, err := s.AcceptAlert(ctx, res.Alert.ID, resolution)
	require.NoError(t, err)
	require.Equal(t, models.AlertResolved, closed.Status)
	require.Equal(t, models.ResolutionAcceptedByBusiness, closed.Resolution.Kind)
	require.Equal(t, "settlement lag confirmed", closed.Resolution.Note)

	events, err := s.ListAlertEvents(ctx, res.Alert.ID)
	require.NoError(t, err)
	require.Equal(t, models.AlertEventAccept, events[0].Type)
}

// TestAcceptAlert_RequiresNote — note is mandatory per V1 spec §5.4.
func TestAcceptAlert_RequiresNote(t *testing.T) {
	s := newStore(t)
	_, err := s.AcceptAlert(context.Background(), uuid.New(), &models.Resolution{
		Kind: models.ResolutionAcceptedByBusiness,
		By:   "treasurer",
		At:   time.Now().UTC(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "note is required")
}

// TestAcceptAlert_RejectsWrongKind — defensive type-check at the storage edge.
func TestAcceptAlert_RejectsWrongKind(t *testing.T) {
	s := newStore(t)
	_, err := s.AcceptAlert(context.Background(), uuid.New(), &models.Resolution{
		Kind: models.ResolutionFixedByBooking,
		Note: "x",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected kind")
}

// TestListAlerts_Filters exercises the cursor list + filter pipeline.
func TestListAlerts_Filters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	openIn := defaultOpenInput(t, ruleID, evID)
	openIn.Fingerprint = "asset:USD/2"
	_, err := s.OpenOrUpdateAlert(ctx, openIn)
	require.NoError(t, err)

	eurIn := defaultOpenInput(t, ruleID, evID)
	eurIn.Fingerprint = "asset:EUR/2"
	_, err = s.OpenOrUpdateAlert(ctx, eurIn)
	require.NoError(t, err)

	q := NewGetAlertsQuery(NewPaginatedQueryOptions(AlertsFilters{}).WithPageSize(15))
	cursor, err := s.ListAlerts(ctx, q)
	require.NoError(t, err)
	require.Len(t, cursor.Data, 2)

	// Filter by fingerprint
	filtered := NewGetAlertsQuery(
		NewPaginatedQueryOptions(AlertsFilters{}).
			WithQueryBuilder(query.Match("fingerprint", "asset:EUR/2")).
			WithPageSize(15),
	)
	cursor, err = s.ListAlerts(ctx, filtered)
	require.NoError(t, err)
	require.Len(t, cursor.Data, 1)
	require.Equal(t, "asset:EUR/2", cursor.Data[0].Fingerprint)

	// Filter by ruleID
	byRule := NewGetAlertsQuery(
		NewPaginatedQueryOptions(AlertsFilters{}).
			WithQueryBuilder(query.Match("ruleID", ruleID)).
			WithPageSize(15),
	)
	cursor, err = s.ListAlerts(ctx, byRule)
	require.NoError(t, err)
	require.Len(t, cursor.Data, 2)

	// Unknown key surfaces ErrInvalidQuery
	bad := NewGetAlertsQuery(
		NewPaginatedQueryOptions(AlertsFilters{}).
			WithQueryBuilder(query.Match("not_a_column", true)).
			WithPageSize(15),
	)
	_, err = s.ListAlerts(ctx, bad)
	require.ErrorIs(t, err, ErrInvalidQuery)

	// Non-$match on enum-style key is rejected
	wrongOp := NewGetAlertsQuery(
		NewPaginatedQueryOptions(AlertsFilters{}).
			WithQueryBuilder(query.Gt("status", "OPEN")).
			WithPageSize(15),
	)
	_, err = s.ListAlerts(ctx, wrongOp)
	require.ErrorIs(t, err, ErrInvalidQuery)
}

// TestRunInTx_RollsBackOnError confirms the transactional helper actually
// rolls back when its callback errors — guarantees that the evaluation +
// alert orchestration is atomic. The rolled-back insert must leave no alert
// AND no event behind.
func TestRunInTx_RollsBackOnError(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)

	wantErr := errors.New("callback failed")
	err := s.RunInTx(ctx, func(ctx context.Context, txStore *Storage) error {
		_, _ = txStore.OpenOrUpdateAlert(ctx, defaultOpenInput(t, ruleID, evID))
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)

	fps, err := s.ListActiveAlertFingerprints(ctx, ruleID)
	require.NoError(t, err)
	require.Empty(t, fps, "rollback must discard the alert")

	var count int
	err = s.db.NewSelect().
		Model((*models.AlertEvent)(nil)).
		ColumnExpr("count(*)").
		Scan(ctx, &count)
	require.NoError(t, err)
	require.Zero(t, count, "rollback must discard the appended event")
}
