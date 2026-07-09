package commonpb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMetadataToMap_StringifiesEveryScalarType is the guard for F33: the flatten
// must render every declared scalar type as a string, not silently drop the
// non-string ones (which would make a typed data-ledger metadata field vanish
// from a rule's view).
func TestMetadataToMap_StringifiesEveryScalarType(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 7, 9, 12, 34, 56, 789_000_000, time.UTC)

	in := map[string]*MetadataValue{
		"s":        {Type: &MetadataValue_StringValue{StringValue: "hello"}},
		"i":        {Type: &MetadataValue_IntValue{IntValue: -42}},
		"u":        {Type: &MetadataValue_UintValue{UintValue: 42}},
		"b":        {Type: &MetadataValue_BoolValue{BoolValue: true}},
		"dt":       {Type: &MetadataValue_DatetimeValue{DatetimeValue: ts.UnixMicro()}},
		"null":     {Type: &MetadataValue_NullValue{NullValue: &NullValue{Original: "raw-value"}}},
		"nilval":   nil,
		"unsetval": {},
	}

	got := MetadataToMap(in)

	require.Equal(t, "hello", got["s"])
	require.Equal(t, "-42", got["i"])
	require.Equal(t, "42", got["u"])
	require.Equal(t, "true", got["b"])
	require.Equal(t, ts.Format(time.RFC3339Nano), got["dt"])
	require.Equal(t, "raw-value", got["null"])

	// nil value and an unset oneof carry nothing to render → dropped.
	_, ok := got["nilval"]
	require.False(t, ok, "nil value must be skipped")
	_, ok = got["unsetval"]
	require.False(t, ok, "unset oneof must be skipped")

	// No key is lost except the two that genuinely carry no value.
	require.Len(t, got, 6)
}

// TestMetadataToMap_DatetimeIsUTC pins the DATETIME rendering to RFC3339Nano in
// UTC regardless of the micros' origin.
func TestMetadataToMap_DatetimeIsUTC(t *testing.T) {
	t.Parallel()

	// 0 micros == Unix epoch.
	got := MetadataToMap(map[string]*MetadataValue{
		"dt": {Type: &MetadataValue_DatetimeValue{DatetimeValue: 0}},
	})
	require.Equal(t, "1970-01-01T00:00:00Z", got["dt"])
}

// TestMetadataFromMapToMap_RoundTripsStrings verifies string values survive a
// from→to round trip unchanged (the MetadataFromMap writer only emits strings).
func TestMetadataFromMapToMap_RoundTripsStrings(t *testing.T) {
	t.Parallel()

	original := map[string]string{"a": "1", "b": "two", "c": ""}
	require.Equal(t, original, MetadataToMap(MetadataFromMap(original)))
}

// TestMetadataToMap_NilMap keeps the nil-in / nil-out contract.
func TestMetadataToMap_NilMap(t *testing.T) {
	t.Parallel()

	require.Nil(t, MetadataToMap(nil))
	require.Nil(t, MetadataFromMap(nil))
}
