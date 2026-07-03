package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
)

// CheckpointReader reads data ledgers at a query checkpoint — the
// checkpoint-consistent read side of ADR-002. Its methods mirror the (future)
// engine.LedgerResolver contract with `checkpointID uint64` replacing `pit`, so
// it drops into the engine when step 6 flips that interface. checkpointID 0
// reads live state.
type CheckpointReader struct {
	client *Client
}

// NewCheckpointReader builds a reader over the given ledger client.
func NewCheckpointReader(client *Client) *CheckpointReader {
	return &CheckpointReader{client: client}
}

// AggregateBalance returns the per-asset aggregate balance of the accounts in
// ledgerName matching query, read at checkpointID. Passing the same checkpointID
// for ledgers A and B yields a consistent cross-ledger cut.
func (r *CheckpointReader) AggregateBalance(ctx context.Context, ledgerName string, query json.RawMessage, checkpointID uint64) (map[string]*big.Int, error) {
	filter, err := schema.TranslateQuery(query, dataLedgerLeaf)
	if err != nil {
		return nil, fmt.Errorf("translate query for %s: %w", ledgerName, err)
	}

	return r.client.AggregateVolumes(ctx, ledgerName, filter, checkpointID)
}

// dataLedgerLeaf maps a data-ledger source predicate (the v2 query DSL a
// template emits — `address` / `metadata[k]`, all `$match`) to a ledger filter.
func dataLedgerLeaf(op, key string, value any) (*commonpb.QueryFilter, error) {
	if op != "$match" {
		return nil, fmt.Errorf("data-ledger query: %q supports only $match, got %s", key, op)
	}

	s, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("data-ledger query: %q expects a string value, got %T", key, value)
	}

	switch {
	case key == "address":
		// `foo:*` (or any trailing `*`) is a prefix match; a plain address is exact.
		if trimmed, isPrefix := strings.CutSuffix(s, "*"); isPrefix {
			return schema.FilterAddressPrefix(trimmed), nil
		}

		return schema.FilterAddressExact(s), nil
	case strings.HasPrefix(key, "metadata[") && strings.HasSuffix(key, "]"):
		return schema.FilterMetadataString(strings.TrimSuffix(strings.TrimPrefix(key, "metadata["), "]"), s), nil
	default:
		return nil, fmt.Errorf("data-ledger query: unsupported key %q", key)
	}
}
