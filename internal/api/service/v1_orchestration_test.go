package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
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

func fpKey(ruleID uuid.UUID, fp string) string { return ruleID.String() + "|" + fp }

func (f *fakeV1Store) Ping() error { return nil }

// Legacy /policies methods — not exercised here.
func (f *fakeV1Store) CreatePolicy(context.Context, *models.Policy) error { return nil }
func (f *fakeV1Store) DeletePolicy(context.Context, uuid.UUID) error      { return nil }
func (f *fakeV1Store) GetPolicy(context.Context, uuid.UUID) (*models.Policy, error) {
	return nil, nil
}
func (f *fakeV1Store) ListPolicies(context.Context, storage.GetPoliciesQuery) (*bunpaginate.Cursor[models.Policy], error) {
	return nil, nil
}
func (f *fakeV1Store) CreateReconciation(context.Context, *models.Reconciliation) error {
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
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.CompiledCEL != nil {
		r.CompiledCEL = *p.CompiledCEL
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
	key := fpKey(in.RuleID, in.Fingerprint)

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
		}
		event := f.recordEvent(alert.ID, models.AlertEventFail, &prev, models.AlertOpen, &in.EvaluationID, in.Evidence, in.OccurredAt)
		copy := *alert
		return &storage.OpenAlertResult{Alert: &copy, Event: event, Created: false, Reopened: reopened}, nil
	}

	fresh := &models.Alert{
		ID:               uuid.New(),
		RuleID:           in.RuleID,
		Fingerprint:      in.Fingerprint,
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

func (f *fakeV1Store) AutoResolveAlert(_ context.Context, ruleID uuid.UUID, fingerprint string, evID uuid.UUID, at time.Time) (*models.Alert, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	id, ok := f.byFP[fpKey(ruleID, fingerprint)]
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
	alert.Status = models.AlertResolved
	alert.Resolution = res
	alert.UpdatedAt = time.Now().UTC()
	payload, _ := json.Marshal(res)
	f.recordEvent(alert.ID, eventType, &prev, models.AlertResolved, nil, payload, res.At)
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

func (f *fakeV1Store) ListAlertEvents(_ context.Context, alertID uuid.UUID) ([]models.AlertEvent, error) {
	out := []models.AlertEvent{}
	for _, e := range f.events {
		if e.AlertID == alertID {
			out = append(out, *e)
		}
	}
	return out, nil
}

func (f *fakeV1Store) ListActiveAlertFingerprints(_ context.Context, ruleID uuid.UUID) ([]string, error) {
	out := []string{}
	for _, alert := range f.alerts {
		if alert.RuleID != ruleID {
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
	id, ok := f.byFP[fpKey(ruleID, fingerprint)]
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
	id, ok := f.byFP[fpKey(ruleID, fingerprint)]
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

func (f *orchestrationLedger) Features(context.Context, string) (engine.LedgerFeatures, error) {
	return engine.LedgerFeatures{AccountMetadataHistory: "SYNC"}, nil
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
}

func (f *orchestrationPayments) PoolBalanceLatest(context.Context, string) (map[string]*big.Int, error) {
	out := map[string]*big.Int{}
	for k, v := range f.current {
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

func driftSpec(t *testing.T, ledger, query, pool string, tol map[string]int64) json.RawMessage {
	t.Helper()
	spec := templates.DriftSpec{
		Ledger:         ledger,
		LedgerQuery:    json.RawMessage(query),
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

func TestCreateRule_PersistsCompiledCEL(t *testing.T) {
	svc, store := newOrchestrationService(t, &orchestrationLedger{current: map[string]*big.Int{}}, &orchestrationPayments{current: map[string]*big.Int{}})
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))
	if rule.CompiledCEL == "" {
		t.Fatalf("expected compiled_cel to be populated")
	}
	saved, _ := store.GetRule(context.Background(), rule.ID)
	if saved.CompiledCEL != rule.CompiledCEL {
		t.Errorf("compiled_cel not persisted")
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

	when := time.Now().Add(48 * time.Hour)
	out, err := svc.AcceptAlert(context.Background(), alert.ID, &AcceptAlertRequest{
		By:        "treasurer",
		Note:      "settlement lag",
		ExpiresAt: &when,
	})
	if err != nil {
		t.Fatalf("AcceptAlert: %v", err)
	}
	if out.Resolution == nil || out.Resolution.Kind != models.ResolutionAcceptedByBusiness {
		t.Fatalf("expected accepted_by_business, got %v", out.Resolution)
	}
	if len(out.Resolution.EvidenceSnapshot) == 0 {
		t.Errorf("expected non-empty evidence snapshot")
	}
	if out.Resolution.ExpiresAt == nil || !out.Resolution.ExpiresAt.Equal(when) {
		t.Errorf("expiresAt not propagated, got %v", out.Resolution.ExpiresAt)
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

var _ = fmt.Sprintf
