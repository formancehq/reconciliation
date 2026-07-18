package templates

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
)

func intPtr(v int) *int { return &v }

func TestExchangeRateBounds_ValidateRateShapes(t *testing.T) {
	t.Parallel()
	base := []V2NamedSource{
		v2LedgerSource("eur", "books", `{}`, "EUR/2"),
		v2LedgerSource("usd", "bank", `{}`, "USD/6"),
	}
	valid := []RateConstraint{
		{Min: "1.0773", Max: "1.0827"},
		{Target: "1.0800", ToleranceBps: intPtr(25)},
	}
	for _, rate := range valid {
		spec := ExchangeRateBoundsSpec{Sources: base, BaseSource: "eur", QuoteSource: "usd", Rate: rate}
		if err := NewExchangeRateBounds().Validate(mustJSON(t, spec)); err != nil {
			t.Errorf("valid rate %+v rejected: %v", rate, err)
		}
	}
	bad := []RateConstraint{
		{},
		{Min: "1.1"},
		{Min: "1.2", Max: "1.1"},
		{Target: "1.08"},
		{Target: "1.08", ToleranceBps: intPtr(25), Min: "1", Max: "2"},
		{Target: "1e3", ToleranceBps: intPtr(25)},
		{Target: "1.08", ToleranceBps: intPtr(10_001)},
	}
	for _, rate := range bad {
		spec := ExchangeRateBoundsSpec{Sources: base, BaseSource: "eur", QuoteSource: "usd", Rate: rate}
		if err := NewExchangeRateBounds().Validate(mustJSON(t, spec)); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("rate %+v: expected ErrInvalidSpec, got %v", rate, err)
		}
	}
}

func TestExchangeRateBounds_ExactSignedCELMatchesDirectAtFullToleranceAndMaxTarget(t *testing.T) {
	t.Parallel()
	const qBase = `{"$match":{"address":"eur"}}`
	const qQuote = `{"$match":{"address":"usd"}}`
	scale, _ := new(big.Int).SetString("100000000000000000000", 10)
	base := new(big.Int).Mul(big.NewInt(-10_000), scale)       // -100 EUR
	quote := new(big.Int).Mul(big.NewInt(-108_000_000), scale) // -108 USD
	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"books|" + qBase: {"EUR/2": base},
		"bank|" + qQuote: {"USD/6": quote},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := ExchangeRateBoundsSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("eur", "books", qBase, "EUR/2"),
			v2LedgerSource("usd", "bank", qQuote, "USD/6"),
		},
		BaseSource: "eur", QuoteSource: "usd",
		Rate: RateConstraint{Target: strings.Repeat("9", 78), ToleranceBps: intPtr(10_000)},
	}
	raw := mustJSON(t, spec)
	tmpl := NewExchangeRateBounds()
	outcomes, err := tmpl.Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("direct Evaluate: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Passed {
		t.Fatalf("direct verdict = %+v, want pass", outcomes)
	}
	rate := outcomes[0].Evidence["observedRate"].(map[string]any)
	if rate["numerator"] != "27" || rate["denominator"] != "25" {
		t.Fatalf("observed rate = %+v, want 27/25", rate)
	}
	if _, found := outcomes[0].Evidence["rightBalance"]; found {
		t.Fatal("V2 evidence must not contain left/right fields")
	}

	expression, err := tmpl.Explain(raw)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	compiled, err := eng.Compile(expression)
	if err != nil {
		t.Fatalf("Compile exact expression: %v\n%s", err, expression)
	}
	celOut, err := eng.Evaluate(context.Background(), compiled, engine.EvalInput{})
	if err != nil {
		t.Fatalf("CEL Evaluate: %v", err)
	}
	if celOut.Passed != outcomes[0].Passed {
		t.Fatalf("CEL verdict %v != direct verdict %v", celOut.Passed, outcomes[0].Passed)
	}
}

func TestExchangeRateBounds_BaseZeroIsUndefinedFailure(t *testing.T) {
	t.Parallel()
	const q = `{}`
	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"books|" + q: {"EUR/2": big.NewInt(0)},
		"bank|" + q:  {"USD/2": big.NewInt(0)},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := ExchangeRateBoundsSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("eur", "books", q, "EUR/2"),
			v2LedgerSource("usd", "bank", q, "USD/2"),
		},
		BaseSource: "eur", QuoteSource: "usd",
		Rate: RateConstraint{Min: "1", Max: "2"},
	}
	raw := mustJSON(t, spec)
	tmpl := NewExchangeRateBounds()
	outcomes, err := tmpl.Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("direct Evaluate: %v", err)
	}
	if outcomes[0].Passed || outcomes[0].Evidence["undefinedReason"] != "base_balance_zero" {
		t.Fatalf("outcome = %+v, want undefined failure", outcomes[0])
	}
	expression, _ := tmpl.Explain(raw)
	compiled, err := eng.Compile(expression)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	celOut, err := eng.Evaluate(context.Background(), compiled, engine.EvalInput{})
	if err != nil {
		t.Fatalf("CEL Evaluate: %v", err)
	}
	if celOut.Passed {
		t.Fatal("CEL verdict = pass, want base-zero failure")
	}
}
