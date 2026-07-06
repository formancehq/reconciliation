package ledger

import (
	"encoding/json"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/stretchr/testify/require"
)

func TestDataLedgerLeaf_AddressExactVsPrefix(t *testing.T) {
	t.Parallel()

	exact, err := dataLedgerLeaf("$match", "address", "acct:x")
	require.NoError(t, err)
	require.Equal(t, "acct:x", exact.GetAddress().GetHardcodedExact())
	require.Empty(t, exact.GetAddress().GetHardcodedPrefix())

	prefix, err := dataLedgerLeaf("$match", "address", "acct:*")
	require.NoError(t, err)
	require.Equal(t, "acct:", prefix.GetAddress().GetHardcodedPrefix())
	require.Empty(t, prefix.GetAddress().GetHardcodedExact())
}

func TestDataLedgerLeaf_Metadata(t *testing.T) {
	t.Parallel()

	f, err := dataLedgerLeaf("$match", "metadata[type]", "payout")
	require.NoError(t, err)
	require.Equal(t, "type", f.GetField().GetField().GetMetadata())
	require.Equal(t, "payout", f.GetField().GetStringCond().GetHardcoded())
}

func TestDataLedgerLeaf_Rejects(t *testing.T) {
	t.Parallel()

	_, err := dataLedgerLeaf("$gt", "address", "acct:x")
	require.Error(t, err, "only $match is supported")

	_, err = dataLedgerLeaf("$match", "unsupported", "x")
	require.Error(t, err, "unknown key")

	_, err = dataLedgerLeaf("$match", "address", 42)
	require.Error(t, err, "non-string value")
}

// TestDataLedgerLeaf_ViaTranslateQuery exercises the leaf through the shared
// tree-walker, the path AggregateBalance takes.
func TestDataLedgerLeaf_ViaTranslateQuery(t *testing.T) {
	t.Parallel()

	f, err := schema.TranslateQuery(json.RawMessage(`{"$match":{"address":"pool:main"}}`), dataLedgerLeaf)
	require.NoError(t, err)
	require.Equal(t, "pool:main", f.GetAddress().GetHardcodedExact())
}

func TestVolumeBalance(t *testing.T) {
	t.Parallel()

	// Balance present → used verbatim, ignoring input/output.
	require.Equal(t, "150", volumeBalance(&commonpb.VolumesWithBalance{Input: "200", Output: "40", Balance: "150"}).String())
	// Balance absent → input − output.
	require.Equal(t, "160", volumeBalance(&commonpb.VolumesWithBalance{Input: "200", Output: "40"}).String())
	// Empty sides are 0.
	require.Equal(t, "0", volumeBalance(&commonpb.VolumesWithBalance{}).String())
	require.Equal(t, "-40", volumeBalance(&commonpb.VolumesWithBalance{Output: "40"}).String())
	// Negative balance survives round-trip.
	require.Equal(t, "-5", volumeBalance(&commonpb.VolumesWithBalance{Balance: "-5"}).String())
	// Nil is 0.
	require.Equal(t, "0", volumeBalance(nil).String())
}

func TestAccountFromProto(t *testing.T) {
	t.Parallel()

	acct := &commonpb.Account{
		Address: "acct:1",
		Metadata: map[string]*commonpb.MetadataValue{
			"type": {Type: &commonpb.MetadataValue_StringValue{StringValue: "payout"}},
		},
		Volumes: map[string]*commonpb.VolumesWithBalance{
			"USD/2": {Balance: "100"},
			"EUR/2": {Input: "70", Output: "20"},
		},
	}

	got := accountFromProto("ledgerA", acct)
	require.Equal(t, "acct:1", got.Address)
	require.Equal(t, "ledgerA", got.Ledger)
	require.Equal(t, map[string]string{"type": "payout"}, got.Metadata)
	require.Equal(t, "100", got.Balances["USD/2"].String())
	require.Equal(t, "50", got.Balances["EUR/2"].String())
}
