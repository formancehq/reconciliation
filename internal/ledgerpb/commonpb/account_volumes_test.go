package commonpb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalancesByAssetCollapsesColors(t *testing.T) {
	account := &Account{Volumes: []*AccountVolume{
		{Asset: "USD/2", Volumes: &VolumesWithBalance{Balance: "100"}},
		{Asset: "USD/2", Color: "RESERVED", Volumes: &VolumesWithBalance{Balance: "25"}},
		{Asset: "EUR/2", Color: "SETTLED", Volumes: &VolumesWithBalance{Input: "70", Output: "20"}},
		nil,
	}}

	balances := BalancesByAsset(account)

	require.Equal(t, "125", balances["USD/2"].String())
	require.Equal(t, "50", balances["EUR/2"].String())
	require.Equal(t, "125", BalanceByAsset(account, "USD/2").String())
	require.Equal(t, "0", BalanceByAsset(account, "GBP/2").String())
}

func TestVolumeBalance(t *testing.T) {
	require.Equal(t, "150", VolumeBalance(&VolumesWithBalance{Input: "200", Output: "40", Balance: "150"}).String())
	require.Equal(t, "160", VolumeBalance(&VolumesWithBalance{Input: "200", Output: "40"}).String())
	require.Equal(t, "0", VolumeBalance(&VolumesWithBalance{}).String())
	require.Equal(t, "-40", VolumeBalance(&VolumesWithBalance{Output: "40"}).String())
	require.Equal(t, "-5", VolumeBalance(&VolumesWithBalance{Balance: "-5"}).String())
	require.Equal(t, "0", VolumeBalance(nil).String())
	require.Equal(t, "0", VolumeBalance(&VolumesWithBalance{Balance: "invalid"}).String())
}
