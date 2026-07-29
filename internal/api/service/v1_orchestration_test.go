package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
)

// --- in-memory store --------------------------------------------------------

// fakeV1Store implements just enough of the Store interface to exercise the
// V1 orchestration flows. Mirrors the real storage layer's semantics for:
//   - OpenOrUpdateAlert dedup (one stable alert per (rule, fingerprint))
//   - AutoResolveAlert (closes active with kind=auto)
//   - Reopen happens IN PLACE on the same alert id (no parent chain)
//   - Every transition appends one row to alert_event
type fakeV1Store struct {
	auditEntries  []models.AuditEntry
	ruleRevisions []models.RuleRevision
	periodSeals   []models.PeriodSeal

	rules       map[uuid.UUID]*models.Rule
	evaluations map[uuid.UUID]*models.Evaluation
	alerts      map[uuid.UUID]*models.Alert // keyed by alert id
	byFP        map[string]uuid.UUID        // "ruleID|fingerprint" → alert id
	events      []*models.AlertEvent        // chronological, append-only
}

func newFakeV1Store() *fakeV1Store {
	return &fakeV1Store{
		rules:       map[uuid.UUID]*models.Rule{},
		evaluations: map[uuid.UUID]*models.Evaluation{},
		alerts:      map[uuid.UUID]*models.Alert{},
		byFP:        map[string]uuid.UUID{},
	}
}

func fpKey(ruleID uuid.UUID, fp, periodID string) string {
	if periodID == "" {
		periodID = models.ContinuousPeriod
	}
	return ruleID.String() + "|" + fp + "|" + periodID
}

func (f *fakeV1Store) Ping(context.Context) error { return nil }

// Legacy /policies methods — not exercised here.
func (f *fakeV1Store) CreatePolicy(context.Context, *models.Policy) error { return nil }
func (f *fakeV1Store) DeletePolicy(context.Context, uuid.UUID) error      { return nil }
func (f *fakeV1Store) GetPolicy(context.Context, uuid.UUID) (*models.Policy, error) {
	return nil, nil
}
func (f *fakeV1Store) ListPolicies(context.Context, storage.GetPoliciesQuery) (*bunpaginate.Cursor[models.Policy], error) {
	return nil, nil
}
func (f *fakeV1Store) CreateReconciliation(context.Context, *models.Reconciliation) error {
	return nil
}
func (f *fakeV1Store) GetReconciliation(context.Context, uuid.UUID) (*models.Reconciliation, error) {
	return nil, nil
}
func (f *fakeV1Store) ListReconciliations(context.Context, storage.GetReconciliationsQuery) (*bunpaginate.Cursor[models.Reconciliation], error) {
	return nil, nil
}

