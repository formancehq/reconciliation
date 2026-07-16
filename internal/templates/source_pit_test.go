package templates

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
)

// The pool balance-window tail means a PIT read at ~now returns empty, so the
// "as of now" default must read the pool via latest (nil pit) while an
// explicitly-requested past instant reads point-in-time. These tests pin that
// gate and the per-source PIT overrides that make each side read a different
// instant (the reconciledAtLedger vs reconciledAtPayments contract).

func TestDrift_PoolReadGate(t *testing.T) {
	t.Parallel()
	const margin = 30 * time.Second
	pit := time.Date(2026, 6, 17, 16, 0, 0, 0, time.UTC)

	spec := DriftSpec{Ledger: "b", LedgerQuery: []byte(`"q"`), PaymentsPoolID: "pool"}
	newFakes := func() (*fakeLedger, *fakePayments) {
		return &fakeLedger{balances: map[string]map[string]*big.Int{
				`b|"q"`: {"USD/2": big.NewInt(-100)},
			}}, &fakePayments{pools: map[string]map[string]*big.Int{
				"pool": {"USD/2": big.NewInt(100)},
			}}
	}

	t.Run("as-of-now default reads pool latest (nil pit)", func(t *testing.T) {
		l, p := newFakes()
		eng, res := newTestEngine(t, l, p)
		_, err := NewLedgerVsPoolDrift().Evaluate(context.Background(), mustJSON(t, spec), eng, res,
			engine.EvalInput{PIT: pit, SafetyMargin: margin}) // PITExplicit false
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		got, ok := p.gotPITs["pool"]
		if !ok {
			t.Fatal("pool was not read")
		}
		if got != nil {
			t.Errorf("pool read point-in-time (%s), want latest (nil) for the as-of-now default", got)
		}
	})

	t.Run("explicit PIT reads pool point-in-time at pit-margin", func(t *testing.T) {
		l, p := newFakes()
		eng, res := newTestEngine(t, l, p)
		_, err := NewLedgerVsPoolDrift().Evaluate(context.Background(), mustJSON(t, spec), eng, res,
			engine.EvalInput{PIT: pit, PITExplicit: true, SafetyMargin: margin})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		got := p.gotPITs["pool"]
		if got == nil {
			t.Fatal("pool read latest, want point-in-time for an explicit PIT")
		}
		if want := pit.Add(-margin); !got.Equal(want) {
			t.Errorf("pool PIT = %s, want margin-adjusted %s", got, want)
		}
	})
}

func TestDrift_PerSourcePITOverride(t *testing.T) {
	t.Parallel()
	const margin = 10 * time.Second
	ledgerAt := time.Date(2026, 6, 17, 16, 0, 0, 0, time.UTC)
	poolAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC) // 4h earlier — the settlement-lag case

	spec := DriftSpec{Ledger: "b", LedgerQuery: []byte(`"q"`), PaymentsPoolID: "pool"}
	l := &fakeLedger{balances: map[string]map[string]*big.Int{`b|"q"`: {"USD/2": big.NewInt(-100)}}}
	p := &fakePayments{pools: map[string]map[string]*big.Int{"pool": {"USD/2": big.NewInt(100)}}}
	eng, res := newTestEngine(t, l, p)

	// Override each side independently, keyed by the template's stable source key.
	_, err := NewLedgerVsPoolDrift().Evaluate(context.Background(), mustJSON(t, spec), eng, res, engine.EvalInput{
		PIT:          time.Now().UTC(),
		SafetyMargin: margin,
		SourcePITs: map[string]time.Time{
			"ledger:b#0":  ledgerAt,
			"pool:pool#0": poolAt,
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if got := l.gotPITs[`b|"q"`]; !got.Equal(ledgerAt.Add(-margin)) {
		t.Errorf("ledger PIT = %s, want %s", got, ledgerAt.Add(-margin))
	}
	poolGot := p.gotPITs["pool"]
	if poolGot == nil {
		t.Fatal("pool read latest, want point-in-time (an override implies an explicit PIT)")
	}
	if want := poolAt.Add(-margin); !poolGot.Equal(want) {
		t.Errorf("pool PIT = %s, want %s", poolGot, want)
	}
}

// Two invariant terms on the same ledger must be addressable — and auditable —
// independently, via the "#idx" key suffix.
func TestInvariant_SameLedgerTermsDistinctPITs(t *testing.T) {
	t.Parallel()
	spec := InvariantSpec{
		Terms: []InvariantTerm{
			{Ledger: "buildr", Query: []byte(`"held"`), Sign: 1},
			{Ledger: "buildr", Query: []byte(`"obligation"`), Sign: -1},
		},
		Tolerance: map[string]int64{"USD/2": 0},
	}
	l := &fakeLedger{balances: map[string]map[string]*big.Int{
		`buildr|"held"`:       {"USD/2": big.NewInt(350)},
		`buildr|"obligation"`: {"USD/2": big.NewInt(350)},
	}}
	eng, res := newTestEngine(t, l, &fakePayments{})

	heldAt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	obligationAt := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	out, err := NewLedgerInvariant().Evaluate(context.Background(), mustJSON(t, spec), eng, res, engine.EvalInput{
		PIT: time.Now().UTC(),
		SourcePITs: map[string]time.Time{
			"ledger:buildr#0": heldAt,       // terms[0]
			"ledger:buildr#1": obligationAt, // terms[1]
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := l.gotPITs[`buildr|"held"`]; !got.Equal(heldAt) {
		t.Errorf("held term PIT = %s, want %s", got, heldAt)
	}
	if got := l.gotPITs[`buildr|"obligation"`]; !got.Equal(obligationAt) {
		t.Errorf("obligation term PIT = %s, want %s", got, obligationAt)
	}
	// Both keys must appear in the audit record.
	pps := out[0].PitPerSource
	for _, k := range []string{"ledger:buildr#0", "ledger:buildr#1"} {
		if _, ok := pps[k]; !ok {
			t.Errorf("pit_per_source missing key %q: %v", k, pps)
		}
	}
}
