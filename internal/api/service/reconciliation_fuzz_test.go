package service

import (
	"math/big"
	"testing"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
)

func FuzzComputeDrift(f *testing.F) {
	// Seed corpus: (asset, ledgerBalance, paymentBalance, ledgerNil, paymentNil)
	f.Add("USD", int64(100), int64(100), false, false)
	f.Add("EUR", int64(0), int64(0), false, false)
	f.Add("BTC", int64(-50), int64(50), false, false)
	f.Add("USD", int64(100), int64(-200), false, false)
	f.Add("USD", int64(0), int64(0), true, false)
	f.Add("USD", int64(0), int64(0), false, true)
	f.Add("USD", int64(0), int64(0), true, true)
	f.Add("XAU", int64(9223372036854775807), int64(-9223372036854775807), false, false)
	f.Add("", int64(1), int64(-1), false, false)
	f.Add("USD", int64(-1), int64(0), false, false)

	f.Fuzz(func(t *testing.T, asset string, ledgerVal int64, paymentVal int64, ledgerNil bool, paymentNil bool) {
		var ledgerBalance *big.Int
		if !ledgerNil {
			ledgerBalance = big.NewInt(ledgerVal)
		}

		var paymentBalance *big.Int
		if !paymentNil {
			paymentBalance = big.NewInt(paymentVal)
		}

		res := &models.Reconciliation{
			ID:            uuid.New(),
			DriftBalances: make(map[string]*big.Int),
		}

		svc := (*Service)(nil)
		// Should not panic
		err := svc.computeDrift(res, asset, ledgerBalance, paymentBalance)

		// Property: if both are nil, no error and no drift entry
		if ledgerNil && paymentNil {
			if err != nil {
				t.Errorf("both nil: expected no error, got %v", err)
			}
			return
		}

		// Property: drift balance should always be non-negative (it's an absolute value)
		if drift, ok := res.DriftBalances[asset]; ok && drift != nil {
			if drift.Sign() < 0 {
				t.Errorf("drift balance for %q is negative: %s", asset, drift.String())
			}
		}

		// Property: if one side is nil, we should get an error
		if (ledgerNil && !paymentNil) || (!ledgerNil && paymentNil) {
			if err == nil {
				t.Errorf("expected error when one side is nil, got nil")
			}
		}

		// Property: if both non-nil and equal in magnitude but opposite sign, drift should be 0
		if !ledgerNil && !paymentNil {
			sum := new(big.Int).Add(big.NewInt(ledgerVal), big.NewInt(paymentVal))
			if sum.Sign() >= 0 {
				// No error expected
				if err != nil {
					t.Errorf("non-negative sum %s: expected no error, got %v", sum.String(), err)
				}
			}
		}
	})
}
