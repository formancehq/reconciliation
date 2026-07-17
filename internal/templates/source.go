package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/formancehq/reconciliation/internal/engine"
)

// Scope selects how a template reads the accounts a source matches:
//   - aggregate  (default): sum the matched set into one balance per asset.
//     A query matching a single account is the degenerate case.
//   - per_account: fan out — evaluate each matched account individually,
//     producing one Outcome per (account, asset). The account address is the
//     alignment key and fingerprint axis.
type Scope string

const (
	ScopeAggregate  Scope = "aggregate"
	ScopePerAccount Scope = "per_account"
)

// Valid reports whether s is a recognised scope (empty defaults to aggregate).
func (s Scope) Valid() bool {
	switch s {
	case "", ScopeAggregate, ScopePerAccount:
		return true
	default:
		return false
	}
}

// SourceKind discriminates how a source produces its per-asset amount. It is an
// optional discriminator on SourceSpec, defaulting to "ledger" so existing specs
// (which omit it) stay valid — the non-breaking seam ADR-001 §7 reserved.
type SourceKind string

const (
	// SourceLedger (default) reads the posting-derived aggregate balance of a
	// ledger account set (ledger + query).
	SourceLedger SourceKind = "ledger"
	// SourceAccountMetadata reads a scalar synced into account metadata (a
	// "mirror" account whose balance an external connector writes as a metadata
	// value, not as postings). The value of metadataKey — a base-10 integer in
	// the explicitly declared asset's minor units — is summed across the matched
	// accounts. The key is opaque and need not contain the asset code. Reconciles
	// the *sync* against a ledger balance.
	SourceAccountMetadata SourceKind = "account_metadata"
)

// SourceSpec is a reusable, typed descriptor of "where to read a per-asset
// amount from". It is the shared primitive under source_parity: a template
// composes one or more sources and expresses its invariant over their resolved
// balances.
//
// Kind selects the read shape (default "ledger"). For "account_metadata",
// MetadataKey + Asset are required and the amount comes from metadata rather
// than postings. One source describes one metadata key / asset pair.
type SourceSpec struct {
	Kind   SourceKind      `json:"kind,omitempty"`
	Ledger string          `json:"ledger,omitempty"`
	Query  json.RawMessage `json:"query,omitempty"`

	// account_metadata only. MetadataKey is opaque: the asset is declared
	// independently and is not inferred from the key.
	MetadataKey string `json:"metadataKey,omitempty"`
	Asset       string `json:"asset,omitempty"`
}

// kind returns the effective kind, defaulting an empty discriminator to ledger.
func (s SourceSpec) kind() SourceKind {
	if s.Kind == "" {
		return SourceLedger
	}
	return s.Kind
}

// Validate checks the source has its required fields present for its kind.
// Returns ErrInvalidSpec-wrapped errors so the API surfaces them as 400
// VALIDATION. `field` prefixes messages so a caller with multiple sources
// (left/right) can point at the offending one.
func (s SourceSpec) Validate(field string) error {
	if s.Ledger == "" {
		return fmt.Errorf("%w: %s.ledger is required", ErrInvalidSpec, field)
	}
	if !hasMeaningfulJSON(s.Query) {
		return fmt.Errorf("%w: %s.query is required", ErrInvalidSpec, field)
	}
	switch s.kind() {
	case SourceLedger:
		// ledger + query suffice.
	case SourceAccountMetadata:
		if s.MetadataKey == "" {
			return fmt.Errorf("%w: %s.metadataKey is required for kind %q", ErrInvalidSpec, field, SourceAccountMetadata)
		}
		if s.Asset == "" {
			return fmt.Errorf("%w: %s.asset is required for kind %q", ErrInvalidSpec, field, SourceAccountMetadata)
		}
		if !engine.ValidAssetCode(s.Asset) {
			return fmt.Errorf("%w: %s.asset %q is not a valid asset code", ErrInvalidSpec, field, s.Asset)
		}
	default:
		return fmt.Errorf("%w: %s.kind %q must be one of %q, %q", ErrInvalidSpec, field, s.Kind, SourceLedger, SourceAccountMetadata)
	}
	return nil
}

// resolve reads the per-asset amount map for this source, live (ADR-003). A
// ledger source aggregates postings (one internally-consistent snapshot); an
// account_metadata source sums metadataKey across the matched accounts, keyed
// by the declared asset. limit is the accounts budget for the metadata read.
func (s SourceSpec) resolve(ctx context.Context, resolvers engine.Resolvers, limit int) (map[string]*big.Int, error) {
	if s.kind() == SourceAccountMetadata {
		accts, err := resolvers.Ledger.ListAccounts(ctx, s.Ledger, s.Query, limit)
		if err != nil {
			return nil, err
		}
		total, err := engine.SumAccountMetadataInt(accts, s.MetadataKey)
		if err != nil {
			return nil, err
		}
		return map[string]*big.Int{s.Asset: total}, nil
	}
	return resolvers.Ledger.AggregateBalance(ctx, s.Ledger, s.Query)
}

// celTerm renders the kernel expression that reads this source's amount for
// assetExpr (already a CEL expression, e.g. `"USD/2"` or `<asset>`). Mirrors the
// kernel builtins (ledgerSet/balance, metadataInt) so the rendered form
// type-checks against the kernel and cross-checks the direct computation. A
// metadata source's amount is labelled by the declared asset, but its key is
// independent of that asset, so assetExpr is unused for that kind.
func (s SourceSpec) celTerm(assetExpr string) string {
	if s.kind() == SourceAccountMetadata {
		return fmt.Sprintf("metadataInt(ledgerSet(%s, %s), %s)", celString(s.Ledger), celJSON(s.Query), celString(s.MetadataKey))
	}
	return fmt.Sprintf("balance(ledgerSet(%s, %s), %s)", celString(s.Ledger), celJSON(s.Query), assetExpr)
}

// resolveAccounts fans the source out into one balance map per matched account,
// read live. limit is the evaluation's accounts budget — the resolver errors
// rather than silently truncating past it.
func (s SourceSpec) resolveAccounts(ctx context.Context, resolvers engine.Resolvers, limit int) ([]engine.Account, error) {
	return resolvers.Ledger.ListAccounts(ctx, s.Ledger, s.Query, limit)
}

// accountAddressQuery renders the metadata-query JSON selecting exactly one
// account by address. json.Marshal escapes the address safely.
func accountAddressQuery(address string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"$match": map[string]any{"address": address}})
	return b
}

// celTermForAccount renders the kernel term reading one account's balance:
// balance(ledgerSet(ledger, {address: addr}), asset). Used for the per-account
// evidence.compiledCEL (explainability). Ledger sources only.
func (s SourceSpec) celTermForAccount(address, assetExpr string) string {
	return fmt.Sprintf("balance(ledgerSet(%s, %s), %s)", celString(s.Ledger), celJSON(accountAddressQuery(address)), assetExpr)
}

// accountsByAddress indexes resolved accounts by address → per-asset balances,
// so two per-account sources can be aligned by address for comparison.
func accountsByAddress(accts []engine.Account) map[string]map[string]*big.Int {
	out := make(map[string]map[string]*big.Int, len(accts))
	for _, a := range accts {
		out[a.Address] = a.Balances
	}
	return out
}

// label is a short, human-readable identifier for this source, used in evidence
// so an operator can tell which side of a comparison a balance came from.
func (s SourceSpec) label() string {
	if s.kind() == SourceAccountMetadata {
		return fmt.Sprintf("metadata:%s[%s]", s.Ledger, s.MetadataKey)
	}
	return "ledger:" + s.Ledger
}
