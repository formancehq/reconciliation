package templates

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
)

func boundsSpec(t *testing.T, asset string, bounds map[string]BalanceBound) json.RawMessage {
	t.Helper()
	return mustJSON(t, BalanceBoundsSpec{
		Source: V2NamedSource{
			ID: "treasury", Ledger: "book",
			Query: json.RawMessage(`{}`), Asset: asset,
		},
		Bounds: bounds,
	})
}

func evaluateBounds(t *testing.T, spec json.RawMessage, balances map[string]*big.Int) []Outcome {
	t.Helper()
	eng, resolvers := newTestEngine(t, &fakeLedger{balances: map[string]map[string]*big.Int{"book|{}": balances}})
	outcomes, err := NewBalanceBounds().Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return outcomes
}

// The case the whole design turns on: a floor on an asset the set no longer
// holds must keep FAILING. Routed through the shared per-asset fan-out it would
// vanish from the outcome set, and the service would auto-resolve the alert.
func TestBalanceBounds_FloorHoldsWhenTheSetDrains(t *testing.T) {
	t.Parallel()

	spec := boundsSpec(t, AssetWildcard, map[string]BalanceBound{
		"USD/2": {Min: "100000"},
	})
	outcomes := evaluateBounds(t, spec, map[string]*big.Int{"EUR/2": big.NewInt(999)})

	if len(outcomes) != 1 {
		t.Fatalf("a declared asset must be checked whether or not it is held, got %d outcomes", len(outcomes))
	}
	if outcomes[0].Passed {
		t.Error("a floor of 100000 against a drained set must fail, not disappear")
	}
	source, _ := outcomes[0].Evidence["source"].(map[string]any)
	if source["balance"] != "0" || source["present"] != false {
		t.Errorf("a drained asset should read as an absent zero: %+v", source)
	}
	if got := outcomes[0].Evidence["excursion"]; got != "-100000" {
		t.Errorf("excursion = %v, want -100000", got)
	}
	if got := outcomes[0].Evidence["breachedBound"]; got != "min" {
		t.Errorf("breachedBound = %v, want min", got)
	}
	// An asset the set holds but the rule never declared is not this rule's business.
	if outcomes[0].Evidence["asset"] != "USD/2" {
		t.Errorf("unexpected asset %v", outcomes[0].Evidence["asset"])
	}
}

func TestBalanceBounds_BoundsTableIsTheUniverse(t *testing.T) {
	t.Parallel()

	spec := boundsSpec(t, AssetWildcard, map[string]BalanceBound{
		"EUR/2": {Max: "1000"},
		"USD/2": {Min: "100", Max: "500"},
	})
	outcomes := evaluateBounds(t, spec, map[string]*big.Int{
		"USD/2": big.NewInt(300),
		"EUR/2": big.NewInt(5000), // over
		"GBP/2": big.NewInt(7),    // held but not declared — ignored
	})

	if len(outcomes) != 2 {
		t.Fatalf("expected one outcome per declared asset, got %d", len(outcomes))
	}
	if outcomes[0].Fingerprint != "asset:EUR/2" || outcomes[1].Fingerprint != "asset:USD/2" {
		t.Fatalf("outcomes must be in lexicographic asset order: %q, %q",
			outcomes[0].Fingerprint, outcomes[1].Fingerprint)
	}
	if outcomes[0].Passed {
		t.Error("EUR/2 is over its ceiling")
	}
	if got := outcomes[0].Evidence["excursion"]; got != "4000" {
		t.Errorf("excursion above a ceiling should be positive, got %v", got)
	}
	if !outcomes[1].Passed {
		t.Error("USD/2 sits inside its range")
	}
	if _, breached := outcomes[1].Evidence["breachedBound"]; breached {
		t.Error("a passing outcome must omit breachedBound rather than encode a sentinel")
	}
	if got := outcomes[1].Evidence["excursion"]; got != "0" {
		t.Errorf("excursion inside the range = %v, want 0", got)
	}
}

func TestBalanceBounds_InclusiveAndOneSided(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		bound   BalanceBound
		balance int64
		passed  bool
	}{
		{"exactly the floor passes", BalanceBound{Min: "100"}, 100, true},
		{"a unit under the floor fails", BalanceBound{Min: "100"}, 99, false},
		{"exactly the ceiling passes", BalanceBound{Max: "100"}, 100, true},
		{"a unit over the ceiling fails", BalanceBound{Max: "100"}, 101, false},
		{"unbounded above", BalanceBound{Min: "100"}, 1 << 40, true},
		{"unbounded below", BalanceBound{Max: "100"}, -1 << 40, true},
		{"min equals max is exact equality", BalanceBound{Min: "7", Max: "7"}, 7, true},
		{"min equals max rejects anything else", BalanceBound{Min: "7", Max: "7"}, 8, false},
		{"negative bounds for a liability set", BalanceBound{Min: "-500000", Max: "0"}, -1000, true},
		{"a positive balance breaches a zero ceiling", BalanceBound{Min: "-500000", Max: "0"}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := boundsSpec(t, "USD/2", map[string]BalanceBound{"USD/2": tc.bound})
			outcomes := evaluateBounds(t, spec, map[string]*big.Int{"USD/2": big.NewInt(tc.balance)})
			if len(outcomes) != 1 {
				t.Fatalf("got %d outcomes", len(outcomes))
			}
			if outcomes[0].Passed != tc.passed {
				t.Errorf("passed = %v, want %v (balance %d, bound %+v)",
					outcomes[0].Passed, tc.passed, tc.balance, tc.bound)
			}
		})
	}
}

