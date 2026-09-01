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

func v2LedgerSource(id, ledger, query, asset string) V2NamedSource {
	return V2NamedSource{ID: id, Ledger: ledger, Query: json.RawMessage(query), Asset: asset}
}

func TestBalanceEquation_Validate(t *testing.T) {
	t.Parallel()
	tmpl := NewBalanceEquation()
	valid := BalanceEquationSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("a", "ledger", `{}`, "USD/2"),
			v2LedgerSource("b", "ledger", `{}`, "USD/2"),
		},
		Terms:     []BalanceEquationTerm{{Source: "a", Coefficient: 1}, {Source: "b", Coefficient: -1}},
		Tolerance: "0",
	}
	if err := tmpl.Validate(mustJSON(t, valid)); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}

	cases := map[string]BalanceEquationSpec{
		"duplicate source ID": func() BalanceEquationSpec {
			s := valid
			s.Sources = append([]V2NamedSource(nil), valid.Sources...)
			s.Sources[1].ID = "a"
			return s
		}(),
		"invalid source ID": func() BalanceEquationSpec {
			s := valid
			s.Sources = append([]V2NamedSource(nil), valid.Sources...)
			s.Sources[0].ID = "a|bad"
			return s
		}(),
		"different assets": func() BalanceEquationSpec {
			s := valid
			s.Sources = append([]V2NamedSource(nil), valid.Sources...)
			s.Sources[1].Asset = "EUR/2"
			return s
		}(),
		"zero coefficient": func() BalanceEquationSpec {
			s := valid
			s.Terms = append([]BalanceEquationTerm(nil), valid.Terms...)
			s.Terms[0].Coefficient = 0
			return s
		}(),
		"negative tolerance": func() BalanceEquationSpec {
			s := valid
			s.Tolerance = "-1"
			return s
		}(),
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := tmpl.Validate(mustJSON(t, spec)); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

func TestBalanceEquation_ExactBigIntCELMatchesDirect(t *testing.T) {
	t.Parallel()
	const qA = `{"$match":{"address":"a"}}`
	const qB = `{"$match":{"address":"b"}}`
	const qC = `{"$match":{"address":"c"}}`
	a, _ := new(big.Int).SetString("1000000000000000000000000000007", 10)
	b, _ := new(big.Int).SetString("2000000000000000000000000000011", 10)
	c := new(big.Int).Add(new(big.Int).Set(a), b)
	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"books|" + qA: {"USD/2": a},
		"books|" + qB: {"USD/2": b},
		"books|" + qC: {"USD/2": c},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := BalanceEquationSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("a", "books", qA, "USD/2"),
			v2LedgerSource("b", "books", qB, "USD/2"),
			v2LedgerSource("c", "books", qC, "USD/2"),
		},
		Terms: []BalanceEquationTerm{
			{Source: "a", Coefficient: 1},
			{Source: "b", Coefficient: 1},
			{Source: "c", Coefficient: -1},
		},
		Tolerance: "0",
	}
	raw := mustJSON(t, spec)
	tmpl := NewBalanceEquation()
	outcomes, err := tmpl.Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("direct Evaluate: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Passed {
		t.Fatalf("direct verdict = %+v, want pass", outcomes)
	}
	if outcomes[0].Evidence["residual"] != "0" {
		t.Fatalf("residual = %v, want 0", outcomes[0].Evidence["residual"])
	}
	if _, found := outcomes[0].Evidence["leftSource"]; found {
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
	celOut, err := eng.Evaluate(context.Background(), compiled, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("CEL Evaluate: %v", err)
	}
	if celOut.Passed != outcomes[0].Passed {
		t.Fatalf("CEL verdict %v != direct verdict %v", celOut.Passed, outcomes[0].Passed)
	}
}

func TestBalanceEquation_MissingLedgerAssetIsExplicitZero(t *testing.T) {
	t.Parallel()
	const q = `{}`
	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"books|" + q: {"USD/2": big.NewInt(0)},
		"empty|" + q: {},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	raw := mustJSON(t, BalanceEquationSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("a", "books", q, "USD/2"),
			v2LedgerSource("b", "empty", q, "USD/2"),
		},
		Terms:     []BalanceEquationTerm{{Source: "a", Coefficient: 1}, {Source: "b", Coefficient: -1}},
		Tolerance: "0",
	})
	outcomes, err := NewBalanceEquation().Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	sources := outcomes[0].Evidence["sources"].([]map[string]any)
	if sources[1]["balance"] != "0" || sources[1]["present"] != false {
		t.Fatalf("missing evidence = %+v", sources[1])
	}
}
