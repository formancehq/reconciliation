package templates

// SEC-100 phase-1 reproduction: template-boundary validation tests.
//
// Expected on unpatched base c5435ac: the RED assertions fail —
// SourceSpec.Validate, InvariantSpec term validation, AccountThreshold.Validate
// and LedgerVsPoolDrift.Validate only check non-emptiness, so traversal /
// delimiter / encoded ledger identifiers are accepted at rule creation and
// later interpolated raw into the outbound SDK URL by the engine resolvers.
// After the fix these tests must pass (ErrInvalidSpec on unsafe identifiers,
// supported names still accepted).

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// supportedLedgerNames are legitimate per the authoritative contract
// (invariants.ValidateLedgerName: charset [a-zA-Z0-9._:-], <= 64 bytes).
var supportedLedgerNames = []string{
	"main",
	"buildr",
	"ledger.prod",
	"tenant:prod.eu-west-1",
	"my-ledger-123",
	"ledger_test",
	"_underscore-prefixed", // only exact "_" and "_system" are reserved
}

// unsafeLedgerIdentifiers must never become an outbound path segment.
var unsafeLedgerIdentifiers = []string{
	"main/../../payments/v3/connectors/configurations", // dot-segment traversal
	"main%2F..%2F..%2Fpayments",                         // encoded slash traversal
	"main?next=1",                                       // query delimiter truncates route
	"main#frag",                                         // fragment delimiter truncates route
	"main%3Fnext=1",                                     // encoded query delimiter
	"..", ".", "../..",                                  // dot-only segments (PathEscape does not escape them)
	"main\\..\\..\\auth",                                 // backslash traversal (Windows-style / some proxies)
	"main%00",                                           // encoded null byte
	"ma in",                                             // space
	"main\n",                                            // control char
	"tênant",                                            // non-ASCII
	"",                                                  // empty (already covered, kept for completeness)
}

func TestSEC100_SourceSpecValidate_AcceptsSupportedLedgerNames(t *testing.T) {
	for _, name := range supportedLedgerNames {
		s := SourceSpec{Kind: SourceLedger, Ledger: name, Query: json.RawMessage(`{}`)}
		require.NoError(t, s.Validate("left"), "supported ledger name %q must be accepted (parity/no false rejection)", name)
	}
}

func TestSEC100_SourceSpecValidate_RejectsUnsafeLedgerIdentifiers(t *testing.T) {
	for _, name := range unsafeLedgerIdentifiers {
		s := SourceSpec{Kind: SourceLedger, Ledger: name, Query: json.RawMessage(`{}`)}
		err := s.Validate("left")
		if err == nil {
			t.Fatalf("REPRODUCED: SourceSpec.Validate accepted unsafe ledger identifier %q — it would be interpolated raw into the outbound SDK URL", name)
		}
		require.ErrorIs(t, err, ErrInvalidSpec, "ledger %q", name)
	}
}

func TestSEC100_LedgerInvariant_Validate_RejectsUnsafeTermLedger(t *testing.T) {
	spec := InvariantSpec{
		Terms: []InvariantTerm{
			{Ledger: "main/../../payments", Query: json.RawMessage(`{}`), Sign: 1},
		},
		Tolerance: map[string]int64{"USD/2": 0},
	}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)

	err = NewLedgerInvariant().Validate(raw)
	if err == nil {
		t.Fatal("REPRODUCED: ledger_invariant.Validate accepted terms[0].ledger with traversal — accepted at POST /rules, interpolated raw at evaluation")
	}
	require.ErrorIs(t, err, ErrInvalidSpec)
}

func TestSEC100_AccountThreshold_Validate_RejectsUnsafeLedger(t *testing.T) {
	min := int64(0)
	spec := ThresholdSpec{Ledger: "main?next=1", Query: json.RawMessage(`{}`), Mode: ThresholdAggregate, Bounds: map[string]ThresholdBounds{"USD/2": {Min: &min}}}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)

	err = NewAccountThreshold().Validate(raw)
	if err == nil {
		t.Fatal("REPRODUCED: account_threshold.Validate accepted ledger with query delimiter")
	}
	require.ErrorIs(t, err, ErrInvalidSpec)
}

func TestSEC100_LedgerVsPoolDrift_Validate_RejectsUnsafeLedger(t *testing.T) {
	spec := DriftSpec{
		Ledger:         "main%2F..%2F..%2Fauth",
		LedgerQuery:    json.RawMessage(`{}`),
		PaymentsPoolID: "3b61a0f0-0000-4000-8000-000000000000",
		Tolerance:      map[string]int64{"USD/2": 0},
	}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)

	err = NewLedgerVsPoolDrift().Validate(raw)
	if err == nil {
		t.Fatal("REPRODUCED: ledger_vs_pool_drift.Validate accepted encoded-slash ledger")
	}
	require.ErrorIs(t, err, ErrInvalidSpec)
}

func TestSEC100_SourceParity_Validate_RejectsUnsafeSourceLedger(t *testing.T) {
	spec := ParitySpec{
		Left:  SourceSpec{Kind: SourceLedger, Ledger: "main/../../x", Query: json.RawMessage(`{}`)},
		Right: SourceSpec{Kind: SourceLedger, Ledger: "main", Query: json.RawMessage(`{}`)},
	}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)

	err = NewSourceParity().Validate(raw)
	if err == nil {
		t.Fatal("REPRODUCED: source_parity.Validate accepted unsafe left.ledger")
	}
	require.ErrorIs(t, err, ErrInvalidSpec)
}
