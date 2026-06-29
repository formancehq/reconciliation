package engine

import (
	"context"
	"encoding/json"
	"math/big"
	"time"
)

// LedgerResolver is the kernel's contract for ledger-backed sources.
// Implementations call the Formance Ledger SDK; tests inject in-memory fakes.
type LedgerResolver interface {
	// AggregateBalance returns per-asset aggregate balance(s) for accounts
	// matched by query at the given PIT. PIT + metadata filtering is safe on
	// supported ledgers (>= v2.4.11, where ledger#1416 is fixed); earlier
	// versions silently returned empty under ACCOUNT_METADATA_HISTORY=DISABLED
	// (see project memory ledger-aggregate-pit-metadata).
	AggregateBalance(ctx context.Context, ledger string, query json.RawMessage, pit time.Time) (map[string]*big.Int, error)

	// ListAccounts returns the accounts matched by the query at the given PIT.
	// Used for per-account templates (account_threshold per_account). The
	// engine enforces a max-accounts-scanned budget before calling.
	ListAccounts(ctx context.Context, ledger string, query json.RawMessage, pit time.Time, limit int) ([]Account, error)
}

// PaymentsResolver is the kernel's contract for payments-pool-backed sources.
//
// V1 deliberately uses the v3 /balances/latest endpoint rather than the legacy
// /api/payments/pools/{id}/balances?at= route — the legacy PIT endpoint silently
// returns empty under payments v3 (see baseline findings). Latest is the
// faithful read of the payments side; PIT semantics across heterogeneous
// systems are handled by tolerances in the template, not by reaching for a
// PIT endpoint that doesn't exist.
type PaymentsResolver interface {
	PoolBalanceLatest(ctx context.Context, poolID string) (map[string]*big.Int, error)
}

// Resolvers groups the resolver impls injected into Engine at construction.
// Optional resolvers (e.g. future ExternalGL) can be added here without
// breaking existing engines — they default to nil and the relevant builtin
// rejects with a clear error.
type Resolvers struct {
	Ledger   LedgerResolver
	Payments PaymentsResolver
}
