package templates

import (
	"context"
	"math/big"
	"sort"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
)

// TestSourceKeys_MatchEvaluate is the anti-drift guard: for every template, the
// keys SourceKeys reports must be exactly the keys Evaluate records in
// pit_per_source. If a template's Evaluate changes how it assigns source keys
// without SourceKeys following (or vice versa), the service's sourcePITs
// validation would reject valid overrides or accept invalid ones — this fails
// CI first.
func TestSourceKeys_MatchEvaluate(t *testing.T) {
	t.Parallel()
	i64 := func(v int64) *int64 { return &v }

	cases := []struct {
		name  string
		tmpl  Evaluator
		spec  any
		fakeL *fakeLedger
		fakeP *fakePayments
		want  []string
	}{
		{
			name:  "drift",
			tmpl:  NewLedgerVsPoolDrift(),
			spec:  DriftSpec{Ledger: "b", LedgerQuery: []byte(`"q"`), PaymentsPoolID: "pool"},
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{`b|"q"`: {"USD/2": big.NewInt(-100)}}},
			fakeP: &fakePayments{pools: map[string]map[string]*big.Int{"pool": {"USD/2": big.NewInt(100)}}},
			want:  []string{"ledger:b#0", "pool:pool#0"},
		},
		{
			name: "parity/same-ledger-distinct-idx",
			tmpl: NewSourceParity(),
			spec: ParitySpec{
				Left:  SourceSpec{Kind: SourceLedger, Ledger: "main", Query: []byte(`"a"`)},
				Right: SourceSpec{Kind: SourceLedger, Ledger: "main", Query: []byte(`"b"`)},
			},
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`main|"a"`: {"USD/2": big.NewInt(100)},
				`main|"b"`: {"USD/2": big.NewInt(100)},
			}},
			fakeP: &fakePayments{},
			want:  []string{"ledger:main#0", "ledger:main#1"},
		},
		{
			name: "invariant/same-ledger-terms",
			tmpl: NewLedgerInvariant(),
			spec: InvariantSpec{
				Terms: []InvariantTerm{
					{Ledger: "buildr", Query: []byte(`"held"`), Sign: 1},
					{Ledger: "buildr", Query: []byte(`"obligation"`), Sign: -1},
				},
				Tolerance: map[string]int64{"USD/2": 0},
			},
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{
				`buildr|"held"`:       {"USD/2": big.NewInt(350)},
				`buildr|"obligation"`: {"USD/2": big.NewInt(350)},
			}},
			fakeP: &fakePayments{},
			want:  []string{"ledger:buildr#0", "ledger:buildr#1"},
		},
		{
			name: "account_threshold",
			tmpl: NewAccountThreshold(),
			spec: ThresholdSpec{
				Ledger: "l", Query: []byte(`"q"`), Mode: ThresholdAggregate,
				Bounds: map[string]ThresholdBounds{"USD/2": {Min: i64(0), Max: i64(1000)}},
			},
			fakeL: &fakeLedger{balances: map[string]map[string]*big.Int{`l|"q"`: {"USD/2": big.NewInt(500)}}},
			fakeP: &fakePayments{},
			want:  []string{"ledger:l#0"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eng, res := newTestEngine(t, c.fakeL, c.fakeP)
			specJSON := mustJSON(t, c.spec)

			gotKeys, err := c.tmpl.SourceKeys(specJSON)
			if err != nil {
				t.Fatalf("SourceKeys: %v", err)
			}
			if got := sortedCopy(gotKeys); !equalStrings(got, c.want) {
				t.Errorf("SourceKeys = %v, want %v", got, c.want)
			}

			// The keys Evaluate actually records must be the same set.
			out, err := c.tmpl.Evaluate(context.Background(), specJSON, eng, res, engine.EvalInput{PIT: time.Now().UTC()})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if len(out) == 0 {
				t.Fatal("no outcomes — cannot cross-check pit_per_source keys")
			}
			seen := map[string]struct{}{}
			for _, o := range out {
				for k := range o.PitPerSource {
					seen[k] = struct{}{}
				}
			}
			evalKeys := make([]string, 0, len(seen))
			for k := range seen {
				evalKeys = append(evalKeys, k)
			}
			if got := sortedCopy(evalKeys); !equalStrings(got, c.want) {
				t.Errorf("Evaluate pit_per_source keys = %v, want %v (SourceKeys must match)", got, c.want)
			}
		})
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
