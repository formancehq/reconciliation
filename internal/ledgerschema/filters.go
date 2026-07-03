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
