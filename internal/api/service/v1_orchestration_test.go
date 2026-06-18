package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
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
//   - OpenOrUpdateIncident dedup (one active per (rule, fingerprint))
//   - AutoResolveIncident (closes active with kind=auto)
//   - parent_incident_id chain on re-open
type fakeV1Store struct {
	rules       map[uuid.UUID]*models.Rule
	evaluations map[uuid.UUID]*models.Evaluation
	incidents   []*models.Incident // append-only chronological list
}

func newFakeV1Store() *fakeV1Store {
	return &fakeV1Store{
		rules:       map[uuid.UUID]*models.Rule{},
		evaluations: map[uuid.UUID]*models.Evaluation{},
	}
}

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

// Incident — the load-bearing part of the orchestration tests.
func (f *fakeV1Store) OpenOrUpdateIncident(_ context.Context, in storage.OpenIncidentInput) (*storage.OpenIncidentResult, error) {
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now().UTC()
	}
	// Look up active incident for the same fingerprint.
	for _, inc := range f.incidents {
		if inc.RuleID == in.RuleID && inc.Fingerprint == in.Fingerprint &&
			(inc.Status == models.IncidentOpen || inc.Status == models.IncidentAcknowledged) {
			inc.LastSeenAt = in.OccurredAt
			inc.LastEvaluationID = in.EvaluationID
			inc.OccurrenceCount++
			inc.Evidence = in.Evidence
			inc.UpdatedAt = time.Now().UTC()
			copy := *inc
			return &storage.OpenIncidentResult{Incident: &copy, Created: false}, nil
		}
	}
	// No active — look for a parent (most recent RESOLVED for same fingerprint).
	var parentID *uuid.UUID
	for i := len(f.incidents) - 1; i >= 0; i-- {
		inc := f.incidents[i]
		if inc.RuleID == in.RuleID && inc.Fingerprint == in.Fingerprint && inc.Status == models.IncidentResolved {
			p := inc.ID
			parentID = &p
			break
		}
	}
	fresh := &models.Incident{
		ID:                uuid.New(),
		RuleID:            in.RuleID,
		Fingerprint:       in.Fingerprint,
		Status:            models.IncidentOpen,
		Severity:          in.Severity,
		OpenedAt:          in.OccurredAt,
		LastSeenAt:        in.OccurredAt,
		OccurrenceCount:   1,
		FirstEvaluationID: in.EvaluationID,
		LastEvaluationID:  in.EvaluationID,
		Evidence:          in.Evidence,
		ParentIncidentID:  parentID,
		Labels:            in.Labels,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	f.incidents = append(f.incidents, fresh)
	copy := *fresh
	return &storage.OpenIncidentResult{Incident: &copy, Created: true}, nil
}

func (f *fakeV1Store) AutoResolveIncident(_ context.Context, ruleID uuid.UUID, fingerprint string, evID uuid.UUID, at time.Time) (*models.Incident, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	for _, inc := range f.incidents {
		if inc.RuleID == ruleID && inc.Fingerprint == fingerprint &&
			(inc.Status == models.IncidentOpen || inc.Status == models.IncidentAcknowledged) {
			inc.Status = models.IncidentResolved
			inc.LastEvaluationID = evID
			inc.Resolution = &models.Resolution{Kind: models.ResolutionAuto, By: "system", At: at}
			inc.UpdatedAt = time.Now().UTC()
			copy := *inc
			return &copy, nil
		}
	}
	return nil, nil
}

func (f *fakeV1Store) AckIncident(_ context.Context, id uuid.UUID, ack *models.Ack) (*models.Incident, error) {
	for _, inc := range f.incidents {
		if inc.ID == id && (inc.Status == models.IncidentOpen || inc.Status == models.IncidentAcknowledged) {
			inc.Status = models.IncidentAcknowledged
			inc.Ack = ack
			inc.UpdatedAt = time.Now().UTC()
			copy := *inc
			return &copy, nil
		}
	}
	return nil, storage.ErrNotFound
}

