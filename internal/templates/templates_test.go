package templates

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// --- helpers -----------------------------------------------------------------

type fakeLedger struct {
	balances map[string]map[string]*big.Int // (ledger|query) → asset → amount
	accounts map[string][]engine.Account    // (ledger|query) → accounts (for per_account)
	// gotPITs records the pit each AggregateBalance call read at, keyed by
	// (ledger|query) so per-source-PIT tests can assert two terms on the same
	// ledger were read at different instants.
	gotPITs map[string]time.Time
}

func (f *fakeLedger) AggregateBalance(_ context.Context, ledger string, query json.RawMessage, pit time.Time) (map[string]*big.Int, error) {
	key := ledger + "|" + string(query)
	if f.gotPITs == nil {
		f.gotPITs = map[string]time.Time{}
	}
	f.gotPITs[key] = pit
	if f.balances == nil {
		return map[string]*big.Int{}, nil
	}
	b, ok := f.balances[key]
	if !ok {
		return map[string]*big.Int{}, nil
	}
	return b, nil
}
func (f *fakeLedger) ListAccounts(_ context.Context, ledger string, query json.RawMessage, _ time.Time, limit int) ([]engine.Account, error) {
	if f.accounts == nil {
		return nil, nil
	}
	accts := f.accounts[ledger+"|"+string(query)]
	if len(accts) > limit {
		return nil, errors.New("listAccounts: exceeded accounts budget")
	}
	return accts, nil
}

type fakePayments struct {
	pools map[string]map[string]*big.Int
	// gotPITs records the pit argument of each PoolBalance call, keyed by poolID
	// (last call wins). A nil entry means the pool was read latest; a non-nil
	// entry means it was read point-in-time at that instant.
	gotPITs map[string]*time.Time
}

func (f *fakePayments) PoolBalance(_ context.Context, id string, pit *time.Time) (map[string]*big.Int, error) {
	if f.gotPITs == nil {
		f.gotPITs = map[string]*time.Time{}
	}
	f.gotPITs[id] = pit
	if f.pools == nil {
		return map[string]*big.Int{}, nil
	}
	b, ok := f.pools[id]
	if !ok {
		return map[string]*big.Int{}, nil
	}
	return b, nil
}

