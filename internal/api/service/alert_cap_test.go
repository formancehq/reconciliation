package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/contractversion"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/templates"
)

// capLedger holds one balance per asset, so a wildcard rule fans out into as
// many outcomes as there are bounded assets.
type capLedger struct{ balances map[string]*big.Int }

func (l *capLedger) AggregateBalance(context.Context, string, json.RawMessage) (map[string]*big.Int, error) {
	if l.balances == nil {
		return map[string]*big.Int{}, nil
	}
	return l.balances, nil
}

func (l *capLedger) ListAccounts(context.Context, string, json.RawMessage, int) ([]engine.Account, error) {
	return nil, nil
}

// capAssets builds n assets, the first `failing` of which sit below the rule's
// floor; the rest hold enough to pass.
func capAssets(n, failing int) (map[string]*big.Int, map[string]templates.BalanceBound) {
	balances := make(map[string]*big.Int, n)
	bounds := make(map[string]templates.BalanceBound, n)
	for i := range n {
		asset := fmt.Sprintf("A%d/2", i)
		amount := big.NewInt(500)
		if i < failing {
			amount = big.NewInt(0)
		}
		balances[asset] = amount
		bounds[asset] = templates.BalanceBound{Min: "100"}
	}
	return balances, bounds
}

func newCapService(t *testing.T, ledger *capLedger, maxNew int) (*Service, *fakeV1Store) {
	t.Helper()
	store := newFakeV1Store()
	res := engine.Resolvers{Ledger: ledger}
	eng, err := engine.New(res, engine.DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return NewService(store, eng, templates.DefaultRegistry(), res, WithMaxNewAlertsPerEvaluation(maxNew)), store
}

// capRule creates a wildcard bounds rule: every bounded asset below the floor
// becomes its own failing outcome, which is the fan-out the cap guards.
func capRule(t *testing.T, svc *Service, bounds map[string]templates.BalanceBound) *models.Rule {
	t.Helper()
	spec, err := json.Marshal(templates.BalanceBoundsSpec{
		Source: templates.V2NamedSource{
			ID: "book", Ledger: "l",
			Query: json.RawMessage(`{"$match":{"address":"acct:*"}}`),
			Asset: templates.AssetWildcard,
		},
		Bounds: bounds,
	})
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	rule, err := svc.CreateRule(contractversion.WithContext(context.Background(), models.ContractVersionV2),
		&CreateRuleRequest{
			Name:         "fan-out rule",
			TemplateKind: models.TemplateBalanceBounds,
			TemplateSpec: spec,
			Severity:     models.SeverityMedium,
		})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	return rule
}

func alertsByFingerprint(store *fakeV1Store) map[string]*models.Alert {
	out := map[string]*models.Alert{}
	for _, alert := range store.alerts {
		out[alert.Fingerprint] = alert
	}
	return out
}

// Over the cap, the whole plan is withheld: no per-account alert is opened, and
// one meta-alert says why.
func TestEvaluate_NewAlertCap_WithholdsEverything(t *testing.T) {
	t.Parallel()

	balances, bounds := capAssets(5, 5)
	svc, store := newCapService(t, &capLedger{balances: balances}, 2)
	rule := capRule(t, svc, bounds)

	evaluation, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{})
	if err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}

	// The evaluation still reports what it found, and the capture still records it.
	if evaluation.Result != models.EvaluationFail {
		t.Errorf("result = %v, want FAIL — the rule did fail, only its alerting was withheld", evaluation.Result)
	}
	if len(store.captures) != 1 {
		t.Errorf("expected the capture to be recorded regardless, got %d", len(store.captures))
	}

	byFP := alertsByFingerprint(store)
	if len(byFP) != 1 {
		t.Fatalf("expected only the meta-alert, got %d: %v", len(byFP), byFP)
	}
	meta, ok := byFP[alertCapFingerprint]
	if !ok {
		t.Fatalf("expected an %s alert, got %v", alertCapFingerprint, byFP)
	}
	if meta.Severity != models.SeverityHigh {
		t.Errorf("severity = %v, want high", meta.Severity)
	}
	if meta.Labels["kind"] != alertCapFingerprint {
		t.Errorf("labels[kind] = %q, want %q", meta.Labels["kind"], alertCapFingerprint)
	}

	var evidence map[string]any
	if err := json.Unmarshal(meta.Evidence, &evidence); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	if evidence["newAlerts"] != float64(5) || evidence["maxNewAlerts"] != float64(2) {
		t.Errorf("evidence should name the count and the cap: %+v", evidence)
	}
	if sample, ok := evidence["fingerprintSample"].([]any); !ok || len(sample) == 0 {
		t.Errorf("evidence should carry a fingerprint sample: %+v", evidence["fingerprintSample"])
	}
}

// Under the cap nothing changes.
func TestEvaluate_NewAlertCap_UnderTheCapIsUnaffected(t *testing.T) {
	t.Parallel()

	balances, bounds := capAssets(5, 5)
	svc, store := newCapService(t, &capLedger{balances: balances}, 10)
	rule := capRule(t, svc, bounds)

	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{}); err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}

	byFP := alertsByFingerprint(store)
	if len(byFP) != 5 {
		t.Fatalf("expected one alert per failing account, got %d", len(byFP))
	}
	if _, capped := byFP[alertCapFingerprint]; capped {
		t.Error("no meta-alert should be raised under the cap")
	}
}