// Rule
func (f *fakeV1Store) CreateRule(_ context.Context, r *models.Rule) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.Revision == 0 {
		r.Revision = 1
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = now
	}
	copy := *r
	f.rules[r.ID] = &copy
	return nil
}
func (f *fakeV1Store) GetRule(_ context.Context, id uuid.UUID) (*models.Rule, error) {
	r, ok := f.rules[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	copy := *r
	return &copy, nil
}
func (f *fakeV1Store) AssertRuleRevision(_ context.Context, id uuid.UUID, revision int64) error {
	r, ok := f.rules[id]
	if !ok {
		return storage.ErrNotFound
	}
	if !r.Enabled || r.Revision != revision {
		return storage.ErrObsoleteJob
	}
	return nil
}
func (f *fakeV1Store) DeleteRule(_ context.Context, id uuid.UUID) error {
	if _, ok := f.rules[id]; !ok {
		return storage.ErrNotFound
	}
	delete(f.rules, id)
	return nil
}
func (f *fakeV1Store) PatchRule(_ context.Context, id uuid.UUID, p storage.RulePatch) error {
	r, ok := f.rules[id]
	if !ok {
		return storage.ErrNotFound
	}
	if p.ExpectedRevision != nil && r.Revision != *p.ExpectedRevision {
		return storage.ErrRuleRevisionConflict
	}
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.ExplanationCEL != nil {
		r.ExplanationCEL = *p.ExplanationCEL
	}
	if p.Enabled != nil {
		r.Enabled = *p.Enabled
	}
	if p.TemplateKind != nil {
		r.TemplateKind = *p.TemplateKind
	}
	if p.TemplateSpec != nil {
		r.TemplateSpec = p.TemplateSpec
	}
	r.Revision++
	r.UpdatedAt = time.Now().UTC()
	return nil
}
func (f *fakeV1Store) ListRules(context.Context, storage.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error) {
	return nil, nil
}

// Evaluation
func (f *fakeV1Store) CreateEvaluation(_ context.Context, ev *models.Evaluation) error {
	if ev.ID == uuid.Nil {
		ev.ID = uuid.New()
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	copy := *ev
	f.evaluations[ev.ID] = &copy
	return nil
}
func (f *fakeV1Store) GetEvaluation(_ context.Context, id uuid.UUID) (*models.Evaluation, error) {
	ev, ok := f.evaluations[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	copy := *ev
	return &copy, nil
}
func (f *fakeV1Store) ListEvaluations(context.Context, storage.GetEvaluationsQuery) (*bunpaginate.Cursor[models.Evaluation], error) {
	return nil, nil
}

// recordEvent is the fake's mirror of storage.appendAlertEvent. Centralised so
// every transition writes the same shape and the test surface for events stays
// honest.
func (f *fakeV1Store) recordEvent(alertID uuid.UUID, t models.AlertEventType, prev *models.AlertStatus, next models.AlertStatus, evalID *uuid.UUID, payload json.RawMessage, at time.Time) *models.AlertEvent {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	event := &models.AlertEvent{
		ID:           uuid.New(),
		AlertID:      alertID,
		EvaluationID: evalID,
		Type:         t,
		PrevStatus:   prev,
		NewStatus:    next,
		Payload:      payload,
		At:           at,
		CreatedAt:    time.Now().UTC(),
	}
	f.events = append(f.events, event)
	return event
}

// Alert — the load-bearing part of the orchestration tests.
func (f *fakeV1Store) OpenOrUpdateAlert(_ context.Context, in storage.OpenAlertInput) (*storage.OpenAlertResult, error) {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	if in.PeriodID == "" {
		in.PeriodID = models.ContinuousPeriod
	}
	key := fpKey(in.RuleID, in.Fingerprint, in.PeriodID)

	if id, ok := f.byFP[key]; ok {
		alert := f.alerts[id]
		prev := alert.Status
		reopened := prev == models.AlertResolved

		alert.Status = models.AlertOpen
		alert.LastSeenAt = in.OccurredAt
		alert.LastEvaluationID = in.EvaluationID
		alert.OccurrenceCount++
		alert.Evidence = in.Evidence
		alert.UpdatedAt = time.Now().UTC()
		if reopened {
			alert.Resolution = nil
			alert.Ack = nil
		} else if prev == models.AlertAcknowledged {
			alert.Ack = nil
		}
		event := f.recordEvent(alert.ID, models.AlertEventFail, &prev, models.AlertOpen, &in.EvaluationID, in.Evidence, in.OccurredAt)
		copy := *alert
		return &storage.OpenAlertResult{Alert: &copy, Event: event, Created: false, Reopened: reopened}, nil
	}

	fresh := &models.Alert{
		ID:               uuid.New(),
		RuleID:           in.RuleID,
		Fingerprint:      in.Fingerprint,
		PeriodID:         in.PeriodID,
		Status:           models.AlertOpen,
		Severity:         in.Severity,
		FirstSeenAt:      in.OccurredAt,
		LastSeenAt:       in.OccurredAt,
		OccurrenceCount:  1,
		LastEvaluationID: in.EvaluationID,
		Evidence:         in.Evidence,
		Labels:           in.Labels,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	f.alerts[fresh.ID] = fresh
	f.byFP[key] = fresh.ID
	event := f.recordEvent(fresh.ID, models.AlertEventFail, nil, models.AlertOpen, &in.EvaluationID, in.Evidence, in.OccurredAt)
	copy := *fresh
	return &storage.OpenAlertResult{Alert: &copy, Event: event, Created: true}, nil
}

func (f *fakeV1Store) AutoResolveAlert(_ context.Context, ruleID uuid.UUID, fingerprint, periodID string, evID uuid.UUID, at time.Time) (*models.Alert, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	id, ok := f.byFP[fpKey(ruleID, fingerprint, periodID)]
	if !ok {
		return nil, nil
	}
	alert := f.alerts[id]
	if alert.Status != models.AlertOpen && alert.Status != models.AlertAcknowledged {
		return nil, nil
	}
	prev := alert.Status
	res := &models.Resolution{Kind: models.ResolutionAuto, By: "system", At: at}
	alert.Status = models.AlertResolved
	alert.Resolution = res
	alert.LastEvaluationID = evID
	alert.UpdatedAt = time.Now().UTC()
	payload, _ := json.Marshal(res)
	f.recordEvent(alert.ID, models.AlertEventPass, &prev, models.AlertResolved, &evID, payload, at)
	copy := *alert
	return &copy, nil
}

func (f *fakeV1Store) AckAlert(_ context.Context, id uuid.UUID, ack *models.Ack) (*models.Alert, error) {
	alert, ok := f.alerts[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	if alert.Status == models.AlertResolved {
		return nil, storage.ErrNotFound
	}
	if alert.Status == models.AlertAcknowledged {
		copy := *alert
		return &copy, nil // no-op, preserve original ack
	}
	prev := alert.Status
	alert.Status = models.AlertAcknowledged
	alert.Ack = ack
	alert.UpdatedAt = time.Now().UTC()
	payload, _ := json.Marshal(ack)
	f.recordEvent(alert.ID, models.AlertEventAck, &prev, models.AlertAcknowledged, nil, payload, ack.At)
	copy := *alert
	return &copy, nil
}

func (f *fakeV1Store) ResolveAlertManual(_ context.Context, id uuid.UUID, res *models.Resolution) (*models.Alert, error) {
	return f.applyFakeResolution(id, res, models.AlertEventResolve)
}

func (f *fakeV1Store) AcceptAlert(_ context.Context, id uuid.UUID, res *models.Resolution) (*models.Alert, error) {
	return f.applyFakeResolution(id, res, models.AlertEventAccept)
}

func (f *fakeV1Store) applyFakeResolution(id uuid.UUID, res *models.Resolution, eventType models.AlertEventType) (*models.Alert, error) {
	alert, ok := f.alerts[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	if alert.Status == models.AlertResolved {
		return nil, storage.ErrNotFound
	}
	prev := alert.Status
	// Mirror storage.applyResolution: business acceptance freezes the evidence
	// from the locked row atomically with the transition.
	if res.Kind == models.ResolutionAcceptedByBusiness && len(res.EvidenceSnapshot) == 0 {
		if len(alert.Evidence) > 0 {
			res.EvidenceSnapshot = alert.Evidence
		} else {
			res.EvidenceSnapshot = json.RawMessage("null")
		}
	}
	alert.Status = models.AlertResolved
	alert.Resolution = res
	alert.UpdatedAt = time.Now().UTC()
	payload, _ := json.Marshal(res)
	f.recordEvent(alert.ID, eventType, &prev, models.AlertResolved, nil, payload, res.At)
	copy := *alert
	return &copy, nil
}

func (f *fakeV1Store) SnoozeAlert(_ context.Context, id uuid.UUID, until time.Time, by, note string) (*models.Alert, error) {
	alert, ok := f.alerts[id]
	if !ok || alert.Status == models.AlertResolved {
		return nil, storage.ErrNotFound
	}
	snooze := &models.Snooze{Until: until, By: by, At: time.Now().UTC(), Note: note}
	alert.Snooze = snooze
	prev := alert.Status
	payload, _ := json.Marshal(snooze)
	f.recordEvent(alert.ID, models.AlertEventSnooze, &prev, alert.Status, nil, payload, snooze.At)
	copy := *alert
	return &copy, nil
}

func (f *fakeV1Store) UnsnoozeAlert(_ context.Context, id uuid.UUID, by string) (*models.Alert, error) {
	alert, ok := f.alerts[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	if alert.Snooze == nil {
		copy := *alert
		return &copy, nil
	}
	alert.Snooze = nil
	prev := alert.Status
	payload, _ := json.Marshal(map[string]string{"by": by})
	f.recordEvent(alert.ID, models.AlertEventUnsnooze, &prev, alert.Status, nil, payload, time.Now().UTC())
	copy := *alert
	return &copy, nil
}

func (f *fakeV1Store) GetAlert(_ context.Context, id uuid.UUID) (*models.Alert, error) {
	alert, ok := f.alerts[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	copy := *alert
	return &copy, nil
}
func (f *fakeV1Store) ListAlerts(context.Context, storage.GetAlertsQuery) (*bunpaginate.Cursor[models.Alert], error) {
	return nil, nil
}

func (f *fakeV1Store) ListAlertEvents(_ context.Context, alertID uuid.UUID, _ storage.GetAlertEventsQuery) (*bunpaginate.Cursor[models.AlertEvent], error) {
	out := []models.AlertEvent{}
	for _, e := range f.events {
		if e.AlertID == alertID {
			out = append(out, *e)
		}
	}
	return &bunpaginate.Cursor[models.AlertEvent]{Data: out}, nil
}

// Audit journal read paths. Sealing is not faked: it needs a real transaction.
func (f *fakeV1Store) ListAuditEntries(_ context.Context, filters storage.AuditEntryFilters, afterSeq int64, limit int) ([]models.AuditEntry, int64, error) {
	if limit <= 0 {
		limit = 100
	}
	out := []models.AuditEntry{}
	for _, entry := range f.auditEntries {
		if entry.Sequence <= afterSeq {
			continue
		}
		if len(filters.Kinds) > 0 && !slices.Contains(filters.Kinds, entry.Kind) {
			continue
		}
		if filters.PeriodID != "" && entry.PeriodID != filters.PeriodID {
			continue
		}
		out = append(out, entry)
		if len(out) == limit {
			break
		}
	}
	return out, 0, nil
}

func (f *fakeV1Store) GetAuditEntry(_ context.Context, sequence int64) (*models.AuditEntry, error) {
	for i := range f.auditEntries {
		if f.auditEntries[i].Sequence == sequence {
			return &f.auditEntries[i], nil
		}
	}
	return nil, storage.ErrNotFound
}

func (f *fakeV1Store) ChainHead(context.Context) (int64, []byte, error) {
	if len(f.auditEntries) == 0 {
		return 0, nil, nil
	}
	last := f.auditEntries[len(f.auditEntries)-1]
	return last.Sequence, last.Hash, nil
}

func (f *fakeV1Store) VerifyChain(_ context.Context, fromSeq, toSeq int64) (*models.ChainVerification, error) {
	return &models.ChainVerification{OK: true, FirstSequence: fromSeq, LastSequence: toSeq}, nil
}

func (f *fakeV1Store) ListRuleRevisions(_ context.Context, ruleID uuid.UUID) ([]models.RuleRevision, error) {
	out := []models.RuleRevision{}
	for _, rev := range f.ruleRevisions {
		if rev.RuleID == ruleID {
			out = append(out, rev)
		}
	}
	return out, nil
}

func (f *fakeV1Store) GetRuleRevision(_ context.Context, ruleID uuid.UUID, revision int64) (*models.RuleRevision, error) {
	for i := range f.ruleRevisions {
		if f.ruleRevisions[i].RuleID == ruleID && f.ruleRevisions[i].Revision == revision {
			return &f.ruleRevisions[i], nil
		}
	}
	return nil, storage.ErrNotFound
}

func (f *fakeV1Store) ListPeriodSeals(context.Context) ([]models.PeriodSeal, error) {
	return f.periodSeals, nil
}

func (f *fakeV1Store) GetPeriodSeal(_ context.Context, periodID string) (*models.PeriodSeal, error) {
	for i := range f.periodSeals {
		if f.periodSeals[i].PeriodID == periodID {
			return &f.periodSeals[i], nil
		}
	}
	return nil, storage.ErrNotFound
}

func (f *fakeV1Store) VerifySealSignature(context.Context, *models.PeriodSeal) (bool, string, error) {
	return true, "", nil
}

func (f *fakeV1Store) ListVerificationKeys(context.Context) ([]storage.VerificationKey, error) {
	return nil, nil
}

func (f *fakeV1Store) ListActiveAlertFingerprints(_ context.Context, ruleID uuid.UUID, periodID string) ([]string, error) {
	if periodID == "" {
		periodID = models.ContinuousPeriod
	}
	out := []string{}
	for _, alert := range f.alerts {
		if alert.RuleID != ruleID || alert.PeriodID != periodID {
			continue
		}
		if alert.Status != models.AlertOpen && alert.Status != models.AlertAcknowledged {
			continue
		}
		out = append(out, alert.Fingerprint)
	}
	return out, nil
}

// activeFor returns the alert for (rule, fingerprint) if it's currently
// OPEN/ACKNOWLEDGED, else nil. Helper for assertions.
func (f *fakeV1Store) activeFor(ruleID uuid.UUID, fingerprint string) *models.Alert {
	id, ok := f.byFP[fpKey(ruleID, fingerprint, models.ContinuousPeriod)]
	if !ok {
		return nil
	}
	alert := f.alerts[id]
	if alert.Status == models.AlertOpen || alert.Status == models.AlertAcknowledged {
		return alert
	}
	return nil
}

// alertFor returns the alert row (any status) for (rule, fingerprint), or nil.
func (f *fakeV1Store) alertFor(ruleID uuid.UUID, fingerprint string) *models.Alert {
	id, ok := f.byFP[fpKey(ruleID, fingerprint, models.ContinuousPeriod)]
	if !ok {
		return nil
	}
	return f.alerts[id]
}

// eventsFor returns all events for the alert, chronologically.
func (f *fakeV1Store) eventsFor(alertID uuid.UUID) []*models.AlertEvent {
	out := []*models.AlertEvent{}
	for _, e := range f.events {
		if e.AlertID == alertID {
			out = append(out, e)
		}
	}
	return out
}

// --- ledger/payments fakes (mirror the kernel's resolver interfaces) -------

type orchestrationLedger struct {
	current map[string]*big.Int // asset → amount, mutated between evaluations
	failErr error
}

func (f *orchestrationLedger) AggregateBalance(_ context.Context, _ string, _ json.RawMessage, _ time.Time) (map[string]*big.Int, error) {
	if f.failErr != nil {
		return nil, f.failErr
	}
	out := map[string]*big.Int{}
	for k, v := range f.current {
		out[k] = new(big.Int).Set(v)
	}
	return out, nil
}
func (f *orchestrationLedger) ListAccounts(context.Context, string, json.RawMessage, time.Time, int) ([]engine.Account, error) {
	return nil, errors.New("ListAccounts not implemented")
}

type orchestrationPayments struct {
	current map[string]*big.Int
	// afterFirst, when non-nil, is returned by every PoolBalance call after the
	// first. Tests use it to prove snapshot CEL never repeats a pool read.
	afterFirst map[string]*big.Int
	calls      int
}

func (f *orchestrationPayments) PoolBalance(context.Context, string, *time.Time) (map[string]*big.Int, error) {
	f.calls++
	src := f.current
	if f.calls > 1 && f.afterFirst != nil {
		src = f.afterFirst
	}
	out := map[string]*big.Int{}
	for k, v := range src {
		out[k] = new(big.Int).Set(v)
	}
	return out, nil
}

// --- helpers ----------------------------------------------------------------

func newOrchestrationService(t *testing.T, l *orchestrationLedger, p *orchestrationPayments) (*Service, *fakeV1Store) {
	t.Helper()
	store := newFakeV1Store()
	res := engine.Resolvers{Ledger: l, Payments: p}
	eng, err := engine.New(res, engine.DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	svc := NewService(store, nil, eng, templates.DefaultRegistry(), res)
	return svc, store
}

func driftSpec(t *testing.T, ledger, _ string, pool string, tol map[string]int64) json.RawMessage {
	t.Helper()
	spec := templates.DriftSpec{
		Ledger:         ledger,
		LedgerQuery:    json.RawMessage(`{}`),
		PaymentsPoolID: pool,
		Tolerance:      tol,
	}
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	return b
}

func mustCreateRule(t *testing.T, svc *Service, spec json.RawMessage) *models.Rule {
	t.Helper()
	rule, err := svc.CreateRule(context.Background(), &CreateRuleRequest{
		Name:         "test-rule",
		TemplateKind: models.TemplateLedgerVsPoolDrift,
		TemplateSpec: spec,
		Severity:     models.SeverityHigh,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	return rule
}

// --- tests ------------------------------------------------------------------

func TestCreateRule_PersistsExplanationCEL(t *testing.T) {
	svc, store := newOrchestrationService(t, &orchestrationLedger{current: map[string]*big.Int{}}, &orchestrationPayments{current: map[string]*big.Int{}})
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))
	if rule.ExplanationCEL == "" {
		t.Fatalf("expected explanation_cel to be populated")
	}
	saved, _ := store.GetRule(context.Background(), rule.ID)
	if saved.ExplanationCEL != rule.ExplanationCEL {
		t.Errorf("explanation_cel not persisted")
	}
}

func TestCreateRule_RejectsUnknownTemplate(t *testing.T) {
	svc, _ := newOrchestrationService(t, &orchestrationLedger{}, &orchestrationPayments{})
	_, err := svc.CreateRule(context.Background(), &CreateRuleRequest{
		Name:         "r",
		TemplateKind: models.TemplateKind("not_a_real_template"),
		TemplateSpec: json.RawMessage(`{}`),
		Severity:     models.SeverityHigh,
	})
	if !errors.Is(err, templates.ErrUnknownTemplate) {
		t.Errorf("expected ErrUnknownTemplate, got %v", err)
	}
}

func TestCreateRule_RejectsInvalidSpec(t *testing.T) {
	svc, _ := newOrchestrationService(t, &orchestrationLedger{}, &orchestrationPayments{})
	_, err := svc.CreateRule(context.Background(), &CreateRuleRequest{
		Name:         "r",
		TemplateKind: models.TemplateLedgerVsPoolDrift,
		TemplateSpec: json.RawMessage(`{"ledger": ""}`), // missing required fields
		Severity:     models.SeverityHigh,
	})
	if !errors.Is(err, templates.ErrInvalidSpec) {
		t.Errorf("expected ErrInvalidSpec, got %v", err)
	}
}

// A cron schedule's expression is validated at create time.
func TestCreateRuleRequest_Validate_CronSchedule(t *testing.T) {
	base := func() *CreateRuleRequest {
		return &CreateRuleRequest{Name: "r", TemplateKind: models.TemplateAccountThreshold, TemplateSpec: json.RawMessage(`{}`)}
	}
	cases := map[string]struct {
		sched   *models.Schedule
		wantErr bool
	}{
		"valid cron":         {&models.Schedule{Kind: models.ScheduleCron, Expr: "*/15 * * * *"}, false},
		"valid cron with tz": {&models.Schedule{Kind: models.ScheduleCron, Expr: "0 9 * * *", TZ: "America/New_York"}, false},
		"invalid cron expr":  {&models.Schedule{Kind: models.ScheduleCron, Expr: "not a cron"}, true},
		"cron without expr":  {&models.Schedule{Kind: models.ScheduleCron}, true},
		"on_demand skips":    {&models.Schedule{Kind: models.ScheduleOnDemand}, false},
		"nil schedule":       {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := base()
			req.Schedule = tc.sched
			err := req.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestEvaluate_PassNoAlerts(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-100)}}
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))

	ev, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}
	if ev.Result != models.EvaluationPass {
		t.Errorf("expected PASS, got %v", ev.Result)
	}
	if len(store.alerts) != 0 {
		t.Errorf("expected no alerts, got %d", len(store.alerts))
	}
}

func TestEvaluate_PreservesSourcePITsWhenNoOutcomes(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{}}
	p := &orchestrationPayments{current: map[string]*big.Int{}}
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))

	pit := time.Date(2026, 6, 17, 16, 0, 0, 0, time.UTC)
	ev, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: pit, SafetyMargin: 30 * time.Second})
	if err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}
	if len(ev.PitPerSource) != 2 {
		t.Fatalf("expected 2 source PIT entries, got %d: %v", len(ev.PitPerSource), ev.PitPerSource)
	}
	if got, ok := ev.PitPerSource["ledger:buildr#0"]; !ok || !got.Equal(pit.Add(-30*time.Second)) {
		t.Fatalf("ledger PIT = %v, want %v", got, pit.Add(-30*time.Second))
	}
	if got, ok := ev.PitPerSource["pool:pool#0"]; !ok || !got.Equal(pit.Add(-30*time.Second)) {
		t.Fatalf("pool PIT = %v, want %v", got, pit.Add(-30*time.Second))
	}
	stored, err := store.GetEvaluation(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("GetEvaluation: %v", err)
	}
	if len(stored.PitPerSource) != 2 {
		t.Fatalf("stored evaluation should preserve 2 source PIT entries, got %d: %v", len(stored.PitPerSource), stored.PitPerSource)
	}
}

