package commonpb

import "math/big"

// BalancesByAsset collapses an account's color-aware volume rows into the
// per-asset balances used by Reconciliation's current source contracts.
func BalancesByAsset(account *Account) map[string]*big.Int {
	if account == nil {
		return map[string]*big.Int{}
	}

	balances := make(map[string]*big.Int, len(account.GetVolumes()))
	for _, entry := range account.GetVolumes() {
		if entry == nil {
			continue
		}

		asset := entry.GetAsset()
		if balances[asset] == nil {
			balances[asset] = new(big.Int)
		}
		balances[asset].Add(balances[asset], VolumeBalance(entry.GetVolumes()))
	}

	return balances
}

// BalanceByAsset returns an account's balance for asset summed across every
// Ledger color bucket. A missing account or asset has a zero balance.
func BalanceByAsset(account *Account, asset string) *big.Int {
	var balance big.Int
	if account == nil {
		return &balance
	}

	for _, entry := range account.GetVolumes() {
		if entry != nil && entry.GetAsset() == asset {
			balance.Add(&balance, VolumeBalance(entry.GetVolumes()))
		}
	}

	return &balance
}

// VolumeBalance derives a balance from the Ledger-provided value when present,
// falling back to input minus output. A malformed value is treated as zero.
func VolumeBalance(volume *VolumesWithBalance) *big.Int {
	if volume == nil {
		return new(big.Int)
	}
	if volume.GetBalance() != nil {
		if b, err := volume.GetBalance().ToBigInt(); err == nil {
			return b
		}
	}

	return new(big.Int).Sub(bigOrZero(volume.GetInput()), bigOrZero(volume.GetOutput()))
}

func bigOrZero(v *BigUint) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	if b, err := v.ToBigInt(); err == nil {
		return b
	}

	return new(big.Int)
}