func (f *fakeV1Store) ResolveIncidentManual(_ context.Context, id uuid.UUID, res *models.Resolution) (*models.Incident, error) {
	for _, inc := range f.incidents {
		if inc.ID == id && (inc.Status == models.IncidentOpen || inc.Status == models.IncidentAcknowledged) {
			inc.Status = models.IncidentResolved
			inc.Resolution = res
			inc.UpdatedAt = time.Now().UTC()
			copy := *inc
			return &copy, nil
		}
	}
	return nil, storage.ErrNotFound
}

func (f *fakeV1Store) AcceptIncident(_ context.Context, id uuid.UUID, res *models.Resolution) (*models.Incident, error) {
	for _, inc := range f.incidents {
		if inc.ID == id && (inc.Status == models.IncidentOpen || inc.Status == models.IncidentAcknowledged) {
			inc.Status = models.IncidentResolved
			inc.Resolution = res
			inc.UpdatedAt = time.Now().UTC()
			copy := *inc
			return &copy, nil
		}
	}
	return nil, storage.ErrNotFound
}

func (f *fakeV1Store) GetIncident(_ context.Context, id uuid.UUID) (*models.Incident, error) {
	for _, inc := range f.incidents {
		if inc.ID == id {
			copy := *inc
			return &copy, nil
		}
	}
	return nil, storage.ErrNotFound
}
func (f *fakeV1Store) ListIncidents(context.Context, storage.GetIncidentsQuery) (*bunpaginate.Cursor[models.Incident], error) {
	return nil, nil
}

// activeFor returns the one active incident for the given (rule, fingerprint),
// or nil. Helper for assertions.
func (f *fakeV1Store) activeFor(ruleID uuid.UUID, fingerprint string) *models.Incident {
	for _, inc := range f.incidents {
		if inc.RuleID == ruleID && inc.Fingerprint == fingerprint &&
			(inc.Status == models.IncidentOpen || inc.Status == models.IncidentAcknowledged) {
			return inc
		}
	}
	return nil
}

