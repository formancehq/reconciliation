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
	// keyed by ledger + canonical(query) → accounts (for ListAccounts)
	accounts map[string][]Account
	// optional error to inject
	err error
}

func (f *fakeLedger) AggregateBalance(_ context.Context, ledger string, query json.RawMessage) (map[string]*big.Int, error) {
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

func (f *fakeLedger) ListAccounts(_ context.Context, ledger string, query json.RawMessage, _ int) ([]Account, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.accounts[ledger+"|"+string(query)], nil
}

// --- helpers -----------------------------------------------------------------

func newTestEngine(t *testing.T, l *fakeLedger) *Engine {
	t.Helper()
	if l == nil {
		l = &fakeLedger{}
	}
	eng, err := New(Resolvers{Ledger: l}, DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng
}

// --- compile-time validation tests ------------------------------------------

func TestCompile_InvalidSyntax(t *testing.T) {
	eng := newTestEngine(t, nil)
	_, err := eng.Compile("this is not + valid &&")
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("expected ErrCompile, got %v", err)
	}
}

func TestCompile_TypeMismatch(t *testing.T) {
	eng := newTestEngine(t, nil)
	// ledgerSet expects (string, string); passing ints should fail type-check.
	_, err := eng.Compile(`balance(ledgerSet(1, 2)) == 0`)
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("expected ErrCompile, got %v", err)
	}
}

func TestCompile_NonBooleanExpression(t *testing.T) {
	eng := newTestEngine(t, nil)
	_, err := eng.Compile(`balance(ledgerSet("l", "q"))`) // int, not bool
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("expected ErrCompile on non-bool, got %v", err)
	}
}

func TestCompile_EmptyExpression(t *testing.T) {
	eng := newTestEngine(t, nil)
	_, err := eng.Compile("")
	if !errors.Is(err, ErrCompile) {
		t.Fatalf("expected ErrCompile on empty, got %v", err)
	}
}

func TestCompile_AcceptsValidExpressions(t *testing.T) {
	eng := newTestEngine(t, nil)
	cases := []string{
		`balance(ledgerSet("l", "q")) + balance(ledgerSet("l2", "q2")) == 0`,
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
	// ledger held USD=+350 against control obligation USD=-350 → drift 0 → PASS.
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"buildr|held":        {"USD/2": big.NewInt(350)},
		"control|obligation": {"USD/2": big.NewInt(-350)},
	}}
	eng := newTestEngine(t, l)
	c, err := eng.Compile(`balance(ledgerSet("buildr","held"), "USD/2") + balance(ledgerSet("control","obligation"), "USD/2") == 0`)
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
		"buildr|q1":  {"USD/2": big.NewInt(350)},
		"control|q2": {"USD/2": big.NewInt(-300)},
	}}
	eng := newTestEngine(t, l)
	c, err := eng.Compile(`balance(ledgerSet("buildr","q1"), "USD/2") + balance(ledgerSet("control","q2"), "USD/2") == 0`)
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

// --- error-path tests -------------------------------------------------------

func TestEvaluate_ResolverError_Surfaces(t *testing.T) {
	l := &fakeLedger{err: errors.New("ledger timeout")}
	eng := newTestEngine(t, l)
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
	eng := newTestEngine(t, l)
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
	eng := newTestEngine(t, &fakeLedger{})
	c, err := eng.Compile(fmt.Sprintf(`abs(%d) >= 0`, int64(math.MinInt64)))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	_, err = eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if !errors.Is(err, ErrEvaluate) {
		t.Fatalf("expected ErrEvaluate from abs(MinInt64), got %v", err)
	}
}

// silence unused-import warning if a future refactor drops references
var _ = fmt.Sprintf
