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
	return SourceSpec{Ledger: ledger, Query: json.RawMessage(query)}
}

func TestSourceParity_Validate(t *testing.T) {
	tmpl := NewSourceParity()
	cases := map[string]ParitySpec{
		"missing left ledger":  {Left: SourceSpec{Query: json.RawMessage(`{}`)}, Right: ledgerSource("r", `{}`)},
		"missing left query":   {Left: SourceSpec{Ledger: "l"}, Right: ledgerSource("r", `{}`)},
		"missing right ledger": {Left: ledgerSource("l", `{}`), Right: SourceSpec{Query: json.RawMessage(`{}`)}},
		"missing right query":  {Left: ledgerSource("l", `{}`), Right: SourceSpec{Ledger: "r"}},
		"negative tolerance":   {Left: ledgerSource("l", `{}`), Right: ledgerSource("r", `{}`), Tolerance: map[string]int64{"USD/2": -1}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := tmpl.Validate(mustJSON(t, spec)); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

func TestSourceParity_ValidationUsesHumanLabelsAndMachinePaths(t *testing.T) {
	tmpl := NewSourceParity()

	err := tmpl.Validate(mustJSON(t, ParitySpec{
		Left:  SourceSpec{Query: json.RawMessage(`{}`)},
		Right: ledgerSource("control", `{}`),
	}))
	if err == nil || !strings.Contains(err.Error(), "Source A") || !strings.Contains(err.Error(), "field: left.ledger") {
		t.Fatalf("left validation error = %v, want Source A and left.ledger", err)
	}

	err = tmpl.Validate(mustJSON(t, ParitySpec{
		Left:  ledgerSource("book", `{}`),
		Right: SourceSpec{Ledger: "control"},
	}))
	if err == nil || !strings.Contains(err.Error(), "Source B") || !strings.Contains(err.Error(), "field: right.query") {
		t.Fatalf("right validation error = %v, want Source B and right.query", err)
	}
}

// TestSourceParity_Drift — the drift use case expressed as parity: two ledgers'
// records of the same money should be equal within tolerance.
func TestSourceParity_Drift(t *testing.T) {
	tmpl := NewSourceParity()
	const q = `{"$match":{"address":"cash"}}`
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"main|" + q:    {"USD/2": big.NewInt(100)},
		"control|" + q: {"USD/2": big.NewInt(130)}, // 30 above the sub-ledger
	}}
	eng, res := newTestEngine(t, l)

	// tolerance 0 → the 30 gap fails.
	spec := mustJSON(t, ParitySpec{Left: ledgerSource("main", q), Right: ledgerSource("control", q)})
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
	spec = mustJSON(t, ParitySpec{Left: ledgerSource("main", q), Right: ledgerSource("control", q), Tolerance: map[string]int64{"USD/2": 50}})
	out, err = tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate (tol 50): %v", err)
	}
	if usd := findOutcome(out, "asset:USD/2"); usd == nil || !usd.Passed {
		t.Fatalf("expected USD/2 to pass at tolerance 50, got %+v", out)
	}
}

// TestSourceParity_LedgerVsLedger — a sub-ledger reconciled against a control
// account on another ledger.
func TestSourceParity_LedgerVsLedger(t *testing.T) {
	tmpl := NewSourceParity()
	const q = `{"$match":{"address":"x"}}`
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		"sub|" + q:     {"USD/2": big.NewInt(100)},
		"control|" + q: {"USD/2": big.NewInt(100)},
	}}
	eng, res := newTestEngine(t, l)
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
		"main|" + q:    {"USD/2": big.NewInt(100), "EUR/2": big.NewInt(50)},
		"control|" + q: {"USD/2": big.NewInt(100)}, // no EUR/2 → treated as 0
	}}
	eng, res := newTestEngine(t, l)
	spec := mustJSON(t, ParitySpec{Left: ledgerSource("main", q), Right: ledgerSource("control", q)})

	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 outcomes (USD/2, EUR/2), got %d", len(out))
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
		Right:     ledgerSource("control", `{"$match":{"address":"settlement"}}`),
		Tolerance: map[string]int64{"USD/2": 50},
	})
	expr, err := tmpl.Explain(spec)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	for _, want := range []string{"abs(", "ledgerSet(", " - ", "<= 50"} {
		if !strings.Contains(expr, want) {
			t.Errorf("Explain output %q missing %q", expr, want)
		}
	}
	// The Explain output is persisted as rule.compiled_cel and CreateRule
	// sanity-compiles it against the kernel. Guard that it actually parses.
	eng, _ := newTestEngine(t, &fakeLedger{})
	if _, err := eng.Compile(expr); err != nil {
		t.Errorf("Explain output does not compile: %v\nexpr: %s", err, expr)
	}
}
