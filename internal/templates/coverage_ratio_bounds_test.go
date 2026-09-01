package templates

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
)

func TestCoverageRatioBounds_Validate(t *testing.T) {
	t.Parallel()
	valid := CoverageRatioBoundsSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("cash", "books", `{}`, "USD/2"),
			v2LedgerSource("securities", "books", `{}`, "USD/2"),
			v2LedgerSource("liabilities", "books", `{}`, "USD/2"),
		},
		NumeratorTerms:   []BalanceEquationTerm{{Source: "cash", Coefficient: 1}, {Source: "securities", Coefficient: 1}},
		DenominatorTerms: []BalanceEquationTerm{{Source: "liabilities", Coefficient: 1}},
		Ratio:            RateConstraint{Min: "1", Max: "1.25"},
	}
	requireValidSpec(t, NewCoverageRatioBounds(), valid)

	cases := map[string]CoverageRatioBoundsSpec{
		"source reused": func() CoverageRatioBoundsSpec {
			s := valid
			s.DenominatorTerms = []BalanceEquationTerm{{Source: "cash", Coefficient: 1}}
			return s
		}(),
		"source unused": func() CoverageRatioBoundsSpec {
			s := valid
			s.NumeratorTerms = []BalanceEquationTerm{{Source: "cash", Coefficient: 1}}
			return s
		}(),
		"empty numerator": func() CoverageRatioBoundsSpec {
			s := valid
			s.NumeratorTerms = nil
			return s
		}(),
		"different assets": func() CoverageRatioBoundsSpec {
			s := valid
			s.Sources = append([]V2NamedSource(nil), valid.Sources...)
			s.Sources[1].Asset = "EUR/2"
			return s
		}(),
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := NewCoverageRatioBounds().Validate(mustJSON(t, spec)); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

func TestCoverageRatioBounds_ExactPortfoliosMatchCELAtFullTolerance(t *testing.T) {
	t.Parallel()
	const (
		qCash = `{"$match":{"address":"cash"}}`
		qSec  = `{"$match":{"address":"securities"}}`
		qLiab = `{"$match":{"address":"liabilities"}}`
	)
	scale, _ := new(big.Int).SetString("1000000000000000000000000", 10)
	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"books|" + qCash: {"USD/2": new(big.Int).Mul(big.NewInt(60), scale)},
		"books|" + qSec:  {"USD/2": new(big.Int).Mul(big.NewInt(40), scale)},
		"books|" + qLiab: {"USD/2": new(big.Int).Mul(big.NewInt(80), scale)},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := CoverageRatioBoundsSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("cash", "books", qCash, "USD/2"),
			v2LedgerSource("securities", "books", qSec, "USD/2"),
			v2LedgerSource("liabilities", "books", qLiab, "USD/2"),
		},
		NumeratorTerms:   []BalanceEquationTerm{{Source: "cash", Coefficient: 1}, {Source: "securities", Coefficient: 1}},
		DenominatorTerms: []BalanceEquationTerm{{Source: "liabilities", Coefficient: 1}},
		Ratio:            RateConstraint{Target: "1.25", ToleranceBps: intPtr(10_000)},
	}
	raw := mustJSON(t, spec)
	tmpl := NewCoverageRatioBounds()
	outcomes, err := tmpl.Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Passed {
		t.Fatalf("outcomes = %+v, want pass", outcomes)
	}
	evidence := outcomes[0].Evidence
	ratio := evidence["observedRatio"].(map[string]any)
	if ratio["numerator"] != "5" || ratio["denominator"] != "4" {
		t.Fatalf("observedRatio = %+v", ratio)
	}
	assertTemplateCELMatches(t, tmpl, raw, eng, outcomes[0].Passed)
}

func TestCoverageRatioBounds_ZeroDenominatorIsFailure(t *testing.T) {
	t.Parallel()
	const q = `{}`
	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"assets|" + q:      {"USD/2": big.NewInt(100)},
		"liabilities|" + q: {"USD/2": big.NewInt(0)},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := CoverageRatioBoundsSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("assets", "assets", q, "USD/2"),
			v2LedgerSource("liabilities", "liabilities", q, "USD/2"),
		},
		NumeratorTerms:   []BalanceEquationTerm{{Source: "assets", Coefficient: 1}},
		DenominatorTerms: []BalanceEquationTerm{{Source: "liabilities", Coefficient: 1}},
		Ratio:            RateConstraint{Min: "1", Max: "2"},
	}
	raw := mustJSON(t, spec)
	tmpl := NewCoverageRatioBounds()
	outcomes, err := tmpl.Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcomes[0].Passed || outcomes[0].Evidence["undefinedReason"] != "denominator_total_zero" {
		t.Fatalf("outcome = %+v", outcomes[0])
	}
	assertTemplateCELMatches(t, tmpl, raw, eng, outcomes[0].Passed)
}
