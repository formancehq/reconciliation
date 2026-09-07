package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"

	"github.com/formancehq/reconciliation/internal/engine"
)

const maxV2Sources = 32

// AssetWildcard declares a source as "every asset this account set holds"
// rather than one named denomination.
//
// It is the named-source model's answer to the V1 templates' per-asset fan-out:
// a V1 rule carries a map of per-asset tolerances and emits one outcome per
// asset, which a model where every source names one asset could not express —
// so migrating a multi-asset V1 rule meant splitting it into one rule per asset.
// With a wildcard the operation fans out instead, aligning sources by asset code
// and emitting the same one-outcome-per-asset shape.
//
// It does not reintroduce the guessing ADR-004 §2 ruled out. Alignment is by
// exact asset code, and a spec must be wholly wildcard or wholly fixed —
// mixing the two is what would require an operation to decide how a USD/2 source
// lines up with an "any asset" one, so it is rejected.
const AssetWildcard = "*"

// wildcard reports whether this source fans out across every asset it holds.
func (s V2NamedSource) wildcard() bool { return s.Asset == AssetWildcard }

// forAsset resolves a wildcard source to one concrete denomination.
//
// Everything an outcome shows a human is about a single asset — its evidence,
// and above all its rendered compiledCEL, which is supposed to be an exact
// record of the predicate that ran. Rendering the spec's sources verbatim under
// a wildcard would put "*" in all of them and give every asset's outcome the
// same expression, which records nothing.
func (s V2NamedSource) forAsset(asset string) V2NamedSource {
	s.Asset = asset
	return s
}

// sourcesForAsset resolves a whole source list to one denomination.
func sourcesForAsset(sources []V2NamedSource, asset string) []V2NamedSource {
	out := make([]V2NamedSource, len(sources))
	for i, source := range sources {
		out[i] = source.forAsset(asset)
	}
	return out
}

var v2SourceIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// V2NamedSource is the common scalar balance input for V2 templates. Unlike
// the V1 left/right source shape, every source has a stable caller-defined ID
// and selects exactly one asset.
type V2NamedSource struct {
	ID          string          `json:"id"`
	Label       string          `json:"label,omitempty"`
	Kind        SourceKind      `json:"kind,omitempty"`
	Ledger      string          `json:"ledger"`
	Query       json.RawMessage `json:"query"`
	MetadataKey string          `json:"metadataKey,omitempty"`
	Asset       string          `json:"asset"`
}

func (s V2NamedSource) sourceSpec() SourceSpec {
	return SourceSpec{
		Kind:        s.Kind,
		Ledger:      s.Ledger,
		Query:       s.Query,
		MetadataKey: s.MetadataKey,
		Asset:       s.Asset,
	}
}

func (s V2NamedSource) effectiveKind() SourceKind { return s.sourceSpec().kind() }

func (s V2NamedSource) exactBalanceCEL() string {
	return fmt.Sprintf("exactBalance(ledgerSet(%s, %s), %s, %s)",
		celString(s.Ledger), celJSON(s.Query), celString(s.Asset), celString(s.MetadataKey))
}

func (s V2NamedSource) displayLabel() string {
	if s.Label != "" {
		return s.Label
	}
	return s.ID
}

func (s V2NamedSource) validate(index int) error {
	return s.validateAt(fmt.Sprintf("sources[%d]", index))
}