// TestEvaluate_DisabledRuleIsValidationError locks the fix for NumaryBot
// r3593989719: evaluating a disabled rule is a predictable client-side
// invalid-state, so EvaluateRule must return an ErrValidation-wrapped error
// (→ HTTP 400 via handleServiceErrors), not a bare error (→ HTTP 500).
func TestEvaluate_DisabledRuleIsValidationError(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-100)}}
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))

	disabled := false
	if err := store.PatchRule(context.Background(), rule.ID, storage.RulePatch{Enabled: &disabled}); err != nil {
		t.Fatalf("disable rule: %v", err)
	}

	_, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err == nil {
		t.Fatal("expected an error evaluating a disabled rule")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("disabled-rule error must wrap ErrValidation (→ 400), got: %v", err)
	}
}

// TestEvaluate_UnknownSourcePITKeyRejected: a sourcePITs key that names no
// source of the rule's template is a client mistake (typo, wrong index) that
// would otherwise be silently ignored — it must be rejected with an
// ErrValidation-wrapped error (→ 400), not accepted-and-ignored.
func TestEvaluate_UnknownSourcePITKeyRejected(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-100)}}
	svc, _ := newOrchestrationService(t, l, p)
	// driftSpec uses ledger "buildr" + pool "pool" → valid keys are
	// ledger:buildr#0 and pool:pool#0.
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))
	ctx := context.Background()

	// Unknown key → rejected.
	_, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{
		PIT:        time.Now(),
		SourcePITs: map[string]time.Time{"pool:typo#0": time.Now().Add(-time.Hour)},
	})
	if err == nil {
		t.Fatal("expected an error for an unknown sourcePITs key")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("unknown-key error must wrap ErrValidation (→ 400), got: %v", err)
	}

	// A valid key is accepted (the evaluation runs to completion).
	if _, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{
		PIT:        time.Now(),
		SourcePITs: map[string]time.Time{"ledger:buildr#0": time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("valid sourcePITs key must be accepted, got: %v", err)
	}
}

