package engine

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"
)

// TestEvaluate_BalancesMap exercises the balances(source) builtin, which returns
// the full per-asset map (as opposed to balance(source, asset)).
func TestEvaluate_BalancesMap(t *testing.T) {
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"l|q": {"USD/2": big.NewInt(350), "EUR/2": big.NewInt(100)},
	}}
	eng := newTestEngine(t, l, nil)
	c, err := eng.Compile(`balances(ledgerSet("l","q"))["USD/2"] == 350 && balances(ledgerSet("l","q"))["EUR/2"] == 100`)
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

// TestEvaluate_SumList exercises the sum([...]) builtin over a list of balances.
func TestEvaluate_SumList(t *testing.T) {
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"l1|q": {"USD/2": big.NewInt(100)},
		"l2|q": {"USD/2": big.NewInt(-100)},
	}}
	eng := newTestEngine(t, l, nil)
	c, err := eng.Compile(`sum([balance(ledgerSet("l1","q"),"USD/2"), balance(ledgerSet("l2","q"),"USD/2")]) == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out, err := eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !out.Passed {
		t.Fatalf("expected sum==0 to pass, got fail (result=%v)", out.Result)
	}
}

// TestEvaluate_BalanceOverflowsInt64_Errors guards the bigIntToInt ceiling: a
// balance exceeding int64 must surface an evaluation error, never wrap silently
// — reconciliation balances are financial and a wrapped magnitude would flip a
// verdict.
func TestEvaluate_BalanceOverflowsInt64_Errors(t *testing.T) {
	huge := new(big.Int).Lsh(big.NewInt(1), 63) // 2^63 = math.MaxInt64 + 1
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"l|q": {"USD/2": huge},
	}}
	eng := newTestEngine(t, l, nil)
	c, err := eng.Compile(`balance(ledgerSet("l","q"), "USD/2") == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if !errors.Is(err, ErrEvaluate) {
		t.Fatalf("expected ErrEvaluate on a >int64 balance, got %v", err)
	}
}

// TestEvaluate_BalanceMissingAsset_ReadsZero — balance(source, asset) for an
// asset absent from the resolved map reads 0 (so an asset present only on one
// side of a comparison is treated as zero, not an error).
func TestEvaluate_BalanceMissingAsset_ReadsZero(t *testing.T) {
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"l|q": {"USD/2": big.NewInt(100)},
	}}
	eng := newTestEngine(t, l, nil)
	c, err := eng.Compile(`balance(ledgerSet("l","q"), "JPY/0") == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out, err := eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !out.Passed {
		t.Fatalf("expected missing asset to read 0 → pass, got fail")
	}
}

// TestNew_RequiresResolvers — both resolvers are mandatory for V1.
func TestNew_RequiresResolvers(t *testing.T) {
	if _, err := New(Resolvers{Payments: &fakePayments{}}, DefaultLimits); err == nil {
		t.Error("expected error when Ledger resolver is nil")
	}
	if _, err := New(Resolvers{Ledger: &fakeLedger{}}, DefaultLimits); err == nil {
		t.Error("expected error when Payments resolver is nil")
	}
}

// TestNew_PartialLimits_InheritDefaults — a caller passing only one limit must
// still get the standard guards on the others (mergeLimits fills zero fields),
// and the accessors report the merged values.
func TestNew_PartialLimits_InheritDefaults(t *testing.T) {
	eng, err := New(Resolvers{Ledger: &fakeLedger{}, Payments: &fakePayments{}}, Limits{MaxCELCost: 12345})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := eng.MaxAccountsScanned(); got != DefaultLimits.MaxAccountsScanned {
		t.Errorf("MaxAccountsScanned = %d, want default %d", got, DefaultLimits.MaxAccountsScanned)
	}
	if got := eng.MaxWallClock(); got != DefaultLimits.MaxWallClock {
		t.Errorf("MaxWallClock = %s, want default %s", got, DefaultLimits.MaxWallClock)
	}
}

// TestBudgetTracker_ChargeAccounts — the per-evaluation accounts budget: charges
// accumulate, a zero/negative charge is a no-op, and crossing the ceiling errors.
func TestBudgetTracker_ChargeAccounts(t *testing.T) {
	b := newBudgetTracker(Limits{MaxAccountsScanned: 100})
	if err := b.ChargeAccounts(0); err != nil {
		t.Fatalf("zero charge should be a no-op: %v", err)
	}
	if err := b.ChargeAccounts(60); err != nil {
		t.Fatalf("60 within budget: %v", err)
	}
	if got := b.AccountsScanned(); got != 60 {
		t.Errorf("AccountsScanned = %d, want 60", got)
	}
	if err := b.ChargeAccounts(50); err == nil {
		t.Error("expected budget-exceeded error crossing the 100 ceiling")
	}
}