// validateAt is validate with a caller-supplied field path, so a template
// carrying a single named source (stale_holds) reports `source.asset` rather
// than `sources[0].asset`.
func (s V2NamedSource) validateAt(path string) error {
	if !v2SourceIDPattern.MatchString(s.ID) {
		return fmt.Errorf("%w: Source %q has an invalid ID (field: %s.id)", ErrInvalidSpec, s.displayLabel(), path)
	}
	if s.Asset == "" {
		return fmt.Errorf("%w: Source %q is missing an asset (field: %s.asset)", ErrInvalidSpec, s.displayLabel(), path)
	}
	if s.wildcard() {
		// A metadata source's value is one scalar under one key; the asset it
		// represents is declared, not discovered, so there is nothing to fan out.
		if s.effectiveKind() == SourceAccountMetadata {
			return fmt.Errorf("%w: Source %q cannot use asset %q for kind %q — a metadata source declares the one asset its key represents (field: %s.asset)",
				ErrInvalidSpec, s.displayLabel(), AssetWildcard, SourceAccountMetadata, path)
		}
	} else if !engine.ValidAssetCode(s.Asset) {
		return fmt.Errorf("%w: Source %q asset %q is not a valid asset code (field: %s.asset)", ErrInvalidSpec, s.displayLabel(), s.Asset, path)
	}
	if s.effectiveKind() == SourceLedger && s.MetadataKey != "" {
		return fmt.Errorf("%w: Source %q must not set metadataKey for kind %q (field: %s.metadataKey)", ErrInvalidSpec, s.displayLabel(), SourceLedger, path)
	}
	if err := s.sourceSpec().validateAs(path, fmt.Sprintf("%q", s.displayLabel())); err != nil {
		return err
	}
	return nil
}

func validateV2Sources(sources []V2NamedSource) (map[string]int, error) {
	if len(sources) < 2 {
		return nil, fmt.Errorf("%w: sources must contain at least two entries", ErrInvalidSpec)
	}
	if len(sources) > maxV2Sources {
		return nil, fmt.Errorf("%w: sources must contain at most %d entries", ErrInvalidSpec, maxV2Sources)
	}
	byID := make(map[string]int, len(sources))
	for i, source := range sources {
		if err := source.validate(i); err != nil {
			return nil, err
		}
		if previous, exists := byID[source.ID]; exists {
			return nil, fmt.Errorf("%w: Source ID %q is duplicated (fields: sources[%d].id, sources[%d].id)", ErrInvalidSpec, source.ID, previous, i)
		}
		byID[source.ID] = i
	}
	if err := requireUniformAssetMode(sources); err != nil {
		return nil, err
	}
	return byID, nil
}

// requireUniformAssetMode rejects a spec that mixes wildcard and named-asset
// sources: aligning "every asset" against one fixed denomination is exactly the
// guess the named-source model exists to avoid.
func requireUniformAssetMode(sources []V2NamedSource) error {
	wildcards := 0
	for _, source := range sources {
		if source.wildcard() {
			wildcards++
		}
	}
	if wildcards != 0 && wildcards != len(sources) {
		return fmt.Errorf("%w: either every source declares asset %q or none does — a mixed spec has no defined alignment (field: sources[].asset)",
			ErrInvalidSpec, AssetWildcard)
	}
	return nil
}

// wildcardSources reports whether this spec fans out per asset. Validation has
// already established the sources agree, so the first one decides.
func wildcardSources(sources []V2NamedSource) bool {
	return len(sources) > 0 && sources[0].wildcard()
}

type resolvedV2Source struct {
	Spec    V2NamedSource
	Balance *big.Int
	Present bool
}