// TestEvaluate_LifecycleInPlace — the headline test for the new model: through
// one rule we exercise open → update → auto-resolve → REOPEN-IN-PLACE in
// sequence. The same alert id stays valid across the entire lifecycle; the
// history lives in alert_event rows, not in chained alert rows.
func TestEvaluate_LifecycleInPlace(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(350)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-300)}} // drift 50
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))
	ctx := context.Background()

	// 1. First failing eval → opens alert. occurrence_count=1, first 'fail' event.
	if _, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("eval1: %v", err)
	}
	alert := store.activeFor(rule.ID, "asset:USD/2")
	if alert == nil {
		t.Fatalf("expected active alert after first fail")
	}
	if alert.OccurrenceCount != 1 {
		t.Errorf("occurrenceCount = %d, want 1", alert.OccurrenceCount)
	}
	originalID := alert.ID

	// 2. Second failing eval → SAME alert id, occurrence_count=2, second event.
	if _, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("eval2: %v", err)
	}
	alert = store.activeFor(rule.ID, "asset:USD/2")
	if alert == nil || alert.ID != originalID {
		t.Fatalf("expected same alert updated, got %v", alert)
	}
	if alert.OccurrenceCount != 2 {
		t.Errorf("occurrenceCount = %d, want 2", alert.OccurrenceCount)
	}

	// 3. Make the world consistent → eval passes, alert auto-resolves IN PLACE.
	p.current["USD/2"] = big.NewInt(-350)
	if _, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("eval3: %v", err)
	}
	if active := store.activeFor(rule.ID, "asset:USD/2"); active != nil {
		t.Fatalf("expected no active alert after pass, got %v", active)
	}
	resolved := store.alertFor(rule.ID, "asset:USD/2")
	if resolved == nil || resolved.ID != originalID {
		t.Fatalf("alert id changed across resolve — expected in-place, got %v", resolved)
	}
	if resolved.Status != models.AlertResolved {
		t.Errorf("expected RESOLVED, got %v", resolved.Status)
	}
	if resolved.Resolution == nil || resolved.Resolution.Kind != models.ResolutionAuto {
		t.Errorf("expected resolution.kind=auto, got %v", resolved.Resolution)
	}

	// 4. Break it again → SAME alert reopens IN PLACE (no new row, no parent
	//    chain). occurrence_count keeps incrementing (lifetime count).
	p.current["USD/2"] = big.NewInt(-200)
	if _, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("eval4: %v", err)
	}
	if got := len(store.alerts); got != 1 {
		t.Fatalf("expected exactly 1 alert across the lifecycle, got %d", got)
	}
	reopened := store.alertFor(rule.ID, "asset:USD/2")
	if reopened.ID != originalID {
		t.Fatalf("reopen produced a new alert id (%v) instead of in-place transition (%v)", reopened.ID, originalID)
	}
	if reopened.Status != models.AlertOpen {
		t.Errorf("expected OPEN after reopen, got %v", reopened.Status)
	}
	if reopened.OccurrenceCount != 3 {
		t.Errorf("lifetime occurrenceCount = %d, want 3 (2 fails + 1 reopen)", reopened.OccurrenceCount)
	}
	if reopened.Resolution != nil {
		t.Errorf("reopen must clear prior resolution, got %+v", reopened.Resolution)
	}

	// 5. The event log captures the full timeline: 2x fail (initial + repeat),
	//    1x pass (auto-resolve), 1x fail (reopen, prev=RESOLVED).
	events := store.eventsFor(originalID)
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d (%v)", len(events), events)
	}

	if e := events[0]; e.Type != models.AlertEventFail || e.PrevStatus != nil || e.NewStatus != models.AlertOpen {
		t.Errorf("event[0] expected first-fail (prev=nil → OPEN), got %+v", e)
	}
	if e := events[1]; e.Type != models.AlertEventFail || e.PrevStatus == nil || *e.PrevStatus != models.AlertOpen {
		t.Errorf("event[1] expected fail-on-open, got %+v", e)
	}
	if e := events[2]; e.Type != models.AlertEventPass || e.NewStatus != models.AlertResolved {
		t.Errorf("event[2] expected pass→resolved, got %+v", e)
	}
	if e := events[3]; !e.IsReopen() {
		t.Errorf("event[3] expected reopen (fail + prev=RESOLVED), got %+v", e)
	}
}

