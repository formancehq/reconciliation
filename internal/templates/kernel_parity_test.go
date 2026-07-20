package templates

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
)

// TestKernelParity_Aggregate guarantees that the source-shaped `compiledCEL`
// retained for explainability matches the authoritative snapshot-value CEL run
// by production templates. The source-shaped version is asserted here against
// deterministic fakes because running it in the hot path would repeat remote
// reads and could diverge for a Payments `latest` source.
//
// For every outcome an aggregate template produces, we compile + evaluate the
// `compiledCEL` it emitted into evidence and require kernel.Passed == outcome.Passed.
// This exercises the kernel builtins (ledgerSet, pool, balance, abs, unary minus)
// AND the template renderers, so any drift between the two implementations of an
// invariant fails CI instead of shipping.
func TestKernelParity_Aggregate(t *testing.T) {
	t.Parallel()

	drift := func(s DriftSpec) any { return s }
	parity := func(s ParitySpec) any { return s }
	threshold := func(s ThresholdSpec) any { return s }
	invariant := func(s InvariantSpec) any { return s }

	i64 := func(v int64) *int64 { return &v }

	cases := []struct {
		name  string
		tmpl  Evaluator
		spec  any
		fakeL *fakeLedger
		fakeP *fakePayments
	}{
		{
			name: "drift/mixed-pass-fail",
			tmpl: NewLedgerVsPoolDrift(),
			spec: drift(DriftSpec{Ledger: "b", LedgerQuery: []byte(`"q"`), PaymentsPoolID: "pool"}),
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`b|"q"`: {"USD/2": big.NewInt(350), "EUR/2": big.NewInt(100)},
			}},
			fakeP: &fakePayments{pools: map[string]map[string]*big.Int{
				"pool": {"USD/2": big.NewInt(-350), "EUR/2": big.NewInt(-50)}, // EUR drifts 50
			}},
		},
		{
			name: "drift/ledgerSign-negative",
			tmpl: NewLedgerVsPoolDrift(),
			spec: drift(DriftSpec{Ledger: "b", LedgerQuery: []byte(`"q"`), PaymentsPoolID: "pool", LedgerSign: -1}),
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`b|"q"`: {"USD/2": big.NewInt(350)},
			}},
			fakeP: &fakePayments{pools: map[string]map[string]*big.Int{
				"pool": {"USD/2": big.NewInt(350)}, // both positive → sign -1 reconciles
			}},
		},
		{
			name: "drift/within-tolerance",
			tmpl: NewLedgerVsPoolDrift(),
			spec: drift(DriftSpec{Ledger: "b", LedgerQuery: []byte(`"q"`), PaymentsPoolID: "pool", Tolerance: map[string]int64{"USD/2": 10}}),
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`b|"q"`: {"USD/2": big.NewInt(100)},
			}},
			fakeP: &fakePayments{pools: map[string]map[string]*big.Int{
				"pool": {"USD/2": big.NewInt(-95)}, // drift 5 <= 10
			}},
		},
		{
			name: "parity/ledger-vs-pool-asset-union",
			tmpl: NewSourceParity(),
			spec: parity(ParitySpec{
				Left:  SourceSpec{Kind: SourceLedger, Ledger: "main", Query: []byte(`{}`)},
				Right: SourceSpec{Kind: SourcePaymentsPool, PoolID: "acct"},
			}),
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`main|{}`: {"USD/2": big.NewInt(100), "EUR/2": big.NewInt(50)},
			}},
			fakeP: &fakePayments{pools: map[string]map[string]*big.Int{
				"acct": {"USD/2": big.NewInt(100)}, // EUR missing → 0, so EUR fails
			}},
		},
		{
			name: "parity/ledger-vs-ledger-within-tolerance",
			tmpl: NewSourceParity(),
			spec: parity(ParitySpec{
				Left:      SourceSpec{Kind: SourceLedger, Ledger: "sub", Query: []byte(`"x"`)},
				Right:     SourceSpec{Kind: SourceLedger, Ledger: "control", Query: []byte(`"x"`)},
				Tolerance: map[string]int64{"USD/2": 5},
			}),
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`sub|"x"`:     {"USD/2": big.NewInt(100)},
				`control|"x"`: {"USD/2": big.NewInt(103)}, // diff 3 <= 5
			}},
			fakeP: &fakePayments{},
		},
		{
			name: "threshold/in-and-out-of-bounds",
			tmpl: NewAccountThreshold(),
			spec: threshold(ThresholdSpec{
				Ledger: "l", Query: []byte(`"q"`), Mode: ThresholdAggregate,
				Bounds: map[string]ThresholdBounds{
					"USD/2": {Min: i64(100), Max: i64(1000)}, // 500 in range → pass
					"EUR/2": {Max: i64(10)},                  // 50 > 10 → fail
					"GBP/2": {Min: i64(100)},                 // 50 < 100 → fail
				},
			}),
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`l|"q"`: {"USD/2": big.NewInt(500), "EUR/2": big.NewInt(50), "GBP/2": big.NewInt(50)},
			}},
			fakeP: &fakePayments{},
		},
		{
			name: "invariant/sum-to-zero-and-breach",
			tmpl: NewLedgerInvariant(),
			spec: invariant(InvariantSpec{
				Terms: []InvariantTerm{
					{Ledger: "buildr", Query: []byte(`"held"`), Sign: 1},
					{Ledger: "buildr", Query: []byte(`"obligation"`), Sign: -1},
				},
				Tolerance: map[string]int64{"USD/2": 0, "EUR/2": 0},
			}),
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`buildr|"held"`:       {"USD/2": big.NewInt(350), "EUR/2": big.NewInt(200)},
				`buildr|"obligation"`: {"USD/2": big.NewInt(350), "EUR/2": big.NewInt(190)}, // EUR nets to 10 → fail
			}},
			fakeP: &fakePayments{},
		},
	}

	// Accumulated across all cases: the suite as a whole must exercise both
	// verdicts, so the parity check is meaningful in both directions (a template
	// that hard-coded Passed=true would still pass a fail-only suite).
	var sawPass, sawFail bool
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng, res := newTestEngine(t, c.fakeL, c.fakeP)
			in := engine.EvalInput{PIT: time.Now().UTC()}

			out, err := c.tmpl.Evaluate(context.Background(), mustJSON(t, c.spec), eng, res, in)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if len(out.Outcomes) == 0 {
				t.Fatal("no outcomes produced — nothing to cross-check")
			}
			if out.CostUnits == 0 {
				t.Fatal("template verdicts did not consume CEL runtime cost")
			}

			for _, o := range out.Outcomes {
				celExpr, ok := o.Evidence["compiledCEL"].(string)
				if !ok || celExpr == "" {
					t.Fatalf("outcome %s has no compiledCEL evidence", o.Fingerprint)
				}
				compiled, err := eng.Compile(celExpr)
				if err != nil {
					t.Fatalf("outcome %s: compiledCEL did not compile: %v\n%s", o.Fingerprint, err, celExpr)
				}
				evalOut, err := eng.Evaluate(context.Background(), compiled, in)
				if err != nil {
					t.Fatalf("outcome %s: kernel eval of compiledCEL failed: %v\n%s", o.Fingerprint, err, celExpr)
				}
				if evalOut.Passed != o.Passed {
					t.Errorf("source/snapshot CEL divergence on %s: source=%v snapshot=%v\ncel=%s",
						o.Fingerprint, evalOut.Passed, o.Passed, celExpr)
				}
				if o.Passed {
					sawPass = true
				} else {
					sawFail = true
				}
			}
		})
	}
	if !sawPass || !sawFail {
		t.Errorf("suite must cover both a pass and a fail across its cases (sawPass=%v sawFail=%v)", sawPass, sawFail)
	}
}

