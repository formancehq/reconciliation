package ledgerschema_test

import (
	"encoding/json"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/stretchr/testify/require"
)

// echoLeaf maps a leaf to a metadata-string filter keyed by the predicate key,
// so tests can assert the tree shape without a resource-specific mapper.
func echoLeaf(_, key string, value any) (*commonpb.QueryFilter, error) {
	return schema.FilterMetadataString(key, value.(string)), nil
}

func TestTranslateQuery_Empty(t *testing.T) {
	t.Parallel()

	f, err := schema.TranslateQuery(nil, echoLeaf)
	require.NoError(t, err)
	require.Nil(t, f, "empty query → nil (caller picks the default)")
}

func TestTranslateQuery_Leaf(t *testing.T) {
	t.Parallel()

	f, err := schema.TranslateQuery(json.RawMessage(`{"$match":{"status":"OPEN"}}`), echoLeaf)
	require.NoError(t, err)
	require.Equal(t, "status", f.GetField().GetField().GetMetadata())
	require.Equal(t, "OPEN", f.GetField().GetStringCond().GetHardcoded())
}

func TestTranslateQuery_AndOrNot(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"$and":[{"$match":{"a":"1"}},{"$or":[{"$match":{"b":"2"}},{"$not":{"$match":{"c":"3"}}}]}]}`)
	f, err := schema.TranslateQuery(raw, echoLeaf)
	require.NoError(t, err)

	and := f.GetAnd().GetFilters()
	require.Len(t, and, 2)
	require.Equal(t, "a", and[0].GetField().GetField().GetMetadata())

	or := and[1].GetOr().GetFilters()
	require.Len(t, or, 2)
	require.Equal(t, "b", or[0].GetField().GetField().GetMetadata())
	require.Equal(t, "c", or[1].GetNot().GetFilter().GetField().GetField().GetMetadata())
}

func TestTranslateQuery_StringWrapped(t *testing.T) {
	t.Parallel()

	// A JSON string wrapping the object (some template compilers emit that).
	f, err := schema.TranslateQuery(json.RawMessage(`"{\"$match\":{\"k\":\"v\"}}"`), echoLeaf)
	require.NoError(t, err)
	require.Equal(t, "k", f.GetField().GetField().GetMetadata())
}

func TestTranslateQuery_Malformed(t *testing.T) {
	t.Parallel()

	_, err := schema.TranslateQuery(json.RawMessage(`{"$and":{"not":"an array"}}`), echoLeaf)
	require.Error(t, err)
}
