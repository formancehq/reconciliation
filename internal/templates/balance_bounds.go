package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// balance_bounds asserts that one named source's balance stays inside inclusive
// per-asset limits, in the asset's minor units. It is the V2 form of
// account_threshold's aggregate mode, and the piece that made the V1 catalogue
// migratable rather than merely re-implementable: an equation between sources
// cannot express a bound on a single balance.
//
// It is the one V2 template whose asset universe is **declared** rather than
// discovered, and that is the whole reason it exists in this shape. Every other
// template builds its universe from the assets its sources actually hold
// (unionAssets, via evaluatePerAsset). A bounds rule cannot: a floor of 100 000
// USD on a set that has drained to nothing must keep failing, and an asset with
// no volume is absent from that union — so the outcome would vanish, and the
// service's disappearance sweep would auto-resolve the very alert that matters.
// The keys of Bounds are therefore exactly the assets checked, wildcard or not.
const maxBalanceBoundsAssets = 256

// signedIntegerPattern matches a base-10 integer in minor units. Bounds are
// signed on purpose: a threshold on a liability or obligation set is negative.
var signedIntegerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// BalanceBound is one asset's inclusive limits. Either side may be empty, which
// means unbounded on that side — not zero. At least one must be set.
type BalanceBound struct {
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
}

// BalanceBoundsSpec is the typed spec for balance_bounds.
type BalanceBoundsSpec struct {
	Source V2NamedSource `json:"source"`
	// Bounds is the declared asset universe: its keys are exactly the assets
	// checked, in lexicographic order.
	Bounds map[string]BalanceBound `json:"bounds"`
}

type BalanceBounds struct{}

func NewBalanceBounds() *BalanceBounds { return &BalanceBounds{} }

func (*BalanceBounds) Kind() models.TemplateKind { return models.TemplateBalanceBounds }

// resolvedBound is one asset's parsed limits. A nil side is unbounded.
type resolvedBound struct {
	Min *big.Int
	Max *big.Int
}

// within reports whether value satisfies the bound, and which side it breached.
// Validation guarantees Min <= Max, so at most one side can be breached.
func (b resolvedBound) within(value *big.Int) (bool, string) {
	if b.Min != nil && value.Cmp(b.Min) < 0 {
		return false, "min"
	}
	if b.Max != nil && value.Cmp(b.Max) > 0 {
		return false, "max"
	}
	return true, ""
}

// excursion is how far outside the limits the value sits: negative below the
// floor, positive above the ceiling, zero inside. It occupies the same
// single-scalar "how bad is it" slot as balance_equation's residual.
func (b resolvedBound) excursion(value *big.Int) *big.Int {
	if b.Min != nil && value.Cmp(b.Min) < 0 {
		return new(big.Int).Sub(value, b.Min)
	}
	if b.Max != nil && value.Cmp(b.Max) > 0 {
		return new(big.Int).Sub(value, b.Max)
	}
	return new(big.Int)
}

