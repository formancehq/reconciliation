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
// PoolBalance reads a pool's per-asset balance. A nil pit reads the current
// snapshot (GET /v3/pools/{id}/balances/latest); a non-nil pit reads the
// balance valid at that instant (GET /v3/pools/{id}/balances?at=). Payments v3
// implements both faithfully (verified against v3.3.1). Callers pass a pit only
// for genuinely historical reads: a PIT read at ~now falls past the pool's last
// balance movement and returns empty (the balance-window tail), so the "as of
// now" path uses latest. See ADR-002.
type PaymentsResolver interface {
	PoolBalance(ctx context.Context, poolID string, pit *time.Time) (map[string]*big.Int, error)
}

// Resolvers groups the resolver impls injected into Engine at construction.
// Optional resolvers (e.g. future ExternalGL) can be added here without
// breaking existing engines — they default to nil and the relevant builtin
// rejects with a clear error.
type Resolvers struct {
	Ledger   LedgerResolver
	Payments PaymentsResolver
}
