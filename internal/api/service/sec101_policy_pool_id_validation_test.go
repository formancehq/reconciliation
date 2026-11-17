package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
)

func TestSEC101_CreatePolicy_PoolIDAlreadyUUIDValidated(t *testing.T) {
	s := NewService(newMockStore(), nil, nil, nil, engine.Resolvers{})
	valid := "0eb4a31f-751e-42d4-8d5b-2129e6d4cf4c"
	p, err := s.CreatePolicy(t.Context(), &CreatePolicyRequest{
		Name:           "p",
		LedgerName:     "main",
		LedgerQuery:    map[string]interface{}{},
		PaymentsPoolID: valid,
	})
	if err != nil {
		t.Fatalf("valid pool UUID rejected: %v", err)
	}
	if p.PaymentsPoolID.String() != valid {
		t.Fatalf("persisted pool id = %q, want %q", p.PaymentsPoolID, valid)
	}
}

func TestSEC101_CreatePolicy_RejectsMalformedPoolIDWithoutReflection(t *testing.T) {
	s := NewService(newMockStore(), nil, nil, nil, engine.Resolvers{})
	malformed := map[string]string{
		"path traversal":    "../../other",
		"encoded traversal": "..%2f..%2fother",
		"slash":             "a/b",
		"query injection":   "x?at=2020",
		"not a uuid":        "pool_xyz",
		"empty":             "",
		"uuid+suffix":       "0eb4a31f-751e-42d4-8d5b-2129e6d4cf4c/extra",
	}
	for name, poolID := range malformed {
		t.Run(name, func(t *testing.T) {
			_, err := s.CreatePolicy(t.Context(), &CreatePolicyRequest{
				Name:           "p",
				LedgerName:     "main",
				LedgerQuery:    map[string]interface{}{},
				PaymentsPoolID: poolID,
			})
			if !errors.Is(err, ErrInvalidID) {
				t.Fatalf("poolID %q: expected ErrInvalidID, got %v", poolID, err)
			}
			// Defensive: the raw malformed identifier must not be reflected in
			// the error text (uuid.Parse does not echo input).
			if poolID != "" && strings.Contains(err.Error(), poolID) {
				t.Errorf("error reflects raw poolID %q: %q", poolID, err.Error())
			}
		})
	}
}

// Keep ledgerName coverage distinct from pool IDs. Its full naming grammar is
// owned by SEC-100; this test covers presence and a supported ordinary name.
func TestSEC101_PolicyLedgerNamePresence(t *testing.T) {
	for _, ledger := range []string{"", "main"} {
		req := &CreatePolicyRequest{Name: "policy", LedgerName: ledger, PaymentsPoolID: "0eb4a31f-751e-42d4-8d5b-2129e6d4cf4c"}
		err := req.Validate()
		if (err != nil) != (ledger == "") {
			t.Fatalf("ledgerName %q: %v", ledger, err)
		}
	}
}
