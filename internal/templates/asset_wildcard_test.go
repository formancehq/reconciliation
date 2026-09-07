package templates

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
)

// A V1 rule carries a map of per-asset tolerances and emits one outcome per
// asset. Expressing that in the named-source model used to mean one rule per
// asset; the asset wildcard closes that gap, which is what makes the V1
// catalogue migratable rather than merely re-implementable.
func TestAssetWildcard_FansOutPerAsset(t *testing.T) {
	t.Parallel()

	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"book|{}": {
			"USD/2": big.NewInt(1000),
			"EUR/2": big.NewInt(250),
		},
		"mirror|{}": {
			"USD/2": big.NewInt(1000),
			"EUR/2": big.NewInt(200), // 50 short: this asset alone breaks
		},
	}}
	eng, resolvers := newTestEngine(t, ledger)

	spec := mustJSON(t, BalanceEquationSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("book", "book", `{}`, AssetWildcard),
			v2LedgerSource("mirror", "mirror", `{}`, AssetWildcard),
		},
		Terms:     []BalanceEquationTerm{{Source: "book", Coefficient: 1}, {Source: "mirror", Coefficient: -1}},
		Tolerance: "0",
	})
	if err := NewBalanceEquation().Validate(spec); err != nil {
		t.Fatalf("a wildcard spec must validate: %v", err)
	}

	outcomes, err := NewBalanceEquation().Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("expected one outcome per asset, got %d: %+v", len(outcomes), outcomes)
	}

	// Lexicographic asset order keeps outcomes stable across runs.
	eur, usd := outcomes[0], outcomes[1]
	if eur.Fingerprint != "asset:EUR/2" || usd.Fingerprint != "asset:USD/2" {
		t.Fatalf("unexpected fingerprints: %q, %q", eur.Fingerprint, usd.Fingerprint)
	}
	if eur.Passed {
		t.Error("EUR/2 is 50 short and must fail")
	}
	if !usd.Passed {
		t.Error("USD/2 balances and must pass")
	}
	if got := eur.Evidence["residual"]; got != "50" {
		t.Errorf("EUR/2 residual = %v, want 50", got)
	}
	if got := eur.Evidence["asset"]; got != "EUR/2" {
		t.Errorf("evidence should name the concrete asset, got %v", got)
	}
	// The wildcard must not leak into the evidence a human reads.
	sources, _ := eur.Evidence["sources"].([]map[string]any)
	if len(sources) != 2 || sources[0]["asset"] != "EUR/2" {
		t.Errorf("source evidence should carry the concrete asset: %+v", sources)
	}
}

// An asset only one side holds still produces an outcome: absence is an explicit
// zero with present=false, so a one-sided balance fails rather than disappearing.
func TestAssetWildcard_AssetMissingOnOneSource(t *testing.T) {
	t.Parallel()

	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"book|{}":   {"USD/2": big.NewInt(100), "GBP/2": big.NewInt(70)},
		"mirror|{}": {"USD/2": big.NewInt(100)},
	}}
	eng, resolvers := newTestEngine(t, ledger)

	spec := mustJSON(t, BalanceEquationSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("book", "book", `{}`, AssetWildcard),
			v2LedgerSource("mirror", "mirror", `{}`, AssetWildcard),
		},
		Terms:     []BalanceEquationTerm{{Source: "book", Coefficient: 1}, {Source: "mirror", Coefficient: -1}},
		Tolerance: "0",
	})
	outcomes, err := NewBalanceEquation().Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	gbp := findOutcome(outcomes, "asset:GBP/2")
	if gbp == nil {
		t.Fatalf("an asset held by one source must still be checked: %+v", outcomes)
	}
	if gbp.Passed {
		t.Error("70 against a missing counterpart must fail")
	}
	sources, _ := gbp.Evidence["sources"].([]map[string]any)
	if len(sources) != 2 || sources[1]["present"] != false || sources[1]["balance"] != "0" {
		t.Errorf("the missing side should read as an explicit absent zero: %+v", sources)
	}
}

func TestAssetWildcard_Validation(t *testing.T) {
	t.Parallel()

	t.Run("mixing wildcard and named assets is rejected", func(t *testing.T) {
		spec := mustJSON(t, BalanceEquationSpec{
			Sources: []V2NamedSource{
				v2LedgerSource("a", "l", `{}`, AssetWildcard),
				v2LedgerSource("b", "l", `{}`, "USD/2"),
			},
			Terms:     []BalanceEquationTerm{{Source: "a", Coefficient: 1}, {Source: "b", Coefficient: -1}},
			Tolerance: "0",
		})
		err := NewBalanceEquation().Validate(spec)
		if err == nil || !strings.Contains(err.Error(), "no defined alignment") {
			t.Fatalf("expected a mixed spec to be rejected, got %v", err)
		}
	})

	t.Run("a metadata source cannot be a wildcard", func(t *testing.T) {
		spec := mustJSON(t, BalanceEquationSpec{
			Sources: []V2NamedSource{
				v2LedgerSource("a", "l", `{}`, AssetWildcard),
				{
					ID: "b", Kind: SourceAccountMetadata, Ledger: "l",
					Query: json.RawMessage(`{}`), MetadataKey: "reported", Asset: AssetWildcard,
				},
			},
			Terms:     []BalanceEquationTerm{{Source: "a", Coefficient: 1}, {Source: "b", Coefficient: -1}},
			Tolerance: "0",
		})
		err := NewBalanceEquation().Validate(spec)
		if err == nil || !strings.Contains(err.Error(), "declares the one asset its key represents") {
			t.Fatalf("expected a metadata wildcard to be rejected, got %v", err)
		}
	})
}