func newTestEngine(t *testing.T, l engine.LedgerResolver, p engine.PaymentsResolver) (*engine.Engine, engine.Resolvers) {
	t.Helper()
	r := engine.Resolvers{Ledger: l, Payments: p}
	eng, err := engine.New(r, engine.DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng, r
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func findOutcome(out []Outcome, fp string) *Outcome {
	for i := range out {
		if out[i].Fingerprint == fp {
			return &out[i]
		}
	}
	return nil
}

// --- registry ---------------------------------------------------------------

func TestDefaultRegistry_ContainsAllV1Templates(t *testing.T) {
	r := DefaultRegistry()
	for _, kind := range []models.TemplateKind{
		models.TemplateLedgerVsPoolDrift,
		models.TemplateLedgerInvariant,
		models.TemplateAccountThreshold,
		models.TemplateSourceParity,
	} {
		if _, err := r.Get(kind); err != nil {
			t.Errorf("registry missing %s: %v", kind, err)
		}
	}
}

func TestRegistry_UnknownKind(t *testing.T) {
	r := DefaultRegistry()
	_, err := r.Get(models.TemplateKind("nope"))
	if !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("expected ErrUnknownTemplate, got %v", err)
	}
}

// --- ledger_vs_pool_drift ---------------------------------------------------

func TestDrift_Validate_RequiredFields(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	cases := map[string]DriftSpec{
		"missing ledger":     {LedgerQuery: json.RawMessage(`{}`), PaymentsPoolID: "p"},
		"missing query":      {Ledger: "l", PaymentsPoolID: "p"},
		"missing pool":       {Ledger: "l", LedgerQuery: json.RawMessage(`{}`)},
		"negative tolerance": {Ledger: "l", LedgerQuery: json.RawMessage(`{}`), PaymentsPoolID: "p", Tolerance: map[string]int64{"USD": -1}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := tmpl.Validate(mustJSON(t, spec)); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

// TestDrift_Evaluate_NilResolver — regression for the panic where the
// template called resolvers.Ledger.AggregateBalance on a nil interface value.
// Must return a clear ErrResolverUnavailable error instead.
func TestDrift_Evaluate_NilResolver(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	// Engine still needs *something* for both, so use a payments-only
	// resolver setup with a nil ledger to trigger the guard.
	eng, _ := newTestEngine(t, &fakeLedger{}, &fakePayments{})
	resolvers := engine.Resolvers{Ledger: nil, Payments: &fakePayments{}}
	spec := mustJSON(t, DriftSpec{
		Ledger:         "buildr",
		LedgerQuery:    json.RawMessage(`{}`),
		PaymentsPoolID: "pool_xyz",
	})
	_, err := tmpl.Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{PIT: time.Now()})
	if !errors.Is(err, ErrResolverUnavailable) {
		t.Fatalf("expected ErrResolverUnavailable, got %v", err)
	}
}

func TestDrift_Evaluate_AllZero_AllPass(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`buildr|{"$match":{"metadata[trust]":"true"}}`: {
			"USD/2": big.NewInt(350),
			"EUR/2": big.NewInt(100),
		},
	}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{
		"pool_xyz": {
			"USD/2": big.NewInt(-350),
			"EUR/2": big.NewInt(-100),
		},
	}}
	eng, res := newTestEngine(t, l, p)
	spec := mustJSON(t, DriftSpec{
		Ledger:         "buildr",
		LedgerQuery:    json.RawMessage(`{"$match":{"metadata[trust]":"true"}}`),
		PaymentsPoolID: "pool_xyz",
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 outcomes (USD/2, EUR/2), got %d", len(out))
	}
	for _, o := range out {
		if !o.Passed {
			t.Errorf("expected pass on %s, got fail (evidence=%v)", o.Fingerprint, o.Evidence)
		}
	}
}

func TestDrift_Evaluate_OneAssetDrifts(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`b|"q"`: {"USD/2": big.NewInt(350), "EUR/2": big.NewInt(100)},
	}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{
		"pool": {"USD/2": big.NewInt(-350), "EUR/2": big.NewInt(-50)}, // EUR drift 50
	}}
	eng, res := newTestEngine(t, l, p)
	spec := mustJSON(t, DriftSpec{Ledger: "b", LedgerQuery: json.RawMessage(`"q"`), PaymentsPoolID: "pool"})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	usd := findOutcome(out, "asset:USD/2")
	eur := findOutcome(out, "asset:EUR/2")
	if usd == nil || eur == nil {
		t.Fatalf("missing outcomes: %v", out)
	}
	if !usd.Passed {
		t.Errorf("USD/2 expected pass")
	}
	if eur.Passed {
		t.Errorf("EUR/2 expected fail (drift 50, tolerance 0)")
	}
	if got := eur.Evidence["drift"]; got != "50" {
		t.Errorf("EUR/2 drift evidence = %v, want %q", got, "50")
	}
}

