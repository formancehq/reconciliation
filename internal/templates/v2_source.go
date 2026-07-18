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
	path := fmt.Sprintf("sources[%d]", index)
	if !v2SourceIDPattern.MatchString(s.ID) {
		return fmt.Errorf("%w: Source %q has an invalid ID (field: %s.id)", ErrInvalidSpec, s.displayLabel(), path)
	}
	if s.Asset == "" {
		return fmt.Errorf("%w: Source %q is missing an asset (field: %s.asset)", ErrInvalidSpec, s.displayLabel(), path)
	}
	if !engine.ValidAssetCode(s.Asset) {
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
	return byID, nil
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
		if limit <= 0 {
			return resolvedV2Source{}, 0, fmt.Errorf("resolve Source %q: evaluation budget exceeded: scanned at least %d accounts (limit %d)", source.displayLabel(), limit+1, limit)
		}
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
