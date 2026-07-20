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
)

func ledgerSource(ledger, query string) SourceSpec {
	return SourceSpec{Kind: SourceLedger, Ledger: ledger, Query: json.RawMessage(query)}
}
func poolSource(id string) SourceSpec {
	return SourceSpec{Kind: SourcePaymentsPool, PoolID: id}
}

func TestSourceParity_Validate(t *testing.T) {
	tmpl := NewSourceParity()
	cases := map[string]ParitySpec{
		"missing left kind":     {Left: SourceSpec{}, Right: poolSource("p")},
		"unknown left kind":     {Left: SourceSpec{Kind: "bank"}, Right: poolSource("p")},
		"ledger missing ledger": {Left: SourceSpec{Kind: SourceLedger, Query: json.RawMessage(`{}`)}, Right: poolSource("p")},
		"ledger missing query":  {Left: SourceSpec{Kind: SourceLedger, Ledger: "l"}, Right: poolSource("p")},
		"pool missing id":       {Left: ledgerSource("l", `{}`), Right: SourceSpec{Kind: SourcePaymentsPool}},
		"negative tolerance":    {Left: ledgerSource("l", `{}`), Right: poolSource("p"), Tolerance: map[string]int64{"USD/2": -1}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := tmpl.Validate(mustJSON(t, spec)); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

// TestSourceParity_LedgerVsPool — the drift use case expressed as parity:
// ledger and pool balances should be equal within tolerance.
func TestSourceParity_LedgerVsPool(t *testing.T) {
	tmpl := NewSourceParity()
	const q = `{"$match":{"address":"cash"}}`
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"main|" + q: {"USD/2": big.NewInt(100)},
	}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{
		"acct": {"USD/2": big.NewInt(130)}, // 30 above the ledger
	}}
	eng, res := newTestEngine(t, l, p)

	// tolerance 0 → the 30 gap fails.
	spec := mustJSON(t, ParitySpec{Left: ledgerSource("main", q), Right: poolSource("acct")})
	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	usd := findOutcome(out, "asset:USD/2")
	if usd == nil || usd.Passed {
		t.Fatalf("expected USD/2 to fail at tolerance 0, got %+v", out)
	}
	if usd.Evidence["difference"] != "30" {
		t.Errorf("expected difference 30, got %v", usd.Evidence["difference"])
	}

	// tolerance 50 → within bound, passes.
	spec = mustJSON(t, ParitySpec{Left: ledgerSource("main", q), Right: poolSource("acct"), Tolerance: map[string]int64{"USD/2": 50}})
	out, err = tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate (tol 50): %v", err)
	}
	if usd := findOutcome(out, "asset:USD/2"); usd == nil || !usd.Passed {
		t.Fatalf("expected USD/2 to pass at tolerance 50, got %+v", out)
	}
}

// TestSourceParity_LedgerVsLedger — the pairing the old templates couldn't
// express: a sub-ledger reconciled against a control account on another ledger.
func TestSourceParity_LedgerVsLedger(t *testing.T) {
	tmpl := NewSourceParity()
	const q = `{"$match":{"address":"x"}}`
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"sub|" + q:     {"USD/2": big.NewInt(100)},
		"control|" + q: {"USD/2": big.NewInt(100)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})
	spec := mustJSON(t, ParitySpec{Left: ledgerSource("sub", q), Right: ledgerSource("control", q)})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if usd := findOutcome(out, "asset:USD/2"); usd == nil || !usd.Passed {
		t.Fatalf("expected ledger↔ledger match to pass, got %+v", out)
	}
}

// TestSourceParity_AssetUnion — an asset present on only one side is compared
// against zero on the other (so it fails unless within tolerance).
func TestSourceParity_AssetUnion(t *testing.T) {
	tmpl := NewSourceParity()
	const q = `{}`
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"main|" + q: {"USD/2": big.NewInt(100), "EUR/2": big.NewInt(50)},
	}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{
		"acct": {"USD/2": big.NewInt(100)}, // no EUR/2 → treated as 0
	}}
	eng, res := newTestEngine(t, l, p)
	spec := mustJSON(t, ParitySpec{Left: ledgerSource("main", q), Right: poolSource("acct")})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(out.Outcomes) != 2 {
		t.Fatalf("expected 2 outcomes (USD/2, EUR/2), got %d", len(out.Outcomes))
	}
	if usd := findOutcome(out, "asset:USD/2"); usd == nil || !usd.Passed {
		t.Errorf("USD/2 should pass (100 == 100), got %+v", usd)
	}
	if eur := findOutcome(out, "asset:EUR/2"); eur == nil || eur.Passed {
		t.Errorf("EUR/2 should fail (50 vs missing→0), got %+v", eur)
	}
}

func TestSourceParity_Explain(t *testing.T) {
	tmpl := NewSourceParity()
	spec := mustJSON(t, ParitySpec{
		Left:      ledgerSource("main", `{"$match":{"address":"cash"}}`),
		Right:     poolSource("acct"),
		Tolerance: map[string]int64{"USD/2": 50},
	})
	expr, err := tmpl.Explain(spec)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	for _, want := range []string{"abs(", "ledgerSet(", `pool("acct")`, " - ", "<= 50"} {
		if !strings.Contains(expr, want) {
			t.Errorf("Explain output %q missing %q", expr, want)
		}
	}
	// The Explain output is persisted as rule.explanation_cel and CreateRule
	// sanity-compiles it against the kernel. Guard that it actually parses.
	eng, _ := newTestEngine(t, &fakeLedger{}, &fakePayments{})
	if _, err := eng.Compile(expr); err != nil {
		t.Errorf("Explain output does not compile: %v\nexpr: %s", err, expr)
	}
}
