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

// TestSumAccountMetadataByPrefix_SkipsNonAssetSuffix proves the eval-time guard:
// a suffix that is not a well-formed asset code (a connector's sidecar metadata,
// or a typo) is skipped, not turned into a phantom asset reconciled against 0.
// A malformed value under such a key is never parsed, so it cannot error either.
func TestSumAccountMetadataByPrefix_SkipsNonAssetSuffix(t *testing.T) {
	accts := []Account{{Address: "mirror:a", Metadata: map[string]string{
		"reported_balance.USDC":       "1000", // valid asset → kept
		"reported_balance.updated_at": "1720000000",
		"reported_balance.note":       "hello", // non-integer, but skipped before parse
		"reported_balance.usdc":       "5",     // lowercase → not a valid asset code
	}}}
	got, err := SumAccountMetadataByPrefix(accts, "reported_balance.")
	if err != nil {
		t.Fatalf("SumAccountMetadataByPrefix: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected only USDC to be treated as an asset, got %d: %v", len(got), got)
	}
	if got["USDC"].String() != "1000" {
		t.Errorf("USDC = %s, want 1000", got["USDC"])
	}
}

func TestValidAssetCode(t *testing.T) {
	valid := []string{
		"USD", "USDC", "EURC", "A", "BTC", "USD/2", "EURC/6", "BTC/8",
		"USD/1", "USD/255", "A0", "ABCDEFGHIJKLMNOPQ", // 17-char base (max)
	}
	for _, s := range valid {
		if !ValidAssetCode(s) {
			t.Errorf("ValidAssetCode(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",                   // empty
		"usd",                // lowercase base
		"usdc",               // lowercase
		"1USD",               // leading digit
		"USD_TOKEN",          // underscore not allowed by the ledger's core validator
		"US-D",               // hyphen
		"USD/",               // empty precision
		"USD/0",              // precision zero
		"USD/02",             // leading zero
		"USD/256",            // precision above 255
		"USD/2/3",            // second slash
		"USD/x",              // non-numeric precision
		"ABCDEFGHIJKLMNOPQR", // 18-char base (over max)
		"updated_at",         // sidecar suffix
		"note",               // typo suffix (lowercase)
		" USD",               // leading space
	}
	for _, s := range invalid {
		if ValidAssetCode(s) {
			t.Errorf("ValidAssetCode(%q) = true, want false", s)
		}
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
