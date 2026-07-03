package ledgerstore

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/storage"
)

// Query filter translation: recon's list APIs take a go-libs query.Builder; the
// ledger takes a commonpb.QueryFilter. The Builder's concrete node types are
// unexported and Walk() flattens the tree, so we translate via its JSON form
// (the Builder marshals to the same {$and|$or|$not|$match|…} shape query.ParseJSON
// consumes), preserving the boolean structure.

const (
	opMatch = "$match"
	opGt    = "$gt"
	opGte   = "$gte"
	opLt    = "$lt"
	opLte   = "$lte"
)

// buildListFilter scopes a list to prefix and ANDs in the translated query
// (nil qb → prefix only). The boolean-tree walk lives in schema.TranslateQuery;
// alertLeaf/ruleLeaf map the recon-specific leaf predicates.
func buildListFilter(prefix string, qb query.Builder, leaf schema.LeafMapper) (*commonpb.QueryFilter, error) {
	prefixFilter := schema.FilterAddressPrefix(prefix)
	if qb == nil {
		return prefixFilter, nil
	}

	raw, err := json.Marshal(qb)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}

	translated, err := schema.TranslateQuery(raw, leaf)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", storage.ErrInvalidQuery, err)
	}

	if translated == nil {
		return prefixFilter, nil
	}

	return schema.FilterAll(prefixFilter, translated), nil
}

// alertLeaf maps an alert list predicate to a ledger filter over alert:item:*.
func alertLeaf(op, key string, value any) (*commonpb.QueryFilter, error) {
	switch key {
	case "id":
		return metaEqString(op, key, schema.MetaID, value)
	case "status":
		return metaEqString(op, key, schema.MetaStatus, value)
	case "severity":
		return metaEqString(op, key, schema.MetaSeverity, value)
	case "fingerprint":
		return metaEqString(op, key, schema.MetaFingerprint, value)
	case "ruleID":
		return metaEqString(op, key, schema.MetaRuleID, value)
	case "periodID":
		return metaEqString(op, key, schema.MetaPeriod, value)
	case "firstSeenAt":
		return metaDatetime(op, key, schema.MetaFirstSeenAt, value)
	case "lastSeenAt":
		return metaDatetime(op, key, schema.MetaLastSeenAt, value)
	default:
		return nil, fmt.Errorf("%w: unknown alert filter key %q", storage.ErrInvalidQuery, key)
	}
}

// ruleLeaf maps a rule list predicate to a ledger filter over rule:*.
func ruleLeaf(op, key string, value any) (*commonpb.QueryFilter, error) {
	switch key {
	case "id":
		if op != opMatch {
			return nil, opErr(op, key)
		}

		s, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %q expects a string", storage.ErrInvalidQuery, key)
		}

		// A rule's identity is its address, not metadata.
		return schema.FilterAddressPrefix(schema.RuleAccount(s)), nil
	case "name":
		return metaEqString(op, key, schema.MetaName, value)
	case "templateKind":
		return metaEqString(op, key, schema.MetaTemplateKind, value)
	case "enabled":
		return metaEqBool(op, key, schema.MetaEnabled, value)
	case "createdAt":
		return metaDatetime(op, key, schema.MetaCreatedAt, value)
	case "updatedAt":
		return metaDatetime(op, key, schema.MetaUpdatedAt, value)
	default:
		return nil, fmt.Errorf("%w: unknown rule filter key %q", storage.ErrInvalidQuery, key)
	}
}

func metaEqString(op, key, field string, value any) (*commonpb.QueryFilter, error) {
	if op != opMatch {
		return nil, opErr(op, key)
	}

	s, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("%w: %q expects a string value", storage.ErrInvalidQuery, key)
	}

	return schema.FilterMetadataString(field, s), nil
}

func metaEqBool(op, key, field string, value any) (*commonpb.QueryFilter, error) {
	if op != opMatch {
		return nil, opErr(op, key)
	}

	b, ok := value.(bool)
	if !ok {
		return nil, fmt.Errorf("%w: %q expects a bool value", storage.ErrInvalidQuery, key)
	}

	return schema.FilterMetadataBool(field, b), nil
}

// metaDatetime maps a comparison on a datetime metadata field (stored as int64
// micros) to an IntCondition range. Values are RFC3339 strings.
func metaDatetime(op, key, field string, value any) (*commonpb.QueryFilter, error) {
	micros, err := datetimeMicros(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %s", storage.ErrInvalidQuery, key, err)
	}

	switch op {
	case opMatch:
		return schema.FilterMetadataInt64Range(field, &micros, &micros, false, false), nil
	case opGt:
		return schema.FilterMetadataInt64Range(field, &micros, nil, true, false), nil
	case opGte:
		return schema.FilterMetadataInt64Range(field, &micros, nil, false, false), nil
	case opLt:
		return schema.FilterMetadataInt64Range(field, nil, &micros, false, true), nil
	case opLte:
		return schema.FilterMetadataInt64Range(field, nil, &micros, false, false), nil
	default:
		return nil, opErr(op, key)
	}
}

func datetimeMicros(value any) (int64, error) {
	s, ok := value.(string)
	if !ok {
		return 0, fmt.Errorf("datetime value must be an RFC3339 string")
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, fmt.Errorf("invalid RFC3339 datetime %q", s)
	}

	return t.UnixMicro(), nil
}

func opErr(op, key string) error {
	return fmt.Errorf("%w: %q does not support operator %s", storage.ErrInvalidQuery, key, op)
}
