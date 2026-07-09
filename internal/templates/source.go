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

// SourceSpec is a reusable, typed descriptor of "where to read a per-asset
// balance from". It is the shared primitive under source_parity: a template
// composes one or more sources and expresses its invariant over their resolved
// balances.
//
// V1 reads a ledger account set only (ledger + query). A future heterogeneous
// source (e.g. an external GL, ADR-001 §7) reintroduces a `kind` discriminator
// here, defaulting to "ledger" so it stays non-breaking.
type SourceSpec struct {
	Ledger string          `json:"ledger,omitempty"`
	Query  json.RawMessage `json:"query,omitempty"`
}

// Validate checks the source has its required fields present. Returns
// ErrInvalidSpec-wrapped errors so the API surfaces them as 400 VALIDATION.
// `field` prefixes messages so a caller with multiple sources (left/right) can
// point at the offending one.
func (s SourceSpec) Validate(field string) error {
	if s.Ledger == "" {
		return fmt.Errorf("%w: %s.ledger is required", ErrInvalidSpec, field)
	}
	if !hasMeaningfulJSON(s.Query) {
		return fmt.Errorf("%w: %s.query is required", ErrInvalidSpec, field)
	}
	return nil
}

// resolve reads the per-asset balance map for this source. A ledger source is
// read live — a single aggregate is an internally consistent snapshot (ADR-003).
func (s SourceSpec) resolve(ctx context.Context, resolvers engine.Resolvers) (map[string]*big.Int, error) {
	return resolvers.Ledger.AggregateBalance(ctx, s.Ledger, s.Query)
}

// celTerm renders the kernel expression that reads this source's balance for
// assetExpr (already a CEL expression, e.g. `"USD/2"` or `<asset>`). Mirrors
// the ledgerSet builtin the kernel exposes, so a template can compile + run the
// rendered expression to cross-check its direct computation.
func (s SourceSpec) celTerm(assetExpr string) string {
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
	return "ledger:" + s.Ledger
}
