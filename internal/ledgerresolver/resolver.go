// Package ledgerresolver adapts the ledger gRPC checkpoint reader to the engine's
// Tier-1 LedgerResolver contract (ADR-002 §6). It is the bridge that keeps
// internal/ledger engine-free and internal/engine transport-free: it imports
// both and maps the engine-free ledger.Account onto engine.Account.
package ledgerresolver

import (
	"context"
	"encoding/json"
	"math/big"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/ledger"
)

// Resolver reads Tier-1 (same-cluster) ledger sources at a query checkpoint via
// a ledger.CheckpointReader, satisfying engine.LedgerResolver.
type Resolver struct {
	reader *ledger.CheckpointReader
}

// New builds a Resolver over the given checkpoint reader.
func New(reader *ledger.CheckpointReader) *Resolver {
	return &Resolver{reader: reader}
}

// AggregateBalance passes through — the reader already returns the per-asset
// aggregate as map[string]*big.Int (no engine type to convert).
func (r *Resolver) AggregateBalance(ctx context.Context, ledgerName string, query json.RawMessage, checkpointID uint64) (map[string]*big.Int, error) {
	return r.reader.AggregateBalance(ctx, ledgerName, query, checkpointID)
}

// ListAccounts reads each matched account at the checkpoint and maps the
// engine-free ledger.Account onto engine.Account. The budget (limit) is enforced
// mid-stream by the reader.
func (r *Resolver) ListAccounts(ctx context.Context, ledgerName string, query json.RawMessage, checkpointID uint64, limit int) ([]engine.Account, error) {
	accts, err := r.reader.ListAccounts(ctx, ledgerName, query, checkpointID, limit)
	if err != nil {
		return nil, err
	}

	return toEngineAccounts(accts), nil
}

// toEngineAccounts maps the engine-free ledger.Account view onto engine.Account.
func toEngineAccounts(accts []ledger.Account) []engine.Account {
	out := make([]engine.Account, len(accts))
	for i, a := range accts {
		out[i] = engine.Account{
			Address:  a.Address,
			Ledger:   a.Ledger,
			Metadata: a.Metadata,
			Balances: a.Balances,
		}
	}

	return out
}

// Compile-time proof the adapter satisfies the kernel's Tier-1 contract.
var _ engine.LedgerResolver = (*Resolver)(nil)
