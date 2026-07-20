package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
)

// zeroIfNil normalises a missing per-asset balance to 0. Shared by every
// template that diffs/aggregates balances across an asset union (a source
// absent for an asset reads as 0).
func zeroIfNil(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return v
}

// ErrResolverUnavailable is returned when a template requires a resolver
// (ledger / payments) the engine wasn't wired with. Distinct from
// ErrInvalidSpec so callers can decide whether the operator's config is
// wrong (404-class) vs the engine's wiring is wrong (500-class).
var ErrResolverUnavailable = errors.New("required resolver is not configured")

// hasMeaningfulJSON returns true iff the RawMessage holds a JSON object. Ledger
// metadata queries are object-shaped; accepting arrays or scalars here would
// defer a client validation error until resolver execution.
func hasMeaningfulJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && object != nil
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

// sourceKeyer assigns each source in a template a stable, unique key of the
// form "<label>#<idx>", where idx counts prior sources sharing that label — so
// two terms on the same ledger get "ledger:x#0" and "ledger:x#1" rather than
// colliding on one label. Keys are handed out in the order the template
// presents its sources, and they key BOTH the EvalInput.SourcePITs override
// lookup (input) and EvaluationResult.PitPerSource (output). Historical PITs
// can therefore be replayed exactly; a Payments latest observation retains the
// same key but has the replay limitation documented in ADR-002.
type sourceKeyer struct{ seen map[string]int }

func newSourceKeyer() *sourceKeyer { return &sourceKeyer{seen: map[string]int{}} }

func (k *sourceKeyer) key(label string) string {
	i := k.seen[label]
	k.seen[label]++
	return fmt.Sprintf("%s#%d", label, i)
}

// effectiveSourcePIT resolves the effective instant a source reads at, and
// whether that instant is an explicit historical PIT (vs the "as of now"
// default). Precedence: a per-source override in EvalInput.SourcePITs wins over
// the evaluation's default PIT. An override always counts as explicit; the
// default is explicit only when the caller supplied `at` (EvalInput.PITExplicit).
// Per-source overrides are persisted effective PITs used for replay, so they
// are returned unchanged. The safety margin is applied only to the default
// evaluation PIT. The explicit bit gates the payments-pool read
// (point-in-time vs latest — see SourceSpec.resolve
// and engine.EvalInput); it is irrelevant to ledger sources, which always read
// at the returned instant.
func effectiveSourcePIT(in engine.EvalInput, key string) (time.Time, bool) {
	if p, ok := in.SourcePITs[key]; ok {
		return p, true
	}
	pit := in.PIT
	if in.SafetyMargin > 0 {
		pit = pit.Add(-in.SafetyMargin)
	}
	return pit, in.PITExplicit
}

// applyKernelVerdicts makes CEL authoritative over values already fetched by
// the template. The expressions contain snapshot values as integer literals,
// so evaluating them cannot repeat remote Ledger or Payments reads. One batch
// also gives every fingerprint a shared wall-clock and CEL-cost budget.
func applyKernelVerdicts(ctx context.Context, eng *engine.Engine, in engine.EvalInput, result *EvaluationResult, expressions []string) error {
	if len(result.Outcomes) != len(expressions) {
		return fmt.Errorf("kernel expression count %d does not match outcome count %d", len(expressions), len(result.Outcomes))
	}
	compiled := make([]*engine.Compiled, 0, len(expressions))
	for i, expression := range expressions {
		if err := ctx.Err(); err != nil {
			return err
		}
		program, err := eng.Compile(expression)
		if err != nil {
			return fmt.Errorf("compile snapshot expression %d: %w", i, err)
		}
		compiled = append(compiled, program)
	}
	outputs, err := eng.EvaluateBatch(ctx, compiled, engine.EvalInput{PIT: in.PIT}, engine.Resolvers{})
	if err != nil {
		return err
	}
	for i, output := range outputs {
		result.Outcomes[i].Passed = output.Passed
		result.CostUnits += output.CostUnits
	}
	return nil
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
