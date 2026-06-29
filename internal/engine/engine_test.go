package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"testing"
	"time"
)

// --- in-memory resolver fakes ------------------------------------------------

type fakeLedger struct {
	// keyed by ledger + canonical(query) → balances
	balances map[string]map[string]*big.Int
	// optional error to inject
	err error
}

func (f *fakeLedger) AggregateBalance(_ context.Context, ledger string, query json.RawMessage, _ time.Time) (map[string]*big.Int, error) {
	if f.err != nil {
		return nil, f.err
	}
	key := ledger + "|" + string(query)
	b, ok := f.balances[key]
	if !ok {
		return map[string]*big.Int{}, nil
	}
	return b, nil
}

func (f *fakeLedger) ListAccounts(_ context.Context, _ string, _ json.RawMessage, _ time.Time, _ int) ([]Account, error) {
	return nil, errors.New("ListAccounts not implemented in fake")
}

type fakePayments struct {
	balances map[string]map[string]*big.Int // poolID → balances
	err      error
}

func (f *fakePayments) PoolBalanceLatest(_ context.Context, poolID string) (map[string]*big.Int, error) {
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.balances[poolID]
	if !ok {
		return map[string]*big.Int{}, nil
	}
	return b, nil
}

// --- helpers -----------------------------------------------------------------

func newTestEngine(t *testing.T, l *fakeLedger, p *fakePayments) *Engine {
	t.Helper()
	if l == nil {
		l = &fakeLedger{}
	}
	if p == nil {
		p = &fakePayments{}
	}
	eng, err := New(Resolvers{Ledger: l, Payments: p}, DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng
}

// --- compile-time validation tests ------------------------------------------

func TestCompile_InvalidSyntax(t *testing.T) {
	eng := newTestEngine(t, nil, nil)
	_, err := eng.Compile("this is not + valid &&")
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("expected ErrCompile, got %v", err)
	}
}

func TestCompile_TypeMismatch(t *testing.T) {
	eng := newTestEngine(t, nil, nil)
	// ledgerSet expects (string, string); passing ints should fail type-check.
	_, err := eng.Compile(`balance(ledgerSet(1, 2)) == 0`)
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("expected ErrCompile, got %v", err)
	}
}

func TestCompile_NonBooleanExpression(t *testing.T) {
	eng := newTestEngine(t, nil, nil)
	_, err := eng.Compile(`balance(ledgerSet("l", "q"))`) // int, not bool
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("expected ErrCompile on non-bool, got %v", err)
	}
}

func TestCompile_EmptyExpression(t *testing.T) {
	eng := newTestEngine(t, nil, nil)
	_, err := eng.Compile("")
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("expected ErrCompile on empty, got %v", err)
	}
}

func TestCompile_AcceptsValidExpressions(t *testing.T) {
	eng := newTestEngine(t, nil, nil)
	cases := []string{
		`balance(ledgerSet("l", "q")) + balance(pool("p")) == 0`,
		`balance(ledgerSet("l", "q"), "USD") >= 100`,
		`balances(ledgerSet("l", "q"))["USD"] == 350`,
		`abs(balance(ledgerSet("l","q"))) <= 50`,
		`sum([balance(ledgerSet("l1","q1")), balance(ledgerSet("l2","q2"))]) == 0`,
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			if _, err := eng.Compile(expr); err != nil {
				t.Fatalf("Compile(%q): %v", expr, err)
			}
		})
	}
}

// --- evaluation tests -------------------------------------------------------

func TestEvaluate_DriftZero_Pass(t *testing.T) {
	// ledger USD=+350 against pool USD=-350 → drift 0 → PASS.
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"buildr|metadata-trust": {"USD/2": big.NewInt(350)},
	}}
	p := &fakePayments{balances: map[string]map[string]*big.Int{
		"pool_xyz": {"USD/2": big.NewInt(-350)},
	}}
	eng := newTestEngine(t, l, p)
	c, err := eng.Compile(`balance(ledgerSet("buildr","metadata-trust"), "USD/2") + balance(pool("pool_xyz"), "USD/2") == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out, err := eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !out.Passed {
		t.Fatalf("expected pass, got fail (result=%v)", out.Result)
	}
}

func TestEvaluate_DriftNonZero_Fail(t *testing.T) {
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"buildr|q1": {"USD/2": big.NewInt(350)},
	}}
	p := &fakePayments{balances: map[string]map[string]*big.Int{
		"pool": {"USD/2": big.NewInt(-300)},
	}}
	eng := newTestEngine(t, l, p)
	c, err := eng.Compile(`balance(ledgerSet("buildr","q1"), "USD/2") + balance(pool("pool"), "USD/2") == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out, err := eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if out.Passed {
		t.Fatalf("expected fail, got pass")
	}
}