func resolveV2Sources(ctx context.Context, sources []V2NamedSource, resolvers engine.Resolvers, maxAccounts int) (map[string]resolvedV2Source, error) {
	resolved := make(map[string]resolvedV2Source, len(sources))
	remaining := maxAccounts
	for _, source := range sources {
		value, scanned, err := resolveV2Source(ctx, source, resolvers, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= scanned
		resolved[source.ID] = value
	}
	return resolved, nil
}

func resolveV2Source(ctx context.Context, source V2NamedSource, resolvers engine.Resolvers, limit int) (resolvedV2Source, int, error) {
	spec := source.sourceSpec()
	if spec.kind() == SourceAccountMetadata {
		accounts, err := resolvers.Ledger.ListAccounts(ctx, spec.Ledger, spec.Query, limit)
		if err != nil {
			return resolvedV2Source{}, 0, fmt.Errorf("resolve Source %q: %w", source.displayLabel(), err)
		}
		total, err := engine.SumAccountMetadataInt(accounts, spec.MetadataKey)
		if err != nil {
			return resolvedV2Source{}, 0, fmt.Errorf("resolve Source %q: %w", source.displayLabel(), err)
		}
		return resolvedV2Source{Spec: source, Balance: total, Present: len(accounts) > 0}, len(accounts), nil
	}

	balances, err := spec.resolve(ctx, resolvers, limit)
	if err != nil {
		return resolvedV2Source{}, 0, fmt.Errorf("resolve Source %q: %w", source.displayLabel(), err)
	}
	value, present := balances[source.Asset]
	return resolvedV2Source{
		Spec:    source,
		Balance: new(big.Int).Set(zeroIfNil(value)),
		Present: present,
	}, 0, nil
}

// evaluatePerAsset runs one operation's arithmetic across the spec's asset
// universe and returns one Outcome per asset.
//
// Fixed-asset specs resolve to exactly one asset — their declared one — so this
// collapses to today's single-outcome behaviour. A wildcard spec resolves every
// source's full balance map and fans out over the union of the assets present,
// which is the shape a V1 rule with a per-asset tolerance map has always had.
//
// An asset a source does not hold contributes an explicit zero with
// present=false, exactly as a missing asset does on a fixed-asset source: the
// operation decides what absence means, this does not decide for it.
func evaluatePerAsset(
	ctx context.Context,
	sources []V2NamedSource,
	resolvers engine.Resolvers,
	maxAccounts int,
	outcome func(asset string, resolved map[string]resolvedV2Source) (Outcome, error),
) ([]Outcome, error) {
	if !wildcardSources(sources) {
		resolved, err := resolveV2Sources(ctx, sources, resolvers, maxAccounts)
		if err != nil {
			return nil, err
		}
		single, err := outcome(sources[0].Asset, resolved)
		if err != nil {
			return nil, err
		}
		return []Outcome{single}, nil
	}

	balances, err := resolveV2SourceBalances(ctx, sources, resolvers, maxAccounts)
	if err != nil {
		return nil, err
	}
	assetMaps := make([]map[string]*big.Int, 0, len(sources))
	for _, source := range sources {
		assetMaps = append(assetMaps, balances[source.ID])
	}

	assets := unionAssets(assetMaps...)
	outcomes := make([]Outcome, 0, len(assets))
	for _, asset := range assets {
		resolved := make(map[string]resolvedV2Source, len(sources))
		for _, source := range sources {
			value, present := balances[source.ID][asset]
			// The per-asset view keeps the source's declared asset honest: it is
			// this asset, not the wildcard, that the arithmetic and the evidence
			// are about.
			spec := source
			spec.Asset = asset
			resolved[source.ID] = resolvedV2Source{
				Spec:    spec,
				Balance: new(big.Int).Set(zeroIfNil(value)),
				Present: present,
			}
		}
		next, err := outcome(asset, resolved)
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, next)
	}
	return outcomes, nil
}

// resolveV2SourceBalances reads every source's full per-asset balance map,
// sharing one accounts budget across them.
func resolveV2SourceBalances(
	ctx context.Context,
	sources []V2NamedSource,
	resolvers engine.Resolvers,
	maxAccounts int,
) (map[string]map[string]*big.Int, error) {
	out := make(map[string]map[string]*big.Int, len(sources))
	remaining := maxAccounts
	for _, source := range sources {
		spec := source.sourceSpec()
		balances, err := spec.resolve(ctx, resolvers, remaining)
		if err != nil {
			return nil, fmt.Errorf("resolve Source %q: %w", source.displayLabel(), err)
		}
		out[source.ID] = balances
	}
	return out, nil
}

func (r resolvedV2Source) evidence() map[string]any {
	out := map[string]any{
		"id":      r.Spec.ID,
		"kind":    string(r.Spec.effectiveKind()),
		"asset":   r.Spec.Asset,
		"balance": r.Balance.String(),
		"present": r.Present,
	}
	if r.Spec.Label != "" {
		out["label"] = r.Spec.Label
	}
	return out
}

func v2Queries(sources []V2NamedSource) []SourceSpec {
	out := make([]SourceSpec, 0, len(sources))
	for _, source := range sources {
		out = append(out, source.sourceSpec())
	}
	return out
}
