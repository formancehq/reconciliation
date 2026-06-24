package templates

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
)

func acct(addr string, asset string, amount int64) engine.Account {
	return engine.Account{Address: addr, Balances: map[string]*big.Int{asset: big.NewInt(amount)}}
}

// account_threshold per_account fans out one Outcome per (account, asset).
func TestThreshold_PerAccount_FansOut(t *testing.T) {
	tmpl := NewAccountThreshold()
	const q = `{}`
	min := int64(100)
	l := &fakeLedger{accounts: map[string][]engine.Account{
		"main|" + q: {
			acct("merchant:a", "USD/2", 150), // >= 100 → pass
			acct("merchant:b", "USD/2", 50),  // < 100  → fail
		},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	spec := mustJSON(t, ThresholdSpec{
		Ledger: "main", Query: json.RawMessage(q), Mode: ThresholdPerAccount,
		Bounds: map[string]ThresholdBounds{"USD/2": {Min: &min}},
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 outcomes (one per account), got %d", len(out))
	}
	a := findOutcome(out, "asset:USD/2|account:merchant:a")
	b := findOutcome(out, "asset:USD/2|account:merchant:b")
	if a == nil || !a.Passed {
		t.Errorf("merchant:a should pass, got %+v", a)
	}
	if b == nil || b.Passed {
		t.Errorf("merchant:b should fail, got %+v", b)
	}
	if b != nil && b.Evidence["account"] != "merchant:b" {
		t.Errorf("evidence.account = %v, want merchant:b", b.Evidence["account"])
	}
}

// The accounts budget aborts (errors) rather than truncating.
func TestThreshold_PerAccount_BudgetExceeded(t *testing.T) {
	tmpl := NewAccountThreshold()
	const q = `{}`
	min := int64(0)
	l := &fakeLedger{accounts: map[string][]engine.Account{
		"main|" + q: {acct("a", "USD/2", 1), acct("b", "USD/2", 1)},
	}}
	eng, err := engine.New(engine.Resolvers{Ledger: l, Payments: &fakePayments{}}, engine.Limits{MaxAccountsScanned: 1})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	res := engine.Resolvers{Ledger: l, Payments: &fakePayments{}}
	spec := mustJSON(t, ThresholdSpec{
		Ledger: "main", Query: json.RawMessage(q), Mode: ThresholdPerAccount,
		Bounds: map[string]ThresholdBounds{"USD/2": {Min: &min}},
	})
	if _, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()}); err == nil {
		t.Fatal("expected accounts-budget error, got nil")
	}
}

// source_parity per_account compares two ledgers account-by-account (by address).
func TestSourceParity_PerAccount_LedgerVsLedger(t *testing.T) {
	tmpl := NewSourceParity()
	const q = `{}`
	l := &fakeLedger{accounts: map[string][]engine.Account{
		"a|" + q: {acct("m:1", "USD/2", 100), acct("m:2", "USD/2", 100)},
		"b|" + q: {acct("m:1", "USD/2", 100), acct("m:2", "USD/2", 70)}, // m:2 differs by 30
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	spec := mustJSON(t, ParitySpec{
		Left:  ledgerSource("a", q),
		Right: ledgerSource("b", q),
		Scope: ScopePerAccount,
	})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(out))
	}
	if o := findOutcome(out, "asset:USD/2|account:m:1"); o == nil || !o.Passed {
		t.Errorf("m:1 should reconcile, got %+v", o)
	}
	if o := findOutcome(out, "asset:USD/2|account:m:2"); o == nil || o.Passed {
		t.Errorf("m:2 should fail (30 gap, tol 0), got %+v", o)
	}
}

// per_account requires both sources to be ledger — a pool side is rejected.
func TestSourceParity_PerAccount_RejectsPool(t *testing.T) {
	tmpl := NewSourceParity()
	spec := mustJSON(t, ParitySpec{
		Left:  ledgerSource("l", `{}`),
		Right: poolSource("p"),
		Scope: ScopePerAccount,
	})
	if err := tmpl.Validate(spec); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec (pool can't be per-account), got %v", err)
	}
}
