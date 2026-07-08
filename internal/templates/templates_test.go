package templates

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// --- helpers -----------------------------------------------------------------

type fakeLedger struct {
	balances map[string]map[string]*big.Int // (ledger|query) → asset → amount
	accounts map[string][]engine.Account    // (ledger|query) → accounts (for per_account)
}

func (f *fakeLedger) AggregateBalance(_ context.Context, ledger string, query json.RawMessage, _ uint64) (map[string]*big.Int, error) {
	if f.balances == nil {
		return map[string]*big.Int{}, nil
	}
	key := ledger + "|" + string(query)
	b, ok := f.balances[key]
	if !ok {
		return map[string]*big.Int{}, nil
	}
	return b, nil
}
func (f *fakeLedger) ListAccounts(_ context.Context, ledger string, query json.RawMessage, _ uint64, limit int) ([]engine.Account, error) {
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
}

func (f *fakePayments) PoolBalanceLatest(_ context.Context, id string) (map[string]*big.Int, error) {
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

// --- kernel/template equivalence (golden) -----------------------------------

// TestCrossCheck_DirectMathMatchesRenderedCEL guards the property the runtime
// cross-check used to assert: each aggregate template's direct big.Int verdict
// equals the verdict of the CEL expression it renders into evidence. It was a
// per-evaluation runtime check (two reads of the same state → always equal, so
// pure cost); moved here as a code-property test. It reads the same fakes on
// both paths — a break in the rendering (celTerm/build*Expression) or the direct
// math makes them diverge and fails this test.
func TestCrossCheck_DirectMathMatchesRenderedCEL(t *testing.T) {
	in := engine.EvalInput{PIT: time.Now()}
	lo, hi := int64(100), int64(1000)

	cases := []struct {
		name string
		tmpl Evaluator
		l    *fakeLedger
		p    *fakePayments
		spec any
	}{
		{
			name: "invariant/pass",
			tmpl: NewLedgerInvariant(),
			l: &fakeLedger{balances: map[string]map[string]*big.Int{
				`buildr|"held"`:       {"USD/2": big.NewInt(350)},
				`buildr|"obligation"`: {"USD/2": big.NewInt(-350)},
			}},
			spec: InvariantSpec{
				Terms: []InvariantTerm{
					{Ledger: "buildr", Query: json.RawMessage(`"held"`), Sign: 1},
					{Ledger: "buildr", Query: json.RawMessage(`"obligation"`), Sign: 1},
				},
				Tolerance: map[string]int64{"USD/2": 0},
			},
		},
		{
			name: "invariant/fail",
			tmpl: NewLedgerInvariant(),
			l: &fakeLedger{balances: map[string]map[string]*big.Int{
				`l|"a"`: {"USD/2": big.NewInt(100)},
				`l|"b"`: {"USD/2": big.NewInt(50)},
			}},
			spec: InvariantSpec{
				Terms: []InvariantTerm{
					{Ledger: "l", Query: json.RawMessage(`"a"`), Sign: 1},
					{Ledger: "l", Query: json.RawMessage(`"b"`), Sign: 1},
				},
				Tolerance: map[string]int64{"USD/2": 100},
			},
		},
		{
			name: "threshold/mixed-assets",
			tmpl: NewAccountThreshold(),
			l: &fakeLedger{balances: map[string]map[string]*big.Int{
				`l|"q"`: {"USD/2": big.NewInt(500), "EUR/2": big.NewInt(50)},
			}},
			spec: ThresholdSpec{
				Ledger: "l", Query: json.RawMessage(`"q"`), Mode: ThresholdAggregate,
				Bounds: map[string]ThresholdBounds{
					"USD/2": {Min: &lo, Max: &hi}, // 500 in [100,1000] → pass
					"EUR/2": {Min: &lo},           // 50 < 100 → fail
				},
			},
		},
		{
			name: "parity/ledger-ledger",
			tmpl: NewSourceParity(),
			l: &fakeLedger{balances: map[string]map[string]*big.Int{
				`a|"x"`: {"USD/2": big.NewInt(1000)},
				`b|"y"`: {"USD/2": big.NewInt(1000), "EUR/2": big.NewInt(5)}, // EUR mismatch → fail
			}},
			spec: ParitySpec{
				Left:      SourceSpec{Kind: SourceLedger, Ledger: "a", Query: json.RawMessage(`"x"`)},
				Right:     SourceSpec{Kind: SourceLedger, Ledger: "b", Query: json.RawMessage(`"y"`)},
				Tolerance: map[string]int64{"USD/2": 0, "EUR/2": 0},
			},
		},
		{
			name: "parity/ledger-pool",
			tmpl: NewSourceParity(),
			l: &fakeLedger{balances: map[string]map[string]*big.Int{
				`a|"x"`: {"USD/2": big.NewInt(1000)},
			}},
			p: &fakePayments{pools: map[string]map[string]*big.Int{
				"pool1": {"USD/2": big.NewInt(1000)},
			}},
			spec: ParitySpec{
				Left:      SourceSpec{Kind: SourceLedger, Ledger: "a", Query: json.RawMessage(`"x"`)},
				Right:     SourceSpec{Kind: SourcePaymentsPool, PoolID: "pool1"},
				Tolerance: map[string]int64{"USD/2": 0},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			if p == nil {
				p = &fakePayments{}
			}
			eng, res := newTestEngine(t, tc.l, p)
			out, err := tc.tmpl.Evaluate(context.Background(), mustJSON(t, tc.spec), eng, res, in)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if len(out) == 0 {
				t.Fatalf("no outcomes produced")
			}
			for _, o := range out {
				celExpr, _ := o.Evidence["compiledCEL"].(string)
				if celExpr == "" {
					t.Fatalf("outcome %s: missing compiledCEL in evidence", o.Fingerprint)
				}
				compiled, err := eng.Compile(celExpr)
				if err != nil {
					t.Fatalf("compile rendered CEL %q: %v", celExpr, err)
				}
				evalOut, err := eng.Evaluate(context.Background(), compiled, in)
				if err != nil {
					t.Fatalf("evaluate rendered CEL %q: %v", celExpr, err)
				}
				if evalOut.Passed != o.Passed {
					t.Errorf("%s: rendered CEL %q verdict=%v, direct=%v",
						o.Fingerprint, celExpr, evalOut.Passed, o.Passed)
				}
			}
		})
	}
}