// The rendered predicate must be executable against today's kernel and name the
// concrete asset — including under a wildcard source.
func TestBalanceBounds_CompiledCEL(t *testing.T) {
	t.Parallel()

	spec := boundsSpec(t, AssetWildcard, map[string]BalanceBound{
		"USD/2": {Min: "100", Max: "500"},
		"EUR/2": {Max: "9"},
	})
	outcomes := evaluateBounds(t, spec, map[string]*big.Int{"USD/2": big.NewInt(300)})

	eng, _ := newTestEngine(t, &fakeLedger{})
	for _, outcome := range outcomes {
		cel, _ := outcome.Evidence["compiledCEL"].(string)
		if strings.Contains(cel, `"*"`) {
			t.Errorf("%s renders the wildcard: %s", outcome.Fingerprint, cel)
		}
		if _, err := eng.Compile(cel); err != nil {
			t.Errorf("%s does not compile against the kernel: %v\n%s", outcome.Fingerprint, err, cel)
		}
	}
	usd := findOutcome(outcomes, "asset:USD/2")
	if cel, _ := usd.Evidence["compiledCEL"].(string); !strings.Contains(cel, ">= 100") || !strings.Contains(cel, "<= 500") {
		t.Errorf("both sides should be rendered: %s", cel)
	}
	eur := findOutcome(outcomes, "asset:EUR/2")
	if cel, _ := eur.Evidence["compiledCEL"].(string); strings.Contains(cel, ">=") {
		t.Errorf("an unbounded side must be omitted, not rendered: %s", cel)
	}

	expression, err := NewBalanceBounds().Explain(spec)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if _, err := eng.Compile(expression); err != nil {
		t.Errorf("Explain output does not compile: %v\n%s", err, expression)
	}
}

func TestBalanceBounds_Validate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		spec    json.RawMessage
		wantErr string
	}{
		{
			name: "wildcard source with several assets",
			spec: boundsSpec(t, AssetWildcard, map[string]BalanceBound{
				"USD/2": {Min: "1"}, "EUR/2": {Max: "2"},
			}),
		},
		{
			name: "named source with its own asset",
			spec: boundsSpec(t, "USD/2", map[string]BalanceBound{"USD/2": {Min: "1"}}),
		},
		{
			name:    "named source bounding a different asset",
			spec:    boundsSpec(t, "USD/2", map[string]BalanceBound{"EUR/2": {Min: "1"}}),
			wantErr: "must hold exactly that one key",
		},
		{
			name: "named source bounding several assets",
			spec: boundsSpec(t, "USD/2", map[string]BalanceBound{
				"USD/2": {Min: "1"}, "EUR/2": {Max: "2"},
			}),
			wantErr: "must hold exactly that one key",
		},
		{
			name:    "empty bounds",
			spec:    boundsSpec(t, "USD/2", map[string]BalanceBound{}),
			wantErr: "at least one asset",
		},
		{
			name:    "neither side set",
			spec:    boundsSpec(t, "USD/2", map[string]BalanceBound{"USD/2": {}}),
			wantErr: "at least one of min or max",
		},
		{
			name:    "min above max",
			spec:    boundsSpec(t, "USD/2", map[string]BalanceBound{"USD/2": {Min: "100", Max: "50"}}),
			wantErr: "must be less than or equal to max",
		},
		{
			name:    "wildcard as a bounds key",
			spec:    boundsSpec(t, AssetWildcard, map[string]BalanceBound{"*": {Min: "1"}}),
			wantErr: "is not a bounds key",
		},
		{
			name:    "bounds key is not an asset code",
			spec:    boundsSpec(t, AssetWildcard, map[string]BalanceBound{"usd": {Min: "1"}}),
			wantErr: "is not a valid asset code",
		},
		{
			name:    "decimal bound",
			spec:    boundsSpec(t, "USD/2", map[string]BalanceBound{"USD/2": {Min: "1.5"}}),
			wantErr: "signed base-10 integer string",
		},
		{
			name:    "leading zero",
			spec:    boundsSpec(t, "USD/2", map[string]BalanceBound{"USD/2": {Min: "007"}}),
			wantErr: "signed base-10 integer string",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := NewBalanceBounds().Validate(tc.spec)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected the spec to validate, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// The convergence claim for this template. These expectations were pinned by
// running the equivalent multi-asset account_threshold rule against the same
// fixture and asserting outcome-for-outcome agreement, before that template was
// retired. The differential test could not outlive its subject; its verdicts
// could, so they are kept here as golden values.
func TestBalanceBounds_MatchesRetiredAccountThreshold(t *testing.T) {
	t.Parallel()

	outcomes := evaluateBounds(t, boundsSpec(t, AssetWildcard, map[string]BalanceBound{
		"USD/2": {Min: "100", Max: "500"},
		"EUR/2": {Max: "1000"},
	}), map[string]*big.Int{"USD/2": big.NewInt(300), "EUR/2": big.NewInt(5000)})

	want := map[string]bool{"asset:EUR/2": false, "asset:USD/2": true}
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
