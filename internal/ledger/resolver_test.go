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

// TestDataLedgerLeaf_MetadataMatchTypes covers the value-typed `$match`: string,
// bool, and integer equality (an int match is an inclusive [n, n] range).
func TestDataLedgerLeaf_MetadataMatchTypes(t *testing.T) {
	t.Parallel()

	b, err := dataLedgerLeaf("$match", "metadata[enabled]", true)
	require.NoError(t, err)
	require.Equal(t, "enabled", b.GetField().GetField().GetMetadata())
	require.True(t, b.GetField().GetBoolCond().GetHardcoded())

	// JSON numbers decode to float64 in the query walker.
	n, err := dataLedgerLeaf("$match", "metadata[tier]", float64(3))
	require.NoError(t, err)
	cond := n.GetField().GetIntCond()
	require.Equal(t, int64(3), cond.GetMin())
	require.Equal(t, int64(3), cond.GetMax())
	require.False(t, cond.GetMinExclusive())
	require.False(t, cond.GetMaxExclusive())
}

// TestDataLedgerLeaf_MetadataComparisons covers $gt/$gte/$lt/$lte → an int range
// with the correct bound and exclusivity.
func TestDataLedgerLeaf_MetadataComparisons(t *testing.T) {
	t.Parallel()

	gt, err := dataLedgerLeaf("$gt", "metadata[tier]", float64(3))
	require.NoError(t, err)
	require.Equal(t, int64(3), gt.GetField().GetIntCond().GetMin())
	require.True(t, gt.GetField().GetIntCond().GetMinExclusive())
	require.Nil(t, gt.GetField().GetIntCond().Max)

	gte, err := dataLedgerLeaf("$gte", "metadata[tier]", float64(3))
	require.NoError(t, err)
	require.Equal(t, int64(3), gte.GetField().GetIntCond().GetMin())
	require.False(t, gte.GetField().GetIntCond().GetMinExclusive())

	lt, err := dataLedgerLeaf("$lt", "metadata[tier]", float64(10))
	require.NoError(t, err)
	require.Equal(t, int64(10), lt.GetField().GetIntCond().GetMax())
	require.True(t, lt.GetField().GetIntCond().GetMaxExclusive())
	require.Nil(t, lt.GetField().GetIntCond().Min)

	lte, err := dataLedgerLeaf("$lte", "metadata[tier]", float64(10))
	require.NoError(t, err)
	require.Equal(t, int64(10), lte.GetField().GetIntCond().GetMax())
	require.False(t, lte.GetField().GetIntCond().GetMaxExclusive())
}

// TestDataLedgerLeaf_MetadataExists covers $exists: true → ExistsCond,
// false → NOT ExistsCond.
func TestDataLedgerLeaf_MetadataExists(t *testing.T) {
	t.Parallel()

	yes, err := dataLedgerLeaf("$exists", "metadata[counterparty]", true)
	require.NoError(t, err)
	require.NotNil(t, yes.GetField().GetExistsCond())
	require.Equal(t, "counterparty", yes.GetField().GetField().GetMetadata())

	no, err := dataLedgerLeaf("$exists", "metadata[counterparty]", false)
	require.NoError(t, err)
	require.NotNil(t, no.GetNot().GetFilter().GetField().GetExistsCond())
}

func TestDataLedgerLeaf_Rejects(t *testing.T) {
	t.Parallel()

	// address supports $match only.
	_, err := dataLedgerLeaf("$gt", "address", "acct:x")
	require.Error(t, err, "address supports only $match")

	_, err = dataLedgerLeaf("$match", "unsupported", "x")
	require.Error(t, err, "unknown key")

	_, err = dataLedgerLeaf("$match", "address", 42)
	require.Error(t, err, "non-string address value")

	// Operators recon does not map onto the ledger's account filter.
	_, err = dataLedgerLeaf("$like", "metadata[type]", "pay*")
	require.Error(t, err, "$like unsupported on metadata")

	_, err = dataLedgerLeaf("$in", "metadata[type]", []any{"a", "b"})
	require.Error(t, err, "$in unsupported on metadata")

	// Type mismatches per operator.
	_, err = dataLedgerLeaf("$gt", "metadata[tier]", "3")
	require.Error(t, err, "comparison needs a numeric value")

	_, err = dataLedgerLeaf("$match", "metadata[tier]", float64(3.5))
	require.Error(t, err, "non-integral number")

	_, err = dataLedgerLeaf("$exists", "metadata[counterparty]", "yes")
	require.Error(t, err, "$exists needs a bool value")
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
		Volumes: []*commonpb.AccountVolume{
			{Asset: "USD/2", Volumes: &commonpb.VolumesWithBalance{Balance: "100"}},
			{Asset: "USD/2", Color: "RESERVED", Volumes: &commonpb.VolumesWithBalance{Balance: "25"}},
			{Asset: "EUR/2", Volumes: &commonpb.VolumesWithBalance{Input: "70", Output: "20"}},
		},
	}

	got := accountFromProto("ledgerA", acct)
	require.Equal(t, "acct:1", got.Address)
	require.Equal(t, "ledgerA", got.Ledger)
	require.Equal(t, map[string]string{"type": "payout"}, got.Metadata)
	require.Equal(t, "125", got.Balances["USD/2"].String())
	require.Equal(t, "50", got.Balances["EUR/2"].String())
}
