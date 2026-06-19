package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/formancehq/reconciliation/internal/engine"
)

// ErrResolverUnavailable is returned when a template requires a resolver
// (ledger / payments) the engine wasn't wired with. Distinct from
// ErrInvalidSpec so callers can decide whether the operator's config is
// wrong (404-class) vs the engine's wiring is wrong (500-class).
var ErrResolverUnavailable = errors.New("required resolver is not configured")

// hasMeaningfulJSON returns true iff the RawMessage holds a non-null, non-empty
// JSON value. Marshalling a struct with a nil json.RawMessage field produces
// `null` (4 bytes) on round-trip — so `len(raw) > 0` is not enough; we also
// reject the literal `null` token.
func hasMeaningfulJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return strings.TrimSpace(string(raw)) != "null"
}

// celString safely quotes an arbitrary Go string as a CEL string literal.
// strconv.Quote escapes per Go's rules, which is a strict superset of CEL's
// double-quoted string escapes — so the result is always a valid CEL string.
func celString(s string) string {
	return strconv.Quote(s)
}

// celJSON returns the given json.RawMessage embedded as a CEL string literal.
// Empty/nil queries are emitted as `"{}"` so the LedgerSet resolver receives
// a parseable empty object rather than an empty string.
func celJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return celString("{}")
	}
	return celString(string(raw))
}

// fingerprintFor builds a deterministic fingerprint string from a sequence of
// (axis, value) pairs. The axes are joined by '|' in input order — so callers
// MUST pass axes in the canonical order for the template (e.g. always
// "asset" before "account").
func fingerprintFor(pairs ...string) string {
	if len(pairs)%2 != 0 {
		panic("fingerprintFor: pairs must have even length")
	}
	var out string
	for i := 0; i < len(pairs); i += 2 {
		if i > 0 {
			out += "|"
		}
		out += pairs[i] + ":" + pairs[i+1]
	}
	return out
}

// sortedKeys returns the keys of a map[string]V in lexicographic order. Used
// to keep CEL-string generation and Outcome iteration order deterministic so
// fingerprints and rule.compiled_cel are stable across runs.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// unionAssets returns the lex-sorted union of asset codes across the input maps.
func unionAssets[V any](maps ...map[string]V) []string {
	seen := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

// requireResolvers checks that every resolver the caller names is wired up
// before the template tries to call it. Calling AggregateBalance / etc. on a
// nil interface value panics with a nil-pointer dereference, which surfaces
// to the API as a 500 with no useful detail — this returns a clear error
// instead so the operator sees "engine resolver X is not configured".
func requireResolvers(r engine.Resolvers, needs ...string) error {
	for _, name := range needs {
		switch name {
		case "ledger":
			if r.Ledger == nil {
				return fmt.Errorf("%w: ledger", ErrResolverUnavailable)
			}
		case "payments":
			if r.Payments == nil {
				return fmt.Errorf("%w: payments", ErrResolverUnavailable)
			}
		default:
			return fmt.Errorf("%w: unknown resolver %q", ErrResolverUnavailable, name)
		}
	}
	return nil
}

// unmarshalSpec is a thin wrapper that returns ErrInvalidSpec-wrapped errors so
// every template's Validate produces a uniform error class.
func unmarshalSpec(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return fmt.Errorf("%w: spec is empty", ErrInvalidSpec)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSpec, err)
	}
	return nil
}