// TestKernelParity_PitPerSource guards the other half of what the inline kernel
// call used to supply: the PitPerSource map. It must be keyed by stable, unique
// source key ("<label>#<idx>") and record the margin-adjusted PIT for every
// source the template read.
func TestKernelParity_PitPerSource(t *testing.T) {
	t.Parallel()
	tmpl := NewSourceParity()
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`main|{}`: {"USD/2": big.NewInt(100)},
	}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{"acct": {"USD/2": big.NewInt(100)}}}
	eng, res := newTestEngine(t, l, p)

	const margin = 30 * time.Second
	pit := time.Now().UTC()
	out, err := tmpl.Evaluate(context.Background(), mustJSON(t, ParitySpec{
		Left:  SourceSpec{Kind: SourceLedger, Ledger: "main", Query: []byte(`{}`)},
		Right: SourceSpec{Kind: SourcePaymentsPool, PoolID: "acct"},
	}), eng, res, engine.EvalInput{PIT: pit, SafetyMargin: margin})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(out.Outcomes) == 0 {
		t.Fatal("no outcomes")
	}
	pps := out.PitPerSource
	want := pit.Add(-margin)
	if got := pps["ledger:main#0"]; !got.Equal(want) {
		t.Errorf("ledger PIT = %s, want margin-adjusted %s", got, want)
	}
	if got := pps["pool:acct#0"]; got.Before(pit) {
		t.Errorf("latest pool observation time = %s, want at or after evaluation start %s", got, pit)
	}
	if len(pps) != 2 {
		t.Errorf("expected exactly 2 source entries, got %d: %v", len(pps), pps)
	}
}
