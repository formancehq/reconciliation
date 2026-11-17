package templates

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// validPoolID is a normal supported identifier (canonical UUID).
const validPoolID = "0eb4a31f-751e-42d4-8d5b-2129e6d4cf4c"

// malformedPoolIDs are non-empty strings that are not UUIDs.
var malformedPoolIDs = map[string]string{
	"absolute URL":           "https://example.invalid/pool",
	"network URL":            "//example.invalid/pool",
	"encoded UUID":           "%30" + validPoolID[1:],
	"path traversal":         "../../other-pool",
	"encoded traversal":      "..%2f..%2fother-pool", // raw JSON keeps %xx literal
	"double-encoded slash":   "%252e%252e%252fpool",
	"slash":                  "a/b",
	"backslash":              `a\b`,
	"query injection":        "x?at=2020-01-01",
	"fragment injection":     "x#frag",
	"space":                  "a b",
	"newline":                "a\nb",
	"null byte":              "a\x00b",
	"percent":                "100%",
	"not a uuid (word)":      "pool_xyz",
	"uuid with extra suffix": validPoolID + "/balances",
}

func TestSEC101_SourceParity_Validate_RejectsMalformedPoolID(t *testing.T) {
	tmpl := NewSourceParity()
	for name, poolID := range malformedPoolIDs {
		t.Run(name, func(t *testing.T) {
			spec := mustJSON(t, ParitySpec{
				Left:  ledgerSource("main", `{}`),
				Right: poolSource(poolID),
			})
			err := tmpl.Validate(spec)
			if !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("poolID %q: expected ErrInvalidSpec rejection, got err=%v", poolID, err)
			}
			if strings.Contains(err.Error(), poolID) {
				t.Errorf("error reflects raw poolID %q: %q", poolID, err.Error())
			}
		})
	}
}

// Both sides of a source_parity spec are validated independently.
func TestSEC101_SourceParity_Validate_RejectsMalformedPoolIDOnEitherSide(t *testing.T) {
	tmpl := NewSourceParity()
	cases := map[string]ParitySpec{
		"left pool malformed":  {Left: poolSource("../x"), Right: ledgerSource("main", `{}`)},
		"right pool malformed": {Left: ledgerSource("main", `{}`), Right: poolSource("../x")},
		"both pools, left bad": {Left: poolSource("a/b"), Right: poolSource(validPoolID)},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := tmpl.Validate(mustJSON(t, spec)); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

// Same contract for the ledger_vs_pool_drift template's paymentsPoolID field.
func TestSEC101_Drift_Validate_RejectsMalformedPaymentsPoolID(t *testing.T) {
	tmpl := NewLedgerVsPoolDrift()
	for name, poolID := range malformedPoolIDs {
		t.Run(name, func(t *testing.T) {
			spec := mustJSON(t, DriftSpec{
				Ledger:         "main",
				LedgerQuery:    json.RawMessage(`{}`),
				PaymentsPoolID: poolID,
			})
			err := tmpl.Validate(spec)
			if !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("paymentsPoolID %q: expected ErrInvalidSpec rejection, got err=%v", poolID, err)
			}
			if strings.Contains(err.Error(), poolID) {
				t.Errorf("error reflects raw paymentsPoolID %q: %q", poolID, err.Error())
			}
		})
	}
}

// Empty stays rejected (pre-existing behavior, distinct message).
func TestSEC101_PoolID_EmptyStillRejected(t *testing.T) {
	if err := NewSourceParity().Validate(mustJSON(t, ParitySpec{
		Left: ledgerSource("main", `{}`), Right: poolSource(""),
	})); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("empty poolID: expected ErrInvalidSpec, got %v", err)
	}
	if err := NewLedgerVsPoolDrift().Validate(mustJSON(t, DriftSpec{
		Ledger: "main", LedgerQuery: json.RawMessage(`{}`),
	})); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("empty paymentsPoolID: expected ErrInvalidSpec, got %v", err)
	}
}

// Valid flows stay valid: canonical UUID, uppercase-hex UUID and the nil UUID
// are accepted on both templates and on either side of source_parity, and
// pool↔pool parity with valid UUIDs passes. Noncanonical-but-parseable UUID
// variants (32-hex, braces, urn) are also accepted — the contract reuses
// uuid.Parse, no canonical-only restriction.
func TestSEC101_PoolID_ValidFlowsStayValid(t *testing.T) {
	parity := NewSourceParity()
	drift := NewLedgerVsPoolDrift()
	valid := map[string]string{
		"canonical uuid": validPoolID,
		"uppercase uuid": strings.ToUpper(validPoolID),
		"nil uuid":       "00000000-0000-0000-0000-000000000000",
		"hex no hyphens": "0eb4a31f751e42d48d5b2129e6d4cf4c",
		"braced":         "{" + validPoolID + "}",
		"urn form":       "urn:uuid:" + validPoolID,
	}
	for name, poolID := range valid {
		t.Run("parity/"+name, func(t *testing.T) {
			spec := mustJSON(t, ParitySpec{
				Left:  ledgerSource("main", `{}`),
				Right: poolSource(poolID),
			})
			if err := parity.Validate(spec); err != nil {
				t.Fatalf("valid poolID %q rejected: %v", poolID, err)
			}
		})
		t.Run("drift/"+name, func(t *testing.T) {
			spec := mustJSON(t, DriftSpec{
				Ledger:         "main",
				LedgerQuery:    json.RawMessage(`{}`),
				PaymentsPoolID: poolID,
			})
			if err := drift.Validate(spec); err != nil {
				t.Fatalf("valid paymentsPoolID %q rejected: %v", poolID, err)
			}
		})
	}
	t.Run("parity pool-vs-pool", func(t *testing.T) {
		spec := mustJSON(t, ParitySpec{
			Left:  poolSource(validPoolID),
			Right: poolSource("11111111-2222-3333-4444-555555555555"),
		})
		if err := parity.Validate(spec); err != nil {
			t.Fatalf("pool-vs-pool with valid UUIDs rejected: %v", err)
		}
	})
}

// Validation runs on the raw decoded JSON string field: encoding/json does not
// percent-decode, so an encoded traversal stays literal and is rejected.
func TestSEC101_PoolID_ValidateOnRawDecodedJSON(t *testing.T) {
	tmpl := NewSourceParity()
	raw := []byte(`{"left":{"kind":"ledger","ledger":"main","query":{}},"right":{"kind":"payments_pool","poolID":"..%2f..%2fetc"}}`)
	var probe struct {
		Right struct {
			PoolID string `json:"poolID"`
		} `json:"right"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v", err)
	}
	if probe.Right.PoolID != "..%2f..%2fetc" {
		t.Fatalf("expected raw literal poolID, got %q", probe.Right.PoolID)
	}
	if err := tmpl.Validate(raw); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("raw JSON with encoded-traversal poolID: expected ErrInvalidSpec, got %v", err)
	}
}