// TestEvaluate_FingerprintDisappears regression for the
// "alert stays OPEN forever" gap: when an asset that was previously in
// drift is cleared by *removing* it from both sides (rather than balancing
// it), the asset union no longer contains that asset and the next evaluation
// produces no outcome for it. The active alert must still be auto-resolved
// — driven by the post-loop sweep against ListActiveAlertFingerprints.
func TestEvaluate_FingerprintDisappears(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(350)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-300)}} // drift 50
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))

	// 1. Open an alert on USD/2.
	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("eval1: %v", err)
	}
	if store.activeFor(rule.ID, "asset:USD/2") == nil {
		t.Fatalf("expected active USD/2 alert after first fail")
	}

	// 2. Remove USD/2 entirely from both sources — simulating the asset
	//    being unwound on both sides. The template's asset union no longer
	//    contains USD/2 → no outcome carries that fingerprint.
	delete(l.current, "USD/2")
	delete(p.current, "USD/2")

	ev, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err != nil {
		t.Fatalf("eval2: %v", err)
	}
	if ev.Result != models.EvaluationPass {
		t.Errorf("eval2 expected PASS, got %v", ev.Result)
	}

	// 3. USD/2 alert must now be RESOLVED via the disappearance sweep.
	if store.activeFor(rule.ID, "asset:USD/2") != nil {
		t.Fatalf("expected USD/2 alert auto-resolved after fingerprint vanished")
	}
	resolved := store.alertFor(rule.ID, "asset:USD/2")
	if resolved == nil || resolved.Status != models.AlertResolved {
		t.Fatalf("expected RESOLVED USD/2 alert, got %v", resolved)
	}
	if resolved.Resolution == nil || resolved.Resolution.Kind != models.ResolutionAuto {
		t.Errorf("expected resolution.kind=auto, got %v", resolved.Resolution)
	}
}

