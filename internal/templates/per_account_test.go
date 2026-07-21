package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
)

func acct(addr string, asset string, amount int64) engine.Account {
	return engine.Account{Address: addr, Balances: map[string]*big.Int{asset: big.NewInt(amount)}}
}

// NOTE: per_account is parked out of the V1 public API — rule creation is
// rejected at Validate (see perAccountParkedMsg in source.go). The tests below
// call Evaluate directly, which does NOT re-run Validate, so they keep the
// preserved evaluatePerAccount implementation compiling and covered for the day
// the park is lifted. Validate-level rejection is asserted in
// TestThreshold_Validate_PerAccount_Parked and TestSourceParity_Validate_PerAccount_Parked.

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
	if len(out.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes (one per account), got %d", len(out.Outcomes))
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

// BenchmarkThreshold_PerAccount records the cost of the current snapshot-CEL
// fan-out. Keep multiple sizes so production tuning can distinguish fixed CEL
// setup cost from the per-fingerprint compile/evaluate cost.
func BenchmarkThreshold_PerAccount(b *testing.B) {
	for _, accountCount := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("accounts_%d", accountCount), func(b *testing.B) {
			const q = `{}`
			min := int64(100)
			accounts := make([]engine.Account, 0, accountCount)
			for i := 0; i < accountCount; i++ {
				accounts = append(accounts, acct(fmt.Sprintf("merchant:%d", i), "USD/2", 150))
			}
			ledger := &fakeLedger{accounts: map[string][]engine.Account{"main|" + q: accounts}}
			resolvers := engine.Resolvers{Ledger: ledger, Payments: &fakePayments{}}
			eng, err := engine.New(resolvers, engine.Limits{MaxAccountsScanned: accountCount})
			if err != nil {
				b.Fatalf("engine.New: %v", err)
			}
			spec, err := json.Marshal(ThresholdSpec{
				Ledger: "main", Query: json.RawMessage(q), Mode: ThresholdPerAccount,
				Bounds: map[string]ThresholdBounds{"USD/2": {Min: &min}},
			})
			if err != nil {
				b.Fatalf("marshal spec: %v", err)
			}
			tmpl := NewAccountThreshold()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := tmpl.Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{PIT: time.Now()}); err != nil {
					b.Fatalf("Evaluate: %v", err)
				}
			}
		})
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
	if len(out.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(out.Outcomes))
	}
	if o := findOutcome(out, "asset:USD/2|account:m:1"); o == nil || !o.Passed {
		t.Errorf("m:1 should reconcile, got %+v", o)
	}
	if o := findOutcome(out, "asset:USD/2|account:m:2"); o == nil || o.Passed {
		t.Errorf("m:2 should fail (30 gap, tol 0), got %+v", o)
	}
}

func TestSourceParity_PerAccountSharesAccountBudgetAcrossSources(t *testing.T) {
	tmpl := NewSourceParity()
	const q = `{}`
	l := &fakeLedger{accounts: map[string][]engine.Account{
		"a|" + q: {acct("m:1", "USD/2", 1), acct("m:2", "USD/2", 1)},
		"b|" + q: {acct("m:1", "USD/2", 1), acct("m:2", "USD/2", 1)},
	}}
	res := engine.Resolvers{Ledger: l, Payments: &fakePayments{}}
	eng, err := engine.New(res, engine.Limits{MaxAccountsScanned: 3})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	spec := mustJSON(t, ParitySpec{Left: ledgerSource("a", q), Right: ledgerSource("b", q), Scope: ScopePerAccount})
	if _, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()}); err == nil {
		t.Fatal("expected the combined account scan to exceed the shared budget")
	}
}

// per_account scope is parked in V1 — Validate rejects it even for two valid
// ledger sources (the case that would pass once the park is lifted). The
// both-sides-must-be-ledger constraint stays enforced at evaluation time by
// SourceSpec.resolveAccounts for the preserved Evaluate path.
func TestSourceParity_Validate_PerAccount_Parked(t *testing.T) {
	tmpl := NewSourceParity()
	spec := mustJSON(t, ParitySpec{
		Left:  ledgerSource("a", `{}`),
		Right: ledgerSource("b", `{}`),
		Scope: ScopePerAccount,
	})
	if err := tmpl.Validate(spec); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected per_account scope to be rejected as parked, got %v", err)
	}
}
