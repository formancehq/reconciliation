package ledger

import (
	"encoding/json"
	"testing"

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
