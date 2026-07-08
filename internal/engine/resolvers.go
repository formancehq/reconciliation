package engine

import (
	"context"
	"encoding/json"
	"math/big"
)

// LedgerResolver is the kernel's contract for ledger-backed sources. Reads are
// live: a single aggregate is internally consistent (one server-side snapshot),
// so a rule whose universe is one ledger read is skew-free. Cross-ledger reads
// are per-source (skew absorbed by tolerance) — the atomic multi-ledger cut is a
// future ledger primitive (EN-1480). The observed state is recorded in an
// immutable _recon capture (ADR-003). The production impl is
// internal/ledgerresolver over the ledger gRPC client; tests inject in-memory fakes.
type LedgerResolver interface {
	// AggregateBalance returns per-asset aggregate balance(s) for accounts
	// matched by query, read live.
	AggregateBalance(ctx context.Context, ledger string, query json.RawMessage) (map[string]*big.Int, error)

	// ListAccounts returns the accounts matched by the query, read live. Used for
	// per-account templates (account_threshold / source_parity per_account). The
	// engine enforces a max-accounts-scanned budget (passed as limit); the
	// resolver errors rather than truncating past it.
	ListAccounts(ctx context.Context, ledger string, query json.RawMessage, limit int) ([]Account, error)
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
