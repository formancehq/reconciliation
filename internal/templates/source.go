package templates

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"encoding/json"

	"github.com/formancehq/reconciliation/internal/engine"
)

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
// balance from". It is the shared primitive under source_parity (and, after the
// reshape, ledger_vs_pool_drift): a template composes one or more sources and
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

// resolve reads the per-asset balance map for this source. The ledger source
// honours pit; the pool source is always latest (see SourcePaymentsPool).
func (s SourceSpec) resolve(ctx context.Context, resolvers engine.Resolvers, pit time.Time) (map[string]*big.Int, error) {
	switch s.Kind {
	case SourceLedger:
		return resolvers.Ledger.AggregateBalance(ctx, s.Ledger, s.Query, pit)
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
