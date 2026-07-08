package templates

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"encoding/json"

	"github.com/formancehq/reconciliation/internal/engine"
)

// Scope selects how a template reads the accounts a source matches:
//   - aggregate  (default): sum the matched set into one balance per asset.
//     A query matching a single account is the degenerate case.
//   - per_account: fan out — evaluate each matched account individually,
//     producing one Outcome per (account, asset). Only available when every
//     source involved is a ledger source (pools have no per-account
//     breakdown); the account address is the alignment key and fingerprint axis.
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

// SourceKind discriminates where a balance source reads from. Both kinds map to
// an existing resolver AND an existing kernel builtin (ledgerSet / pool), so a
// template built on Source can both compute directly and cross-check against
// the kernel. New kinds (e.g. an external bank/PSP account) slot in here once
// they have a resolver + builtin.
type SourceKind string

const (
	// SourceLedger reads the aggregate balance of a ledger account set
	// (ledger + query) at the evaluation PIT.
	SourceLedger SourceKind = "ledger"
	// SourcePaymentsPool reads the latest balance of a payments pool. Pool
	// balances are always "latest" — payments v3 has no faithful PIT read
	// (see engine.PaymentsResolver); cross-system PIT skew is absorbed by the
	// consuming template's tolerance.
	SourcePaymentsPool SourceKind = "payments_pool"
)

// SourceSpec is a reusable, typed descriptor of "where to read a per-asset
// balance from". It is the shared primitive under source_parity: a template composes one or more sources and
// expresses its invariant over their resolved balances. Only the fields
// relevant to Kind are populated.
type SourceSpec struct {
	Kind SourceKind `json:"kind"`

	// Ledger source fields.
	Ledger string          `json:"ledger,omitempty"`
	Query  json.RawMessage `json:"query,omitempty"`

	// Payments-pool source field.
	PoolID string `json:"poolID,omitempty"`
}

// Validate checks the source is internally consistent: a known kind with its
// required fields present. Returns ErrInvalidSpec-wrapped errors so the API
// surfaces them as 400 VALIDATION. `field` prefixes messages so a caller with
// multiple sources (left/right) can point at the offending one.
func (s SourceSpec) Validate(field string) error {
	switch s.Kind {
	case SourceLedger:
		if s.Ledger == "" {
			return fmt.Errorf("%w: %s.ledger is required for kind %q", ErrInvalidSpec, field, s.Kind)
		}
		if !hasMeaningfulJSON(s.Query) {
			return fmt.Errorf("%w: %s.query is required for kind %q", ErrInvalidSpec, field, s.Kind)
		}
	case SourcePaymentsPool:
		if s.PoolID == "" {
			return fmt.Errorf("%w: %s.poolID is required for kind %q", ErrInvalidSpec, field, s.Kind)
		}
	case "":
		return fmt.Errorf("%w: %s.kind is required", ErrInvalidSpec, field)
	default:
		return fmt.Errorf("%w: %s.kind %q must be one of %q, %q", ErrInvalidSpec, field, s.Kind, SourceLedger, SourcePaymentsPool)
	}
	return nil
}

// resolverNeed names the resolver this source requires, for requireResolvers.
func (s SourceSpec) resolverNeed() string {
	if s.Kind == SourcePaymentsPool {
		return "payments"
	}
	return "ledger"
}

// resolve reads the per-asset balance map for this source. A ledger source is
// read live (a single aggregate is an internally consistent snapshot). A pool
// source is Tier-2: always latest (see SourcePaymentsPool).
func (s SourceSpec) resolve(ctx context.Context, resolvers engine.Resolvers) (map[string]*big.Int, error) {
	switch s.Kind {
	case SourceLedger:
		return resolvers.Ledger.AggregateBalance(ctx, s.Ledger, s.Query)
	case SourcePaymentsPool:
		return resolvers.Payments.PoolBalanceLatest(ctx, s.PoolID)
	default:
		return nil, fmt.Errorf("%w: cannot resolve source kind %q", ErrInvalidSpec, s.Kind)
	}
}

// celTerm renders the kernel expression that reads this source's balance for
// assetExpr (already a CEL expression, e.g. `"USD/2"` or `<asset>`). Mirrors
// the builtins the kernel exposes (ledgerSet / pool), so a template can compile
// + run the rendered expression to cross-check its direct computation.
func (s SourceSpec) celTerm(assetExpr string) string {
	switch s.Kind {
	case SourceLedger:
		return fmt.Sprintf("balance(ledgerSet(%s, %s), %s)", celString(s.Ledger), celJSON(s.Query), assetExpr)
	case SourcePaymentsPool:
		return fmt.Sprintf("balance(pool(%s), %s)", celString(s.PoolID), assetExpr)
	default:
		return ""
	}
}

// supportsPerAccount reports whether this source can fan out per account. Only
// ledger sources can — a payments pool exposes a single aggregate balance with
// no per-account breakdown keyed to ledger addresses.
func (s SourceSpec) supportsPerAccount() bool { return s.Kind == SourceLedger }

// resolveAccounts fans the source out into one balance map per matched account,
// read live. Ledger sources only; pools are aggregate-only (returns
// ErrInvalidSpec). limit is the evaluation's accounts budget — the resolver
// errors rather than silently truncating past it.
func (s SourceSpec) resolveAccounts(ctx context.Context, resolvers engine.Resolvers, limit int) ([]engine.Account, error) {
	if s.Kind != SourceLedger {
		return nil, fmt.Errorf("%w: per-account scope is not supported for source kind %q (pools are aggregate-only)", ErrInvalidSpec, s.Kind)
	}
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

// poolPitPerSource records the audit PIT for each payments-pool source, in the
// order given, keyed to match the kernel's naming ("payments_pool:N"). Ledger
// sources are read live (ADR-003) and are not recorded. This
// reproduces, without a kernel eval, the pit_per_source the cross-check used to
// populate — the templates own it now that they no longer run the CEL kernel.
func poolPitPerSource(pit time.Time, sources ...SourceSpec) map[string]time.Time {
	out := map[string]time.Time{}
	n := 0
	for _, s := range sources {
		if s.Kind == SourcePaymentsPool {
			out[fmt.Sprintf("%s:%d", engine.SourcePaymentsPool, n)] = pit
			n++
		}
	}
	return out
}

// label is a short, human-readable identifier for this source, used in evidence
// so an operator can tell which side of a comparison a balance came from.
func (s SourceSpec) label() string {
	switch s.Kind {
	case SourceLedger:
		return "ledger:" + s.Ledger
	case SourcePaymentsPool:
		return "pool:" + s.PoolID
	default:
		return string(s.Kind)
	}
}