func TestEvaluate_EngineError_RaisesMetaAlert(t *testing.T) {
	l := &orchestrationLedger{failErr: errors.New("ledger upstream timeout")}
	svc, store := newOrchestrationService(t, l, &orchestrationPayments{current: map[string]*big.Int{}})
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))

	ev, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}
	if ev.Result != models.EvaluationError {
		t.Errorf("expected ERROR result, got %v", ev.Result)
	}
	if ev.Error == "" {
		t.Errorf("expected non-empty Error on evaluation row")
	}

	if got := len(store.alerts); got != 1 {
		t.Fatalf("expected 1 alert (the meta), got %d", got)
	}
	var meta *models.Alert
	for _, a := range store.alerts {
		meta = a
		break
	}
	if meta.Fingerprint != engineErrorFingerprint {
		t.Errorf("expected engine.error fingerprint, got %q", meta.Fingerprint)
	}
	if meta.Labels["kind"] != engineErrorFingerprint {
		t.Errorf("expected labels.kind = engine.error, got %v", meta.Labels)
	}
	if meta.Severity != models.SeverityHigh {
		t.Errorf("expected meta-alert severity=high, got %v", meta.Severity)
	}
}

func TestAckResolveAccept_StateTransitions(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-50)}} // drift 50
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))

	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}
	alert := store.activeFor(rule.ID, "asset:USD/2")
	if alert == nil {
		t.Fatalf("expected open alert to test transitions")
	}

	// Ack
	acked, err := svc.AckAlert(context.Background(), alert.ID, &AckAlertRequest{By: "ops@buildr.com", Note: "investigating"})
	if err != nil {
		t.Fatalf("AckAlert: %v", err)
	}
	if acked.Status != models.AlertAcknowledged {
		t.Errorf("expected ACKNOWLEDGED, got %v", acked.Status)
	}
	if acked.Ack == nil || acked.Ack.By != "ops@buildr.com" {
		t.Errorf("expected ack.by populated, got %v", acked.Ack)
	}

	// Resolve fixed_by_booking
	resolved, err := svc.ResolveAlert(context.Background(), alert.ID, &ResolveAlertRequest{
		By:              "ops@buildr.com",
		Note:            "posted correction tx_abc",
		TransactionRefs: []string{"tx_abc"},
	})
	if err != nil {
		t.Fatalf("ResolveAlert: %v", err)
	}
	if resolved.Status != models.AlertResolved {
		t.Errorf("expected RESOLVED, got %v", resolved.Status)
	}
	if resolved.Resolution == nil || resolved.Resolution.Kind != models.ResolutionFixedByBooking {
		t.Errorf("expected fixed_by_booking, got %v", resolved.Resolution)
	}
	if len(resolved.Resolution.TransactionRefs) != 1 || resolved.Resolution.TransactionRefs[0] != "tx_abc" {
		t.Errorf("expected tx_abc in transactionRefs, got %v", resolved.Resolution.TransactionRefs)
	}
}

