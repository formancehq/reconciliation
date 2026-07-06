package ledgerresolver

import (
	"math/big"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/stretchr/testify/require"
)

func TestToEngineAccounts(t *testing.T) {
	t.Parallel()

	in := []ledger.Account{
		{
			Address:  "acct:a",
			Ledger:   "ledgerA",
			Metadata: map[string]string{"type": "payout"},
			Balances: map[string]*big.Int{"USD/2": big.NewInt(100)},
		},
		{
			Address:  "acct:b",
			Ledger:   "ledgerA",
			Balances: map[string]*big.Int{"EUR/2": big.NewInt(-5)},
		},
	}

	out := toEngineAccounts(in)
	require.Len(t, out, 2)

	require.Equal(t, "acct:a", out[0].Address)
	require.Equal(t, "ledgerA", out[0].Ledger)
	require.Equal(t, map[string]string{"type": "payout"}, out[0].Metadata)
	require.Equal(t, "100", out[0].Balances["USD/2"].String())

	require.Equal(t, "acct:b", out[1].Address)
	require.Equal(t, "-5", out[1].Balances["EUR/2"].String())
}

func TestToEngineAccounts_Empty(t *testing.T) {
	t.Parallel()

	require.Empty(t, toEngineAccounts(nil))
}
