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
		"malformed asset":     metadataSource("l", `{}`, "ext", "usd"),
		"unknown kind":        {Kind: "bank", Ledger: "l", Query: json.RawMessage(`{}`)},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.Validate("src"); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}

	for _, key := range []string{"reported.USD", "value_known.toto"} {
		t.Run("opaque key "+key, func(t *testing.T) {
			if err := metadataSource("l", `{}`, key, "USD/2").Validate("src"); err != nil {
				t.Errorf("valid account_metadata source rejected: %v", err)
			}
		})
	}
}

// A metadata rule binds one opaque metadata key to one declared asset. Other
// assets on the ledger account are outside this rule and require their own rule.
func TestSourceParity_LedgerVsMetadata_SingleAsset(t *testing.T) {
	tmpl := NewSourceParity()
	const bookQ = `{"$match":{"address":"cash:stripe"}}`
	const mirrorQ = `{"$match":{"address":"mirror:stripe"}}`

	l := &fakeLedger{
		balances: map[string]map[string]*big.Int{
			"book|" + bookQ: {
				"USD/2": big.NewInt(35000),
				"EUR/2": big.NewInt(12000), // unrelated; a separate rule may check it
			},
		},
		accounts: map[string][]engine.Account{
			"mirror|" + mirrorQ: {{
				Address: "mirror:stripe",
				Metadata: map[string]string{
					"value_known.toto": "35000",
					"reported.EUR":     "12000",
				},
			}},
		},
	}
	eng, res := newTestEngine(t, l)

	spec := mustJSON(t, ParitySpec{
		Left:  ledgerSource("book", bookQ),
		Right: metadataSource("mirror", mirrorQ, "value_known.toto", "USD/2"),
	})
	out, err := tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected exactly one USD/2 outcome, got %d: %+v", len(out), out)
	}
	o := findOutcome(out, "asset:USD/2")
	if o == nil || !o.Passed {
		t.Fatalf("expected PASS (35000 == synced 35000), got %+v", out)
	}
	if o.Evidence["rightSource"] != "metadata:mirror[value_known.toto]" {
		t.Errorf("unexpected metadata source label: %v", o.Evidence["rightSource"])
	}

	l.accounts["mirror|"+mirrorQ][0].Metadata["value_known.toto"] = "34000"
	out, err = tmpl.Evaluate(context.Background(), spec, eng, res, engine.EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate (drift): %v", err)
	}
	if o := findOutcome(out, "asset:USD/2"); o == nil || o.Passed {
		t.Fatalf("expected FAIL after sync drift, got %+v", out)
	}
}

func TestSourceParity_MetadataExplainCompiles(t *testing.T) {
	tmpl := NewSourceParity()
	spec := mustJSON(t, ParitySpec{
		Left:      ledgerSource("book", `{"$match":{"address":"cash:stripe"}}`),
		Right:     metadataSource("mirror", `{"$match":{"address":"mirror:stripe"}}`, "value_known.toto", "USD/2"),
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

func TestSourceParity_MetadataSourcesRequireSameAsset(t *testing.T) {
	tmpl := NewSourceParity()
	spec := mustJSON(t, ParitySpec{
		Left:  metadataSource("a", `{}`, "left.value", "USD/2"),
		Right: metadataSource("b", `{}`, "right.value", "EUR/2"),
	})
	if err := tmpl.Validate(spec); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec for mismatched assets, got %v", err)
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