func TestAccept_RequiresNote(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-50)}}
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))
	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}
	alert := store.activeFor(rule.ID, "asset:USD/2")

	_, err := svc.AcceptAlert(context.Background(), alert.ID, &AcceptAlertRequest{By: "treasurer", Note: ""})
	if err == nil {
		t.Fatalf("expected error on accept-without-note")
	}
	if msg := err.Error(); !contains(msg, "note") {
		t.Errorf("expected 'note' in error, got %q", msg)
	}
}

func TestAccept_FreezesEvidence(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(100)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-50)}}
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))
	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()}); err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}
	alert := store.activeFor(rule.ID, "asset:USD/2")

	out, err := svc.AcceptAlert(context.Background(), alert.ID, &AcceptAlertRequest{
		By:   "treasurer",
		Note: "settlement lag",
	})
	if err != nil {
		t.Fatalf("AcceptAlert: %v", err)
	}
	if out.Resolution == nil || out.Resolution.Kind != models.ResolutionAcceptedByBusiness {
		t.Fatalf("expected accepted_by_business, got %v", out.Resolution)
	}
	// The evidence snapshot is captured inside AcceptAlert's transaction from the
	// locked alert row — it must equal the alert's evidence at acceptance time.
	if len(out.Resolution.EvidenceSnapshot) == 0 {
		t.Errorf("expected non-empty evidence snapshot")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && (haystack == needle ||
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()))
}

