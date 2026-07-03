package ledgerschema

import (
	"encoding/json"
	"fmt"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
)

// Query translation: recon's list APIs (query.Builder) and the data-ledger
// resolvers both express filters as the JSON boolean tree query.ParseJSON
// consumes (`{$and|$or|$not|$match|…}`). This walks that tree into a
// commonpb.QueryFilter, preserving the boolean structure. Leaf predicates are
// resource-specific (a rule's id is its address, an alert's id is metadata, a
// data-ledger query uses `address`/`metadata[k]`), so the caller supplies a
// LeafMapper; structural nodes (and/or/not) are handled here.

// LeafMapper maps one leaf predicate (operator, key, value) to a ledger filter.
type LeafMapper func(op, key string, value any) (*commonpb.QueryFilter, error)

// TranslateQuery converts a query JSON tree to a QueryFilter. Returns (nil, nil)
// for an empty query (the caller decides the unconstrained default).
func TranslateQuery(raw json.RawMessage, leaf LeafMapper) (*commonpb.QueryFilter, error) {
	node, err := decodeQueryObject(raw)
	if err != nil {
		return nil, err
	}

	if node == nil {
		return nil, nil
	}

	return translateNode(node, leaf)
}

// decodeQueryObject unmarshals a query into a single-key map, tolerating a value
// that is a JSON string wrapping the object (some template compilers emit that).
func decodeQueryObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	// A string-wrapped object: "{\"$match\":...}" → unwrap once.
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		if asString == "" {
			return nil, nil
		}

		raw = json.RawMessage(asString)
	}

	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, fmt.Errorf("decode query %q: %w", string(raw), err)
	}

	if len(node) == 0 {
		return nil, nil
	}

	return node, nil
}

func translateNode(node map[string]any, leaf LeafMapper) (*commonpb.QueryFilter, error) {
	op, val, err := singleMapKey(node)
	if err != nil {
		return nil, err
	}

	switch op {
	case "$and", "$or":
		items, ok := val.([]any)
		if !ok {
			return nil, fmt.Errorf("%s expects an array", op)
		}

		subs := make([]*commonpb.QueryFilter, 0, len(items))
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s item must be an object", op)
			}

			sub, serr := translateNode(m, leaf)
			if serr != nil {
				return nil, serr
			}

			subs = append(subs, sub)
		}

		if op == "$and" {
			return FilterAll(subs...), nil
		}

		return FilterAny(subs...), nil

	case "$not":
		m, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$not expects an object")
		}

		sub, serr := translateNode(m, leaf)
		if serr != nil {
			return nil, serr
		}

		return FilterNot(sub), nil

	default:
		m, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s expects an object", op)
		}

		key, value, kerr := singleMapKey(m)
		if kerr != nil {
			return nil, kerr
		}

		return leaf(op, key, value)
	}
}

// singleMapKey returns the sole (key, value) of a one-entry map — the shape of
// every query JSON node.
func singleMapKey(m map[string]any) (string, any, error) {
	if len(m) != 1 {
		return "", nil, fmt.Errorf("expected exactly one key, got %d", len(m))
	}

	for k, v := range m {
		return k, v, nil
	}

	return "", nil, nil // unreachable (len checked above)
}
