package commonpb

import (
	"strconv"
	"time"
)

// MetadataFromMap converts a map[string]string to a proto metadata map.
func MetadataFromMap(m map[string]string) map[string]*MetadataValue {
	if m == nil {
		return nil
	}

	result := make(map[string]*MetadataValue, len(m))
	for k, v := range m {
		result[k] = &MetadataValue{Type: &MetadataValue_StringValue{StringValue: v}}
	}

	return result
}

// MetadataToMap flattens a proto metadata map to map[string]string, stringifying
// every scalar type. It is lossless in the key set: a field declared as
// INT64/UINT64/DATETIME/BOOL is rendered as its canonical string form rather
// than dropped (the earlier string-only reader silently discarded such keys,
// which would make a typed data-ledger metadata field vanish from a rule's view
// — see F33). DATETIME is rendered RFC3339Nano UTC; a NullValue (a stored value
// the ledger could not coerce to its declared type) yields its preserved raw
// original. A nil value or an unset oneof is skipped.
func MetadataToMap(m map[string]*MetadataValue) map[string]string {
	if m == nil {
		return nil
	}

	result := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := metadataValueString(v); ok {
			result[k] = s
		}
	}

	return result
}

// metadataValueString renders a MetadataValue as a string, reporting false when
// there is nothing to render (nil value or unset oneof).
func metadataValueString(v *MetadataValue) (string, bool) {
	if v == nil {
		return "", false
	}

	switch t := v.GetType().(type) {
	case *MetadataValue_StringValue:
		return t.StringValue, true
	case *MetadataValue_IntValue:
		return strconv.FormatInt(t.IntValue, 10), true
	case *MetadataValue_UintValue:
		return strconv.FormatUint(t.UintValue, 10), true
	case *MetadataValue_BoolValue:
		return strconv.FormatBool(t.BoolValue), true
	case *MetadataValue_DatetimeValue:
		return time.UnixMicro(t.DatetimeValue).UTC().Format(time.RFC3339Nano), true
	case *MetadataValue_NullValue:
		return t.NullValue.GetOriginal(), true
	default:
		return "", false
	}
}

// MetadataMapFromMap converts a map[string]string to a *MetadataMap (for account_metadata fields).
func MetadataMapFromMap(m map[string]string) *MetadataMap {
	if m == nil {
		return nil
	}

	return &MetadataMap{Values: MetadataFromMap(m)}
}