// TestEvaluate_PeriodicOpensFreshCasePerPeriod proves the period-scoping
// behaviour end-to-end through EvaluateRule: a monthly rule that keeps failing
// across two months opens a *distinct* case per month (not one immortal
// alert), and evaluating April never closes March's still-open case.
func TestEvaluate_PeriodicOpensFreshCasePerPeriod(t *testing.T) {
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(350)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-300)}} // drift 50 → fails
	svc, store := newOrchestrationService(t, l, p)
	rule, err := svc.CreateRule(context.Background(), &CreateRuleRequest{
		Name:         "monthly-recon",
		TemplateKind: models.TemplateLedgerVsPoolDrift,
		TemplateSpec: driftSpec(t, "buildr", `"q"`, "pool", nil),
		Severity:     models.SeverityHigh,
		Cadence:      models.CadenceMonthly,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	ctx := context.Background()

	mar := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	apr := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	if _, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: mar}); err != nil {
		t.Fatalf("eval March: %v", err)
	}
	if _, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: apr}); err != nil {
		t.Fatalf("eval April: %v", err)
	}

	byPeriod := map[string]*models.Alert{}
	for _, a := range store.alerts {
		byPeriod[a.PeriodID] = a
	}
	if len(store.alerts) != 2 {
		t.Fatalf("expected 2 alerts (one per period), got %d", len(store.alerts))
	}
	marAlert, aprAlert := byPeriod["2026-03"], byPeriod["2026-04"]
	if marAlert == nil || aprAlert == nil {
		t.Fatalf("expected a case in both 2026-03 and 2026-04, got periods %v", keysOf(byPeriod))
	}
	if marAlert.ID == aprAlert.ID {
		t.Errorf("expected distinct alert ids per period, both = %s", marAlert.ID)
	}
	if marAlert.Fingerprint != "asset:USD/2" {
		t.Errorf("unexpected fingerprint %q", marAlert.Fingerprint)
	}
	// April's evaluation must not have swept/closed March's open case.
	if marAlert.Status != models.AlertOpen {
		t.Errorf("March case must stay OPEN after April's run, got %v", marAlert.Status)
	}
	if aprAlert.Status != models.AlertOpen {
		t.Errorf("April case should be OPEN, got %v", aprAlert.Status)
	}
}

func keysOf(m map[string]*models.Alert) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

var _ = fmt.Sprintf