// TestDrift_Evaluate_LedgerSignNegative — `ledgerSign: -1` flips the ledger
// term so two positively-signed sides (held=+350 on ledger, +350 on pool)
// reconcile to zero. Without the sign, the legacy formulation would treat
// this as a 700-unit drift.
func TestDrift_Evaluate_LedgerSignNegative(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`b|"q"`: {"USD/2": big.NewInt(350)},
	}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{
		"pool": {"USD/2": big.NewInt(350)}, // BOTH positive
	}}
	eng, res := newTestEngine(t, l, p)
	spec := mustJSON(t, DriftSpec{
		Ledger:         "b",
		LedgerQuery:    json.RawMessage(`"q"`),
		PaymentsPoolID: "pool",
		LedgerSign:     -1,
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	usd := findOutcome(out, "asset:USD/2")
	if usd == nil {
		t.Fatalf("missing USD/2 outcome: %v", out)
	}
	if !usd.Passed {
		t.Errorf("expected pass with ledgerSign=-1 and equal positive balances, got fail; evidence=%v", usd.Evidence)
	}
	if got := usd.Evidence["ledgerSign"]; got != -1 {
		t.Errorf("evidence.ledgerSign = %v, want -1", got)
	}
	if got := usd.Evidence["ledgerBalanceRaw"]; got != "350" {
		t.Errorf("evidence.ledgerBalanceRaw = %v, want %q (raw, pre-sign)", got, "350")
	}
	if got := usd.Evidence["ledgerBalance"]; got != "-350" {
		t.Errorf("evidence.ledgerBalance = %v, want %q (signed contribution)", got, "-350")
	}
}

// TestDrift_Evaluate_LedgerSignDefault — omitting ledgerSign defaults to +1,
// preserving legacy semantics: positive ledger + positive pool = drift.
func TestDrift_Evaluate_LedgerSignDefault(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`b|"q"`: {"USD/2": big.NewInt(350)},
	}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{
		"pool": {"USD/2": big.NewInt(350)},
	}}
	eng, res := newTestEngine(t, l, p)
	// No ledgerSign set → defaults to +1 → 350 + 350 = 700 drift
	spec := mustJSON(t, DriftSpec{Ledger: "b", LedgerQuery: json.RawMessage(`"q"`), PaymentsPoolID: "pool"})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	usd := findOutcome(out, "asset:USD/2")
	if usd == nil || usd.Passed {
		t.Fatalf("expected FAIL with default ledgerSign and matching positives, got %+v", usd)
	}
	if got := usd.Evidence["ledgerSign"]; got != 1 {
		t.Errorf("evidence.ledgerSign = %v, want 1 (default)", got)
	}
}

// TestDrift_Validate_LedgerSignBounds — only +1 / -1 / unset are accepted.
func TestDrift_Validate_LedgerSignBounds(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	for _, ok := range []int{0, 1, -1} {
		spec := mustJSON(t, DriftSpec{
			Ledger: "b", LedgerQuery: json.RawMessage(`"q"`), PaymentsPoolID: "pool",
			LedgerSign: ok,
		})
		if err := tmpl.Validate(spec); err != nil {
			t.Errorf("Validate(ledgerSign=%d) unexpectedly errored: %v", ok, err)
		}
	}
	for _, bad := range []int{2, -2, 100} {
		spec := mustJSON(t, DriftSpec{
			Ledger: "b", LedgerQuery: json.RawMessage(`"q"`), PaymentsPoolID: "pool",
			LedgerSign: bad,
		})
		if err := tmpl.Validate(spec); err == nil {
			t.Errorf("Validate(ledgerSign=%d) accepted, want rejection", bad)
		}
	}
}

