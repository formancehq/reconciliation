package commonpb

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

// MetadataToMap converts a proto metadata map to map[string]string (string values only).
func MetadataToMap(m map[string]*MetadataValue) map[string]string {
	if m == nil {
		return nil
	}

	result := make(map[string]string, len(m))
	for k, v := range m {
		if v != nil {
			if sv, ok := v.GetType().(*MetadataValue_StringValue); ok {
				result[k] = sv.StringValue
			}
		}
	}

	return result
}

// MetadataMapFromMap converts a map[string]string to a *MetadataMap (for account_metadata fields).
func MetadataMapFromMap(m map[string]string) *MetadataMap {
	if m == nil {
		return nil
	}

	return &MetadataMap{Values: MetadataFromMap(m)}
}
