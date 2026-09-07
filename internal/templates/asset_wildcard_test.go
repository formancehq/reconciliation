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

// The convergence claim for the wildcard. These expectations were pinned by
// running the equivalent multi-asset source_parity rule against the same
// fixture, before that template was retired: one V2 rule covers what previously
// needed one rule per asset, with the same verdicts.
func TestAssetWildcard_MatchesRetiredMultiAssetParity(t *testing.T) {
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
	eng, resolvers := newTestEngine(t, &fakeLedger{balances: balances})
	spec := mustJSON(t, BalanceEquationSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("sub", "sub", `{}`, AssetWildcard),
			v2LedgerSource("control", "control", `{}`, AssetWildcard),
		},
		Terms:     []BalanceEquationTerm{{Source: "sub", Coefficient: 1}, {Source: "control", Coefficient: -1}},
		Tolerance: "0",
	})
	outcomes, err := NewBalanceEquation().Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	want := map[string]bool{"asset:EUR/2": false, "asset:GBP/2": true, "asset:USD/2": true}
	if len(outcomes) != len(want) {
		t.Fatalf("expected %d outcomes, got %d", len(want), len(outcomes))
	}
	for fingerprint, passed := range want {
		got := findOutcome(outcomes, fingerprint)
		if got == nil {
			t.Fatalf("no outcome for %s", fingerprint)
		}
		if got.Passed != passed {
			t.Errorf("%s: passed = %v, want %v", fingerprint, got.Passed, passed)
		}
	}
}