func TestEvaluate_PerSourcePIT_Recorded(t *testing.T) {
	pit := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"l|q": {"USD/2": big.NewInt(100)},
	}}
	p := &fakePayments{balances: map[string]map[string]*big.Int{
		"p": {"USD/2": big.NewInt(-100)},
	}}
	eng := newTestEngine(t, l, p)
	c, err := eng.Compile(`balance(ledgerSet("l","q"), "USD/2") + balance(pool("p"), "USD/2") == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	out, err := eng.Evaluate(context.Background(), c, EvalInput{PIT: pit, SafetyMargin: 30 * time.Second})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !out.Passed {
		t.Fatalf("expected pass, got fail")
	}

	wantPIT := pit.Add(-30 * time.Second)
	if len(out.PitPerSource) != 2 {
		t.Fatalf("expected 2 PIT entries, got %d: %v", len(out.PitPerSource), out.PitPerSource)
	}
	for key, gotPIT := range out.PitPerSource {
		if !gotPIT.Equal(wantPIT) {
			t.Errorf("PIT for %s = %v, want %v (PIT - safetyMargin)", key, gotPIT, wantPIT)
		}
	}
}

func TestEvaluate_SafetyMargin_Subtracts(t *testing.T) {
	pit := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	margin := 45 * time.Second

	var capturedPIT time.Time
	l := &fakeLedgerWithCapture{
		fakeLedger: fakeLedger{balances: map[string]map[string]*big.Int{
			"l|q": {"USD/2": big.NewInt(1)},
		}},
		onAggregate: func(at time.Time) { capturedPIT = at },
	}
	eng := newTestEngine(t, &l.fakeLedger, nil)
	eng.resolvers.Ledger = l // override with capturing variant
	c, err := eng.Compile(`balance(ledgerSet("l","q")) == 1`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := eng.Evaluate(context.Background(), c, EvalInput{PIT: pit, SafetyMargin: margin}); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	want := pit.Add(-margin)
	if !capturedPIT.Equal(want) {
		t.Errorf("resolver called with PIT %v, want %v", capturedPIT, want)
	}
}

// --- error-path tests -------------------------------------------------------

func TestEvaluate_ResolverError_Surfaces(t *testing.T) {
	l := &fakeLedger{err: errors.New("ledger timeout")}
	p := &fakePayments{}
	eng := newTestEngine(t, l, p)
	c, err := eng.Compile(`balance(ledgerSet("l","q")) == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if !errors.Is(err, ErrEvaluate) {
		t.Fatalf("expected ErrEvaluate wrap, got %v", err)
	}
}

func TestEvaluate_BalanceSingleAsset_MultiAssetSource_Errors(t *testing.T) {
	// Source has both USD/2 and EUR/2 → balance(source) without an asset arg
	// must error so templates can't silently pick the wrong asset.
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"l|q": {"USD/2": big.NewInt(100), "EUR/2": big.NewInt(200)},
	}}
	eng := newTestEngine(t, l, nil)
	c, err := eng.Compile(`balance(ledgerSet("l","q")) == 100`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if !errors.Is(err, ErrEvaluate) {
		t.Fatalf("expected ErrEvaluate on multi-asset balance(), got %v", err)
	}
}

// TestEvaluate_AbsMinInt64Overflow guards against a silent wrap where
// abs(math.MinInt64) returns math.MinInt64 (negative). A naive
// `if i < 0 { i = -i }` implementation has this bug. The builtin must
// surface an evaluation error instead.
func TestEvaluate_AbsMinInt64Overflow(t *testing.T) {
	eng := newTestEngine(t, &fakeLedger{}, &fakePayments{})
	c, err := eng.Compile(fmt.Sprintf(`abs(%d) >= 0`, int64(math.MinInt64)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if !errors.Is(err, ErrEvaluate) {
		t.Fatalf("expected ErrEvaluate from abs(MinInt64), got %v", err)
	}
}

// --- helper: capturing fake -------------------------------------------------

type fakeLedgerWithCapture struct {
	fakeLedger
	onAggregate func(time.Time)
}

func (f *fakeLedgerWithCapture) AggregateBalance(ctx context.Context, ledger string, query json.RawMessage, pit time.Time) (map[string]*big.Int, error) {
	if f.onAggregate != nil {
		f.onAggregate(pit)
	}
	return f.fakeLedger.AggregateBalance(ctx, ledger, query, pit)
}

// silence unused-import warning if a future refactor drops references
var _ = fmt.Sprintf
