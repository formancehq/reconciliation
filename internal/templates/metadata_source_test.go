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

func metadataSource(ledger, query, key, asset string) SourceSpec {
	return SourceSpec{
		Kind:        SourceAccountMetadata,
		Ledger:      ledger,
		Query:       json.RawMessage(query),
		MetadataKey: key,
		Asset:       asset,
	}
}

func TestSourceSpec_Validate_AccountMetadata(t *testing.T) {
	cases := map[string]SourceSpec{
		"missing metadataKey": {Kind: SourceAccountMetadata, Ledger: "l", Query: json.RawMessage(`{}`), Asset: "USD/2"},
		"missing asset":       {Kind: SourceAccountMetadata, Ledger: "l", Query: json.RawMessage(`{}`), MetadataKey: "ext"},
		"unknown kind":        {Kind: "bank", Ledger: "l", Query: json.RawMessage(`{}`)},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.Validate("src"); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
	ok := metadataSource("l", `{"$match":{"address":"mirror:x"}}`, "ext", "USD/2")
	if err := ok.Validate("src"); err != nil {
		t.Errorf("valid account_metadata source rejected: %v", err)
	}
}

// TestSourceParity_LedgerVsMetadata is the headline: a ledger balance reconciled
// against an externally-synced value stored in account metadata (a mirror
// account). PASS when they agree, FAIL when the synced value drifts.
func TestSourceParity_LedgerVsMetadata(t *testing.T) {
	tmpl := NewSourceParity()
	const bookQ = `{"$match":{"address":"cash:stripe"}}`
	const mirrorQ = `{"$match":{"address":"mirror:stripe"}}`

	l := &fakeLedger{
		balances: map[string]map[string]*big.Int{
			"book|" + bookQ: {"USD/2": big.NewInt(35000)}, // real posting-derived balance
		},
		accounts: map[string][]engine.Account{
			"mirror|" + mirrorQ: {{
				Address:  "mirror:stripe",
				Metadata: map[string]string{"ext_balance": "35000"}, // synced snapshot
			}},
		},
	}
	eng, res := newTestEngine(t, l)

	spec := mustJSON(t, ParitySpec{
		Left:  ledgerSource("book", bookQ),
		Right: metadataSource("mirror", mirrorQ, "ext_balance", "USD/2"),
	})
	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || !o.Passed {
		t.Fatalf("expected PASS (35000 == synced 35000), got %+v", out)
	}
	if o.Evidence["rightBalance"] != "35000" {
		t.Errorf("expected rightBalance from metadata = 35000, got %v", o.Evidence["rightBalance"])
	}

	// Drift the synced value → mismatch fails at tolerance 0.
	l.accounts["mirror|"+mirrorQ][0].Metadata["ext_balance"] = "34000"
	out, err = tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate (drift): %v", err)
	}
	if o := findOutcome(out, "asset:USD/2"); o == nil || o.Passed {
		t.Fatalf("expected FAIL after sync drift (35000 vs 34000), got %+v", out)
	}
}

// TestSourceParity_MetadataExplainCompiles guards that a metadata rule's
// rendered compiled_cel type-checks against the kernel (CreateRule compiles it).
func TestSourceParity_MetadataExplainCompiles(t *testing.T) {
	tmpl := NewSourceParity()
	spec := mustJSON(t, ParitySpec{
		Left:      ledgerSource("book", `{"$match":{"address":"cash:stripe"}}`),
		Right:     metadataSource("mirror", `{"$match":{"address":"mirror:stripe"}}`, "ext_balance", "USD/2"),
		Tolerance: map[string]int64{"USD/2": 0},
	})
	expr, err := tmpl.Explain(spec)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	eng, _ := newTestEngine(t, &fakeLedger{})
	if _, err := eng.Compile(expr); err != nil {
		t.Errorf("metadata Explain output does not compile: %v\nexpr: %s", err, expr)
	}
}

func TestSourceParity_MetadataRejectedInPerAccount(t *testing.T) {
	tmpl := NewSourceParity()
	spec := mustJSON(t, ParitySpec{
		Left:  ledgerSource("a", `{}`),
		Right: metadataSource("mirror", `{"$match":{"address":"mirror:x"}}`, "ext", "USD/2"),
		Scope: ScopePerAccount,
	})
	if err := tmpl.Validate(spec); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec (metadata source can't be per_account), got %v", err)
	}
}
