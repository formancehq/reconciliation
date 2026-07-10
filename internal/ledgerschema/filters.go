package ledgerschema

import "github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"

// QueryFilter builders. CreatePreparedQuery / ExecutePreparedQuery take a
// commonpb.QueryFilter proto (the ledger's filterexpr text parser is server-side
// only), so the client constructs the proto directly. Reused by the provisioner
// (fixed prepared queries) and the filter translator (ad-hoc queries, step 4).

// FilterAddressPrefix matches accounts whose address starts with prefix
// (filterexpr: `address == "<prefix>*"`).
func FilterAddressPrefix(prefix string) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Address{
			Address: &commonpb.AddressMatch{
				Match: &commonpb.AddressMatch_HardcodedPrefix{HardcodedPrefix: prefix},
				Role:  commonpb.AddressRole_ADDRESS_ROLE_ANY,
			},
		},
	}
}

// FilterAddressExact matches exactly one account address (filterexpr:
// `address == "<addr>"`, no prefix semantics).
func FilterAddressExact(address string) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Address{
			Address: &commonpb.AddressMatch{
				Match: &commonpb.AddressMatch_HardcodedExact{HardcodedExact: address},
				Role:  commonpb.AddressRole_ADDRESS_ROLE_ANY,
			},
		},
	}
}

// FilterMetadataString matches `metadata[key] == v`.
func FilterMetadataString(key, v string) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Field{
			Field: &commonpb.FieldCondition{
				Field: &commonpb.FieldRef{Metadata: key},
				Condition: &commonpb.FieldCondition_StringCond{
					StringCond: &commonpb.StringCondition{
						Value: &commonpb.StringCondition_Hardcoded{Hardcoded: v},
					},
				},
			},
		},
	}
}

// FilterMetadataBool matches `metadata[key] == v`.
func FilterMetadataBool(key string, v bool) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Field{
			Field: &commonpb.FieldCondition{
				Field: &commonpb.FieldRef{Metadata: key},
				Condition: &commonpb.FieldCondition_BoolCond{
					BoolCond: &commonpb.BoolCondition{
						Value: &commonpb.BoolCondition_Hardcoded{Hardcoded: v},
					},
				},
			},
		},
	}
}

// FilterMetadataInt64Range matches a signed-int / datetime metadata field
// against a bound range (filterexpr: `metadata[key] > min and metadata[key] <
// max`). Datetime fields are stored as int64 micros and accept int bounds
// verbatim. Nil min/max leaves that side unbounded.
func FilterMetadataInt64Range(key string, min, max *int64, minExclusive, maxExclusive bool) *commonpb.QueryFilter {
	cond := &commonpb.IntCondition{MinExclusive: minExclusive, MaxExclusive: maxExclusive}
	if min != nil {
		cond.Min = min
	}

	if max != nil {
		cond.Max = max
	}

	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Field{
			Field: &commonpb.FieldCondition{
				Field:     &commonpb.FieldRef{Metadata: key},
				Condition: &commonpb.FieldCondition_IntCond{IntCond: cond},
			},
		},
	}
}

// FilterMetadataExists matches accounts that carry `metadata[key]` at all
// (filterexpr: `metadata[key] exists`), regardless of value. includeNull keeps
// keys explicitly set to a null value in the match set; false requires a
// non-null value. Needs the key's accounts index like any other metadata filter.
func FilterMetadataExists(key string, includeNull bool) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Field{
			Field: &commonpb.FieldCondition{
				Field: &commonpb.FieldRef{Metadata: key},
				Condition: &commonpb.FieldCondition_ExistsCond{
					ExistsCond: &commonpb.ExistsCondition{IncludeNull: includeNull},
				},
			},
		},
	}
}

// FilterAll ANDs the given filters (filterexpr: `a and b and ...`).
func FilterAll(filters ...*commonpb.QueryFilter) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_And{And: &commonpb.AndFilter{Filters: filters}},
	}
}

// FilterAny ORs the given filters (filterexpr: `a or b or ...`).
func FilterAny(filters ...*commonpb.QueryFilter) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Or{Or: &commonpb.OrFilter{Filters: filters}},
	}
}

// FilterNot negates a filter (filterexpr: `not (a)`).
func FilterNot(f *commonpb.QueryFilter) *commonpb.QueryFilter {
	return &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Not{Not: &commonpb.NotFilter{Filter: f}},
	}
}