// The convergence claim, checked rather than asserted: a V1 source_parity rule
// carrying a per-asset tolerance map and the single V2 balance_equation that
// replaces it must produce the same outcomes against the same ledger. Until the
// wildcard existed, the V2 side needed one rule per asset.
func TestAssetWildcard_MatchesV1MultiAssetParity(t *testing.T) {
	t.Parallel()

	balances := map[string]map[string]*big.Int{
		"sub|{}": {
			"USD/2": big.NewInt(5000),
			"EUR/2": big.NewInt(300),
			"GBP/2": big.NewInt(90),
		},
		"control|{}": {
			"USD/2": big.NewInt(5000),
			"EUR/2": big.NewInt(280), // outside the zero tolerance
			"GBP/2": big.NewInt(90),
		},
	}

	v1Engine, v1Resolvers := newTestEngine(t, &fakeLedger{balances: balances})
	v1 := mustJSON(t, ParitySpec{
		Left:      SourceSpec{Ledger: "sub", Query: json.RawMessage(`{}`)},
		Right:     SourceSpec{Ledger: "control", Query: json.RawMessage(`{}`)},
		Tolerance: map[string]int64{"USD/2": 0, "EUR/2": 0, "GBP/2": 0},
	})
	v1Outcomes, err := NewSourceParity().Evaluate(context.Background(), v1, v1Engine, v1Resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("V1 Evaluate: %v", err)
	}

	v2Engine, v2Resolvers := newTestEngine(t, &fakeLedger{balances: balances})
	v2 := mustJSON(t, BalanceEquationSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("sub", "sub", `{}`, AssetWildcard),
			v2LedgerSource("control", "control", `{}`, AssetWildcard),
		},
		Terms:     []BalanceEquationTerm{{Source: "sub", Coefficient: 1}, {Source: "control", Coefficient: -1}},
		Tolerance: "0",
	})
	v2Outcomes, err := NewBalanceEquation().Evaluate(context.Background(), v2, v2Engine, v2Resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("V2 Evaluate: %v", err)
	}

	if len(v1Outcomes) != len(v2Outcomes) {
		t.Fatalf("outcome counts differ: V1 %d, V2 %d", len(v1Outcomes), len(v2Outcomes))
	}
	for _, want := range v1Outcomes {
		got := findOutcome(v2Outcomes, want.Fingerprint)
		if got == nil {
			t.Fatalf("V2 produced no outcome for %s", want.Fingerprint)
		}
		if got.Passed != want.Passed {
			t.Errorf("%s: V1 passed=%v, V2 passed=%v", want.Fingerprint, want.Passed, got.Passed)
		}
	}
}

// evidence.compiledCEL is meant to be an exact record of the predicate that ran
// for THAT outcome. Under a wildcard the spec's own sources all read "*", so a
// naive render gives every asset the same expression and records nothing.
func TestAssetWildcard_RendersTheConcreteAssetInCEL(t *testing.T) {
	t.Parallel()

	balances := map[string]map[string]*big.Int{
		"a|{}": {"USD/2": big.NewInt(10), "EUR/2": big.NewInt(5)},
		"b|{}": {"USD/2": big.NewInt(10), "EUR/2": big.NewInt(5)},
	}
	wildcardSourcePair := []V2NamedSource{
		v2LedgerSource("a", "a", `{}`, AssetWildcard),
		v2LedgerSource("b", "b", `{}`, AssetWildcard),
	}

	specs := map[string]json.RawMessage{
		"balance_equation": mustJSON(t, BalanceEquationSpec{
			Sources:   wildcardSourcePair,
			Terms:     []BalanceEquationTerm{{Source: "a", Coefficient: 1}, {Source: "b", Coefficient: -1}},
			Tolerance: "0",
		}),
		"source_consensus": mustJSON(t, SourceConsensusSpec{
			Sources: wildcardSourcePair, Tolerance: "0",
		}),
		"coverage_ratio_bounds": mustJSON(t, CoverageRatioBoundsSpec{
			Sources:          wildcardSourcePair,
			NumeratorTerms:   []BalanceEquationTerm{{Source: "a", Coefficient: 1}},
			DenominatorTerms: []BalanceEquationTerm{{Source: "b", Coefficient: 1}},
			Ratio:            RateConstraint{Min: "0.5", Max: "2"},
		}),
	}
	evaluators := map[string]Evaluator{
		"balance_equation":      NewBalanceEquation(),
		"source_consensus":      NewSourceConsensus(),
		"coverage_ratio_bounds": NewCoverageRatioBounds(),
	}

	for kind, spec := range specs {
		t.Run(kind, func(t *testing.T) {
			eng, resolvers := newTestEngine(t, &fakeLedger{balances: balances})
			outcomes, err := evaluators[kind].Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if len(outcomes) != 2 {
				t.Fatalf("expected one outcome per asset, got %d", len(outcomes))
			}
			seen := map[string]string{}
			for _, outcome := range outcomes {
				cel, _ := outcome.Evidence["compiledCEL"].(string)
				if strings.Contains(cel, `"*"`) {
					t.Errorf("%s renders the wildcard instead of its asset:\n%s", outcome.Fingerprint, cel)
				}
				asset, _ := outcome.Evidence["asset"].(string)
				if !strings.Contains(cel, asset) {
					t.Errorf("%s should name %s in its expression:\n%s", outcome.Fingerprint, asset, cel)
				}
				seen[outcome.Fingerprint] = cel
			}
			if seen["asset:EUR/2"] == seen["asset:USD/2"] {
				t.Error("two assets must not share one expression — it would record nothing")
			}
		})
	}
}