// resolveBounds parses the whole table. Validate and Evaluate both call it, so
// an accepted spec cannot fail later, and a persisted spec that predates a
// validation change errors loudly instead of quietly emitting no outcomes.
func (spec *BalanceBoundsSpec) resolveBounds() (map[string]resolvedBound, error) {
	if len(spec.Bounds) == 0 {
		return nil, fmt.Errorf("%w: bounds must contain at least one asset (field: bounds)", ErrInvalidSpec)
	}
	if len(spec.Bounds) > maxBalanceBoundsAssets {
		return nil, fmt.Errorf("%w: bounds must contain at most %d assets (got %d) (field: bounds)",
			ErrInvalidSpec, maxBalanceBoundsAssets, len(spec.Bounds))
	}

	out := make(map[string]resolvedBound, len(spec.Bounds))
	for _, asset := range sortedKeys(spec.Bounds) {
		bound := spec.Bounds[asset]
		if asset == AssetWildcard {
			return nil, fmt.Errorf(`%w: %q is not a bounds key — a bound is denominated, so name every asset you want checked and declare asset %q on the source (fields: bounds, source.asset)`,
				ErrInvalidSpec, AssetWildcard, AssetWildcard)
		}
		if !engine.ValidAssetCode(asset) {
			return nil, fmt.Errorf("%w: bounds key %q is not a valid asset code (field: bounds)", ErrInvalidSpec, asset)
		}

		minimum, err := parseSignedInteger(bound.Min, fmt.Sprintf("bounds[%s].min", asset))
		if err != nil {
			return nil, err
		}
		maximum, err := parseSignedInteger(bound.Max, fmt.Sprintf("bounds[%s].max", asset))
		if err != nil {
			return nil, err
		}
		if minimum == nil && maximum == nil {
			return nil, fmt.Errorf("%w: bounds[%s] must set at least one of min or max (fields: bounds[%s].min, bounds[%s].max)",
				ErrInvalidSpec, asset, asset, asset)
		}
		// min == max is legal and means exact equality.
		if minimum != nil && maximum != nil && minimum.Cmp(maximum) > 0 {
			return nil, fmt.Errorf("%w: bounds[%s].min (%s) must be less than or equal to max (%s) (field: bounds[%s].min)",
				ErrInvalidSpec, asset, minimum, maximum, asset)
		}
		out[asset] = resolvedBound{Min: minimum, Max: maximum}
	}
	return out, nil
}

func (t *BalanceBounds) Validate(raw json.RawMessage) error {
	var spec BalanceBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	if err := spec.Source.validateAt("source"); err != nil {
		return err
	}
	if _, err := spec.resolveBounds(); err != nil {
		return err
	}
	// A source naming one denomination can only be bounded in that denomination;
	// bounding several is what the wildcard is for.
	if !spec.Source.wildcard() {
		if _, ok := spec.Bounds[spec.Source.Asset]; !ok || len(spec.Bounds) != 1 {
			return fmt.Errorf("%w: Source %q declares asset %q, so bounds must hold exactly that one key — declare asset %q on the source to bound several assets (fields: source.asset, bounds)",
				ErrInvalidSpec, spec.Source.displayLabel(), spec.Source.Asset, AssetWildcard)
		}
	}
	return nil
}

func (t *BalanceBounds) Queries(raw json.RawMessage) ([]SourceSpec, error) {
	var spec BalanceBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	return []SourceSpec{spec.Source.sourceSpec()}, nil
}

// Explain renders the canonical shape for a single asset — the first the rule
// declares. Per-outcome expressions carry each asset's own limits.
func (t *BalanceBounds) Explain(raw json.RawMessage) (string, error) {
	var spec BalanceBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	bounds, err := spec.resolveBounds()
	if err != nil {
		return "", err
	}
	asset := sortedKeys(bounds)[0]
	return buildBalanceBoundsExpression(spec.Source, asset, bounds[asset]), nil
}

func (t *BalanceBounds) Evaluate(
	ctx context.Context,
	raw json.RawMessage,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	_ engine.EvalInput,
) ([]Outcome, error) {
	var spec BalanceBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return nil, err
	}
	bounds, err := spec.resolveBounds()
	if err != nil {
		return nil, err
	}

	balances, present, err := spec.readBalances(ctx, resolvers, eng.MaxAccountsScanned())
	if err != nil {
		return nil, err
	}

	assets := sortedKeys(bounds)
	outcomes := make([]Outcome, 0, len(assets))
	for _, asset := range assets {
		bound := bounds[asset]
		// A declared asset the source does not hold reads as an explicit zero and
		// is still checked — that is the floor-on-a-drained-set case.
		balance := zeroIfNil(balances[asset])
		passed, breached := bound.within(balance)

		source := resolvedV2Source{
			Spec:    spec.Source.forAsset(asset),
			Balance: balance,
			Present: present[asset],
		}
		evidence := map[string]any{
			"schemaVersion":   2,
			"operation":       "balance_bounds",
			"asset":           asset,
			"source":          source.evidence(),
			"effectiveBounds": bound.evidence(),
			"excursion":       bound.excursion(balance).String(),
			"compiledCEL":     buildBalanceBoundsExpression(spec.Source, asset, bound),
		}
		if !passed {
			evidence["breachedBound"] = breached
		}
		outcomes = append(outcomes, Outcome{
			Fingerprint: fingerprintFor("asset", asset),
			Passed:      passed,
			Evidence:    evidence,
		})
	}
	return outcomes, nil
}