func TestDrift_Evaluate_ToleranceAbsorbs(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`b|"q"`: {"USD/2": big.NewInt(100)},
	}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{
		"pool": {"USD/2": big.NewInt(-95)}, // drift 5
	}}
	eng, res := newTestEngine(t, l, p)
	spec := mustJSON(t, DriftSpec{
		Ledger:         "b",
		LedgerQuery:    json.RawMessage(`"q"`),
		PaymentsPoolID: "pool",
		Tolerance:      map[string]int64{"USD/2": 10},
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || !o.Passed {
		t.Fatalf("expected pass within tolerance, got %v", o)
	}
}

func TestDrift_Explain_HasShape(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	spec := mustJSON(t, DriftSpec{
		Ledger: "buildr", LedgerQuery: json.RawMessage(`{}`), PaymentsPoolID: "p1",
		Tolerance: map[string]int64{"USD/2": 50},
	})
	explained, err := tmpl.Explain(spec)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	for _, want := range []string{"ledgerSet", "pool", "abs(", "<asset>"} {
		if !strings.Contains(explained, want) {
			t.Errorf("explanation missing %q: %s", want, explained)
		}
	}
	// The representative CEL must parse — CreateRule runs exactly this compile
	// check at the API boundary, so an unparseable Explain (e.g. a stray block
	// comment in the tolerance literal) rejects every rule create with a 400.
	eng, _ := newTestEngine(t, &fakeLedger{}, &fakePayments{})
	if _, err := eng.Compile(explained); err != nil {
		t.Errorf("representative CEL did not compile: %v\n%s", err, explained)
	}
}

// --- ledger_invariant -------------------------------------------------------

func TestInvariant_Validate(t *testing.T) {
	tmpl := NewLedgerInvariant()
	bad := []InvariantSpec{
		{Terms: nil, Tolerance: map[string]int64{"USD/2": 0}},
		{Terms: []InvariantTerm{{Ledger: "l", Query: json.RawMessage(`"q"`), Sign: 1}}, Tolerance: nil},
		{Terms: []InvariantTerm{{Ledger: "l", Query: json.RawMessage(`"q"`), Sign: 2}}, Tolerance: map[string]int64{"USD/2": 0}},
		{Terms: []InvariantTerm{{Sign: 1, Query: json.RawMessage(`"q"`)}}, Tolerance: map[string]int64{"USD/2": 0}},
	}
	for i, s := range bad {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			if err := tmpl.Validate(mustJSON(t, s)); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

func TestInvariant_Evaluate_SumsToZero(t *testing.T) {
	tmpl := NewLedgerInvariant()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`buildr|"held"`:       {"USD/2": big.NewInt(350)},
		`buildr|"obligation"`: {"USD/2": big.NewInt(-350)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	spec := mustJSON(t, InvariantSpec{
		Terms: []InvariantTerm{
			{Ledger: "buildr", Query: json.RawMessage(`"held"`), Sign: 1},
			{Ledger: "buildr", Query: json.RawMessage(`"obligation"`), Sign: 1},
		},
		Tolerance: map[string]int64{"USD/2": 0},
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || !o.Passed {
		t.Fatalf("expected pass, got %v", o)
	}
}

func TestInvariant_Evaluate_NegativeSign(t *testing.T) {
	tmpl := NewLedgerInvariant()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`buildr|"held"`:    {"USD/2": big.NewInt(350)},
		`buildr|"outflow"`: {"USD/2": big.NewInt(350)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	// held +1 + outflow -1 = 0 → pass
	spec := mustJSON(t, InvariantSpec{
		Terms: []InvariantTerm{
			{Ledger: "buildr", Query: json.RawMessage(`"held"`), Sign: 1},
			{Ledger: "buildr", Query: json.RawMessage(`"outflow"`), Sign: -1},
		},
		Tolerance: map[string]int64{"USD/2": 0},
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || !o.Passed {
		t.Fatalf("expected pass with opposing signs, got %v", o)
	}
}

func TestInvariant_Evaluate_ExceedsTolerance_Fails(t *testing.T) {
	tmpl := NewLedgerInvariant()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`l|"a"`: {"USD/2": big.NewInt(100)},
		`l|"b"`: {"USD/2": big.NewInt(50)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	spec := mustJSON(t, InvariantSpec{
		Terms: []InvariantTerm{
			{Ledger: "l", Query: json.RawMessage(`"a"`), Sign: 1},
			{Ledger: "l", Query: json.RawMessage(`"b"`), Sign: 1},
		},
		Tolerance: map[string]int64{"USD/2": 100}, // sum = 150, tolerance = 100 → fail
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || o.Passed {
		t.Fatalf("expected fail (sum 150 > tolerance 100), got %v", o)
	}
}

// --- account_threshold ------------------------------------------------------

// per_account is now a shipped mode — Validate must accept it.
func TestThreshold_Validate_PerAccount_Accepted(t *testing.T) {
	tmpl := NewAccountThreshold()
	one := int64(1)
	spec := mustJSON(t, ThresholdSpec{
		Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdPerAccount,
		Bounds: map[string]ThresholdBounds{"USD/2": {Min: &one}},
	})
	if err := tmpl.Validate(spec); err != nil {
		t.Fatalf("expected per_account to validate, got %v", err)
	}
}

func TestThreshold_Validate_BoundsRequired(t *testing.T) {
	tmpl := NewAccountThreshold()
	cases := []ThresholdSpec{
		{Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdAggregate, Bounds: map[string]ThresholdBounds{}},
		{Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdAggregate, Bounds: map[string]ThresholdBounds{"USD/2": {}}},
	}
	for i, s := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			if err := tmpl.Validate(mustJSON(t, s)); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

func TestThreshold_Validate_MinGreaterThanMax(t *testing.T) {
	tmpl := NewAccountThreshold()
	lo, hi := int64(100), int64(50)
	spec := mustJSON(t, ThresholdSpec{
		Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdAggregate,
		Bounds: map[string]ThresholdBounds{"USD/2": {Min: &lo, Max: &hi}},
	})
	if err := tmpl.Validate(spec); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec, got %v", err)
	}
}

func TestThreshold_Evaluate_InBounds(t *testing.T) {
	tmpl := NewAccountThreshold()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`l|"q"`: {"USD/2": big.NewInt(500)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	lo, hi := int64(100), int64(1000)
	spec := mustJSON(t, ThresholdSpec{
		Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdAggregate,
		Bounds: map[string]ThresholdBounds{"USD/2": {Min: &lo, Max: &hi}},
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || !o.Passed {
		t.Fatalf("expected pass (500 in [100,1000]), got %v", o)
	}
}

func TestThreshold_Evaluate_BelowMin(t *testing.T) {
	tmpl := NewAccountThreshold()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`l|"q"`: {"USD/2": big.NewInt(50)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	lo := int64(100)
	spec := mustJSON(t, ThresholdSpec{
		Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdAggregate,
		Bounds: map[string]ThresholdBounds{"USD/2": {Min: &lo}},
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || o.Passed {
		t.Fatalf("expected fail (50 < 100), got %v", o)
	}
}

func TestThreshold_Evaluate_AboveMax(t *testing.T) {
	tmpl := NewAccountThreshold()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`l|"q"`: {"USD/2": big.NewInt(5000)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	hi := int64(1000)
	spec := mustJSON(t, ThresholdSpec{
		Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdAggregate,
		Bounds: map[string]ThresholdBounds{"USD/2": {Max: &hi}},
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || o.Passed {
		t.Fatalf("expected fail (5000 > 1000), got %v", o)
	}
}

func TestThreshold_DeterministicFingerprintOrder(t *testing.T) {
	// Outcomes must be emitted in lex-sorted asset order regardless of how the
	// spec's bounds map iterates. This stabilises fingerprint order downstream.
	tmpl := NewAccountThreshold()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`l|"q"`: {"USD/2": big.NewInt(100), "EUR/2": big.NewInt(100), "GBP/2": big.NewInt(100)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	hi := int64(1000)
	spec := mustJSON(t, ThresholdSpec{
		Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdAggregate,
		Bounds: map[string]ThresholdBounds{
			"USD/2": {Max: &hi},
			"GBP/2": {Max: &hi},
			"EUR/2": {Max: &hi},
		},
	})
	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	got := []string{out[0].Fingerprint, out[1].Fingerprint, out[2].Fingerprint}
	want := []string{"asset:EUR/2", "asset:GBP/2", "asset:USD/2"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("outcome[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}