// allFor returns every incident for the given (rule, fingerprint) in
// chronological order. Helper for assertions about re-open chains.
func (f *fakeV1Store) allFor(ruleID uuid.UUID, fingerprint string) []*models.Incident {
	out := []*models.Incident{}
	for _, inc := range f.incidents {
		if inc.RuleID == ruleID && inc.Fingerprint == fingerprint {
			out = append(out, inc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OpenedAt.Before(out[j].OpenedAt) })
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

func TestEvaluate_PassNoIncidents(t *testing.T) {
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
	if len(store.incidents) != 0 {
		t.Errorf("expected no incidents, got %d", len(store.incidents))
	}
}

func TestEvaluate_OpenAndUpdateAndAutoResolveAndReopen(t *testing.T) {
	// The headline orchestration test: through one rule we exercise open →
	// update → auto-resolve → reopen-with-parent in sequence.
	l := &orchestrationLedger{current: map[string]*big.Int{"USD/2": big.NewInt(350)}}
	p := &orchestrationPayments{current: map[string]*big.Int{"USD/2": big.NewInt(-300)}} // drift 50
	svc, store := newOrchestrationService(t, l, p)
	rule := mustCreateRule(t, svc, driftSpec(t, "buildr", `"q"`, "pool", nil))

	// 1. First failing eval → opens incident.
	ev1, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err != nil {
		t.Fatalf("eval1: %v", err)
	}
	if ev1.Result != models.EvaluationFail {
		t.Fatalf("eval1 expected FAIL, got %v", ev1.Result)
	}
	active := store.activeFor(rule.ID, "asset:USD/2")
	if active == nil {
		t.Fatalf("expected active incident after first fail")
	}
	if active.OccurrenceCount != 1 {
		t.Errorf("occurrenceCount = %d, want 1", active.OccurrenceCount)
	}
	firstID := active.ID

	// 2. Second failing eval (same fingerprint) → updates in place.
	_, err = svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err != nil {
		t.Fatalf("eval2: %v", err)
	}
	active = store.activeFor(rule.ID, "asset:USD/2")
	if active == nil || active.ID != firstID {
		t.Fatalf("expected same incident updated, got %v", active)
	}
	if active.OccurrenceCount != 2 {
		t.Errorf("occurrenceCount = %d, want 2", active.OccurrenceCount)
	}

	// 3. Make the world consistent → next eval passes, auto-resolves.
	p.current["USD/2"] = big.NewInt(-350)
	_, err = svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err != nil {
		t.Fatalf("eval3: %v", err)
	}
	if active := store.activeFor(rule.ID, "asset:USD/2"); active != nil {
		t.Fatalf("expected no active incident after pass, got %v", active)
	}
	all := store.allFor(rule.ID, "asset:USD/2")
	if len(all) != 1 || all[0].Status != models.IncidentResolved {
		t.Fatalf("expected 1 RESOLVED incident, got %d (%v)", len(all), all)
	}
	if all[0].Resolution == nil || all[0].Resolution.Kind != models.ResolutionAuto {
		t.Errorf("expected resolution.kind=auto, got %v", all[0].Resolution)
	}

	// 4. Break it again → new incident opens, parent_incident_id = the resolved one.
	p.current["USD/2"] = big.NewInt(-200)
	_, err = svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
	if err != nil {
		t.Fatalf("eval4: %v", err)
	}
	all = store.allFor(rule.ID, "asset:USD/2")
	if len(all) != 2 {
		t.Fatalf("expected 2 incidents (resolved + new), got %d", len(all))
	}
	reopen := all[1]
	if reopen.Status != models.IncidentOpen {
		t.Errorf("re-opened status = %v, want OPEN", reopen.Status)
	}
	if reopen.ParentIncidentID == nil || *reopen.ParentIncidentID != firstID {
		t.Errorf("re-opened parent_incident_id = %v, want %v", reopen.ParentIncidentID, firstID)
	}
}

func TestEvaluate_EngineError_RaisesMetaIncident(t *testing.T) {
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

	if len(store.incidents) != 1 {
		t.Fatalf("expected 1 incident (the meta), got %d", len(store.incidents))
	}
	meta := store.incidents[0]
	if !isEngineErrorIncident(meta) {
		t.Errorf("expected engine.error fingerprint, got %q", meta.Fingerprint)
	}
	if meta.Labels["kind"] != engineErrorFingerprint {
		t.Errorf("expected labels.kind = engine.error, got %v", meta.Labels)
	}
	if meta.Severity != models.SeverityHigh {
		t.Errorf("expected meta-incident severity=high, got %v", meta.Severity)
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
	inc := store.activeFor(rule.ID, "asset:USD/2")
	if inc == nil {
		t.Fatalf("expected open incident to test transitions")
	}

	// Ack
	acked, err := svc.AckIncident(context.Background(), inc.ID, &AckIncidentRequest{By: "ops@buildr.com", Note: "investigating"})
	if err != nil {
		t.Fatalf("AckIncident: %v", err)
	}
	if acked.Status != models.IncidentAcknowledged {
		t.Errorf("expected ACKNOWLEDGED, got %v", acked.Status)
	}
	if acked.Ack == nil || acked.Ack.By != "ops@buildr.com" {
		t.Errorf("expected ack.by populated, got %v", acked.Ack)
	}

	// Resolve fixed_by_booking
	resolved, err := svc.ResolveIncident(context.Background(), inc.ID, &ResolveIncidentRequest{
		By:              "ops@buildr.com",
		Note:            "posted correction tx_abc",
		TransactionRefs: []string{"tx_abc"},
	})
	if err != nil {
		t.Fatalf("ResolveIncident: %v", err)
	}
	if resolved.Status != models.IncidentResolved {
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
	inc := store.activeFor(rule.ID, "asset:USD/2")

	_, err := svc.AcceptIncident(context.Background(), inc.ID, &AcceptIncidentRequest{By: "treasurer", Note: ""})
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
	inc := store.activeFor(rule.ID, "asset:USD/2")

	when := time.Now().Add(48 * time.Hour)
	out, err := svc.AcceptIncident(context.Background(), inc.ID, &AcceptIncidentRequest{
		By:        "treasurer",
		Note:      "settlement lag",
		ExpiresAt: &when,
	})
	if err != nil {
		t.Fatalf("AcceptIncident: %v", err)
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
		// crude substring match without importing strings — keeps imports minimal
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()))
}

// silence unused-import flagger if the package gets refactored
var _ = fmt.Sprintf