// readBalances reads the source once, returning its per-asset balances and which
// assets it actually holds. Both branches go through the shared V2 resolution so
// `present` means the same thing here as in every other V2 template.
func (spec *BalanceBoundsSpec) readBalances(
	ctx context.Context,
	resolvers engine.Resolvers,
	maxAccounts int,
) (map[string]*big.Int, map[string]bool, error) {
	if spec.Source.wildcard() {
		byID, err := resolveV2SourceBalances(ctx, []V2NamedSource{spec.Source}, resolvers, maxAccounts)
		if err != nil {
			return nil, nil, err
		}
		balances := byID[spec.Source.ID]
		present := make(map[string]bool, len(balances))
		for asset := range balances {
			present[asset] = true
		}
		return balances, present, nil
	}

	resolved, _, err := resolveV2Source(ctx, spec.Source, resolvers, maxAccounts)
	if err != nil {
		return nil, nil, err
	}
	return map[string]*big.Int{spec.Source.Asset: resolved.Balance},
		map[string]bool{spec.Source.Asset: resolved.Present}, nil
}

// evidence renders only the sides the spec declared: an unbounded side is an
// absent key, never a null or a sentinel.
func (b resolvedBound) evidence() map[string]any {
	out := map[string]any{}
	if b.Min != nil {
		out["min"] = b.Min.String()
	}
	if b.Max != nil {
		out["max"] = b.Max.String()
	}
	return out
}

// buildBalanceBoundsExpression renders the kernel form for one asset.
//
// Unlike the other V2 templates this uses plain comparison operators rather than
// a dedicated `…Within` builtin. Those builtins exist because exact rational
// arithmetic cannot be written with CEL operators; an inclusive integer bound
// can, so the rendered predicate stays executable against today's kernel and
// adds no surface to it (ADR-001 keeps the kernel deliberately small). It is the
// same form account_threshold renders, which also keeps a migrated rule's
// compiled_cel recognisable.
func buildBalanceBoundsExpression(source V2NamedSource, asset string, bound resolvedBound) string {
	term := source.forAsset(asset).sourceSpec().celTerm(celString(asset))
	parts := make([]string, 0, 2)
	if bound.Min != nil {
		parts = append(parts, fmt.Sprintf("%s >= %s", term, bound.Min))
	}
	if bound.Max != nil {
		parts = append(parts, fmt.Sprintf("%s <= %s", term, bound.Max))
	}
	return strings.Join(parts, " && ")
}

// parseSignedInteger parses one optional bound. An empty string is unbounded on
// that side and returns (nil, nil) — deliberately not zero.
func parseSignedInteger(value, field string) (*big.Int, error) {
	if value == "" {
		return nil, nil
	}
	if !signedIntegerPattern.MatchString(value) {
		return nil, fmt.Errorf("%w: %s must be a signed base-10 integer string (got %q)", ErrInvalidSpec, field, value)
	}
	digits := strings.TrimPrefix(value, "-")
	if len(digits) > 78 {
		return nil, fmt.Errorf("%w: %s must contain at most 78 digits", ErrInvalidSpec, field)
	}
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a valid integer", ErrInvalidSpec, field)
	}
	return parsed, nil
}
