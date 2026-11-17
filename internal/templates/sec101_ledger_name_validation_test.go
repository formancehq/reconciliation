package templates

import (
	"errors"
	"testing"
)

// Empty ledger is still rejected with ErrInvalidSpec on both templates and on
// either side of source_parity.
func TestSEC101_LedgerName_EmptyStillRejected(t *testing.T) {
	parity := NewSourceParity()
	drift := NewLedgerVsPoolDrift()
	cases := map[string]func() error{
		"parity left empty": func() error {
			return parity.Validate(mustJSON(t, ParitySpec{Left: ledgerSource("", `{}`), Right: poolSource(validPoolID)}))
		},
		"parity right empty": func() error {
			return parity.Validate(mustJSON(t, ParitySpec{Left: poolSource(validPoolID), Right: ledgerSource("", `{}`)}))
		},
		"drift empty": func() error {
			return drift.Validate(mustJSON(t, DriftSpec{Ledger: "", LedgerQuery: []byte(`{}`), PaymentsPoolID: validPoolID}))
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("expected ErrInvalidSpec for empty ledger, got %v", err)
			}
		})
	}
}

// A normal supported ledger name keeps passing Validate on both templates and
// both parity sides — guards against an over-broad change breaking valid flows.
func TestSEC101_LedgerName_ValidFlowStaysValid(t *testing.T) {
	parity := NewSourceParity()
	drift := NewLedgerVsPoolDrift()
	valid := []string{"main", "default", "buildr", "ledger-1", "ledger_2", "Ledger3"}
	for _, ledger := range valid {
		t.Run("parity/"+ledger, func(t *testing.T) {
			spec := mustJSON(t, ParitySpec{Left: ledgerSource(ledger, `{}`), Right: poolSource(validPoolID)})
			if err := parity.Validate(spec); err != nil {
				t.Fatalf("valid ledger %q rejected: %v", ledger, err)
			}
		})
		t.Run("drift/"+ledger, func(t *testing.T) {
			spec := mustJSON(t, DriftSpec{Ledger: ledger, LedgerQuery: []byte(`{}`), PaymentsPoolID: validPoolID})
			if err := drift.Validate(spec); err != nil {
				t.Fatalf("valid ledger %q rejected: %v", ledger, err)
			}
		})
	}
}
