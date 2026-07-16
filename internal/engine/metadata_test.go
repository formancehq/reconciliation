package engine

import (
	"context"
	"math/big"
	"testing"
	"time"
)

func TestSumAccountMetadataInt(t *testing.T) {
	accts := []Account{
		{Address: "mirror:a", Metadata: map[string]string{"ext": "1000"}},
		{Address: "mirror:b", Metadata: map[string]string{"ext": " 250 "}}, // trimmed
	}
	got, err := SumAccountMetadataInt(accts, "ext")
	if err != nil {
		t.Fatalf("SumAccountMetadataInt: %v", err)
	}
	if got.String() != "1250" {
		t.Errorf("sum = %s, want 1250", got)
	}
}

func TestSumAccountMetadataInt_MissingKey(t *testing.T) {
	accts := []Account{{Address: "mirror:a", Metadata: map[string]string{"other": "1"}}}
	if _, err := SumAccountMetadataInt(accts, "ext"); err == nil {
		t.Fatal("expected error on missing key, got nil")
	}
}

func TestSumAccountMetadataInt_NonInteger(t *testing.T) {
	accts := []Account{{Address: "mirror:a", Metadata: map[string]string{"ext": "12.5"}}}
	if _, err := SumAccountMetadataInt(accts, "ext"); err == nil {
		t.Fatal("expected error on non-integer value, got nil")
	}
}

func TestSumAccountMetadataByPrefix(t *testing.T) {
	accts := []Account{
		{Address: "mirror:a", Metadata: map[string]string{
			"reported_balance.USDC": "1000",
			"reported_balance.EURC": "500",
			"reported_balance.":     "9", // empty suffix → skipped
			"unrelated":             "7", // no prefix → ignored
		}},
		{Address: "mirror:b", Metadata: map[string]string{
			"reported_balance.USDC": "250", // aggregates with mirror:a
		}},
	}
	got, err := SumAccountMetadataByPrefix(accts, "reported_balance.")
	if err != nil {
		t.Fatalf("SumAccountMetadataByPrefix: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 assets (USDC, EURC), got %d: %v", len(got), got)
	}
	if got["USDC"].String() != "1250" {
		t.Errorf("USDC = %s, want 1250 (1000+250)", got["USDC"])
	}
	if got["EURC"].String() != "500" {
		t.Errorf("EURC = %s, want 500", got["EURC"])
	}
}

func TestSumAccountMetadataByPrefix_NonInteger(t *testing.T) {
	accts := []Account{{Address: "mirror:a", Metadata: map[string]string{"reported_balance.USDC": "1.5"}}}
	if _, err := SumAccountMetadataByPrefix(accts, "reported_balance."); err == nil {
		t.Fatal("expected error on non-integer value, got nil")
	}
}

// TestEvaluate_MetadataInt exercises the metadataInt builtin end-to-end through
// the kernel: it reads an integer metadata field off the matched accounts and
// sums it, so a mirror account carrying a synced balance reconciles against a
// hardcoded expectation.
func TestEvaluate_MetadataInt(t *testing.T) {
	l := &fakeLedger{accounts: map[string][]Account{
		"treasury|q": {{Address: "mirror:stripe", Metadata: map[string]string{"ext_balance": "35000"}}},
	}}
	eng := newTestEngine(t, l)
	c, err := eng.Compile(`metadataInt(ledgerSet("treasury","q"), "ext_balance") == 35000`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out, err := eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !out.Passed {
		t.Fatalf("expected pass, got fail (result=%v)", out.Result)
	}
}

func TestEvaluate_MetadataInt_MissingKey_Errors(t *testing.T) {
	l := &fakeLedger{accounts: map[string][]Account{
		"treasury|q": {{Address: "mirror:stripe", Metadata: map[string]string{"other": "1"}}},
	}}
	eng := newTestEngine(t, l)
	c, err := eng.Compile(`metadataInt(ledgerSet("treasury","q"), "ext_balance") == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := eng.Evaluate(context.Background(), c, EvalInput{PIT: time.Now()}); err == nil {
		t.Fatal("expected ErrEvaluate on missing metadata key, got nil")
	}
}

var _ = big.NewInt