// The cap counts alerts an evaluation would OPEN, not ones it updates: a rule
// steadily failing on a set that is already alerted must keep working, or its
// open alerts would be stranded and could never resolve.
func TestEvaluate_NewAlertCap_UpdatesDoNotCount(t *testing.T) {
	t.Parallel()

	balances, bounds := capAssets(5, 5)
	ledger := &capLedger{balances: balances}
	svc, store := newCapService(t, ledger, 10)
	rule := capRule(t, svc, bounds)

	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{}); err != nil {
		t.Fatalf("first EvaluateRule: %v", err)
	}
	if got := len(alertsByFingerprint(store)); got != 5 {
		t.Fatalf("setup: expected 5 alerts, got %d", got)
	}

	// Same five failures, now below the cap — but they are all updates.
	svc.maxNewAlerts = 2
	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{}); err != nil {
		t.Fatalf("second EvaluateRule: %v", err)
	}

	byFP := alertsByFingerprint(store)
	if _, capped := byFP[alertCapFingerprint]; capped {
		t.Error("updating already-open alerts must not trip the new-alert cap")
	}
	if len(byFP) != 5 {
		t.Errorf("expected the same 5 alerts, got %d", len(byFP))
	}
}

// Withholding must not resolve anything either — that is the whole reason it is
// all-or-nothing rather than a truncation.
func TestEvaluate_NewAlertCap_WithholdingResolvesNothing(t *testing.T) {
	t.Parallel()

	balances, bounds := capAssets(3, 3)
	ledger := &capLedger{balances: balances}
	svc, store := newCapService(t, ledger, 10)
	rule := capRule(t, svc, bounds)

	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{}); err != nil {
		t.Fatalf("first EvaluateRule: %v", err)
	}
	opened := alertsByFingerprint(store)
	if len(opened) != 3 {
		t.Fatalf("setup: expected 3 alerts, got %d", len(opened))
	}

	// The original three now pass (they would auto-resolve), but a large new set
	// fails — enough to trip the cap.
	healthy, _ := capAssets(3, 0)
	extra, extraBounds := capAssets(9, 9)
	for asset, amount := range healthy {
		extra[asset] = amount // the original three now sit above their floor
	}
	ledger.balances = extra
	rule = capRule(t, svc, extraBounds)
	svc.maxNewAlerts = 2
	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{}); err != nil {
		t.Fatalf("second EvaluateRule: %v", err)
	}

	byFP := alertsByFingerprint(store)
	for fingerprint, alert := range opened {
		current, ok := byFP[fingerprint]
		if !ok {
			t.Fatalf("alert %s disappeared", fingerprint)
		}
		if current.Status != alert.Status {
			t.Errorf("alert %s changed status to %v while transitions were withheld", fingerprint, current.Status)
		}
	}
	for fingerprint := range byFP {
		if _, existed := opened[fingerprint]; !existed && fingerprint != alertCapFingerprint {
			t.Errorf("no new alert should have opened, got %s", fingerprint)
		}
	}
}

// A zero cap restores the unbounded behaviour.
func TestEvaluate_NewAlertCap_ZeroDisables(t *testing.T) {
	t.Parallel()

	balances, bounds := capAssets(6, 6)
	svc, store := newCapService(t, &capLedger{balances: balances}, 0)
	rule := capRule(t, svc, bounds)

	if _, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{}); err != nil {
		t.Fatalf("EvaluateRule: %v", err)
	}
	if got := len(alertsByFingerprint(store)); got != 6 {
		t.Errorf("expected all 6 alerts with the cap disabled, got %d", got)
	}
}

// The default is applied when no option is passed.
func TestNewService_AppliesTheDefaultCap(t *testing.T) {
	t.Parallel()

	svc := NewService(newFakeV1Store(), nil, nil, engine.Resolvers{})
	if svc.maxNewAlerts != DefaultMaxNewAlertsPerEvaluation {
		t.Errorf("maxNewAlerts = %d, want the default %d", svc.maxNewAlerts, DefaultMaxNewAlertsPerEvaluation)
	}
}

// A rule whose template kind this binary does not know must surface the same way
// as any other engine failure. It used to return before persisting anything, so
// a scheduled rule failed once a tick with nothing on any channel — the exact
// hazard that makes retiring a template kind dangerous.
func TestEvaluate_UnknownTemplateKind_IsAnEngineError(t *testing.T) {
	t.Parallel()

	_, bounds := capAssets(1, 1)
	svc, store := newCapService(t, &capLedger{}, 0)
	rule := capRule(t, svc, bounds)

	// Simulate the kind having been retired (or the binary rolled back) while a
	// rule still references it.
	persisted := store.rules[rule.ID]
	persisted.TemplateKind = models.TemplateKind("account_threshold_v0")

	evaluation, err := svc.EvaluateRule(context.Background(), rule.ID, EvaluateRuleRequest{})
	if err != nil {
		t.Fatalf("an unknown kind must not fail the call outright: %v", err)
	}
	if evaluation.Result != models.EvaluationError {
		t.Errorf("result = %v, want ERROR", evaluation.Result)
	}
	if !strings.Contains(evaluation.Error, "not available in this build") {
		t.Errorf("the error should name the cause, got %q", evaluation.Error)
	}
	if len(store.captures) != 1 {
		t.Errorf("the run must still be captured, got %d captures", len(store.captures))
	}
	byFP := alertsByFingerprint(store)
	if _, ok := byFP[engineErrorFingerprint]; !ok {
		t.Errorf("expected an %s meta-alert, got %v", engineErrorFingerprint, byFP)
	}
}
