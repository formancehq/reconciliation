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

// Reader reads data-ledger account universes live for the engine's
// LedgerResolver (ADR-003). A single aggregate is computed against one
// server-side snapshot, so it is internally consistent; the observed state is
// recorded in an immutable _recon capture. Reads are engine-free (return
// ledger.Account) so internal/ledger stays a leaf — the ledgerresolver adapter
// maps to engine.Account.
type Reader struct {
	client *Client
}

// NewReader builds a reader over the given ledger client.
func NewReader(client *Client) *Reader {
	return &Reader{client: client}
}

// Account is a data-ledger account: its address, typed metadata flattened to
// strings, and per-asset balances. Kept engine-free so internal/ledger stays a
// leaf — the engine adapter (internal/ledgerresolver) maps it to engine.Account.
type Account struct {
	Address  string
	Ledger   string
	Metadata map[string]string
	Balances map[string]*big.Int
}

// AggregateBalance returns the per-asset aggregate balance of the accounts in
// ledgerName matching query, read live. A single aggregate is internally
// consistent (one server-side snapshot); cross-ledger reads are per-source and
// their skew is absorbed by the template's tolerance (ADR-003).
func (r *Reader) AggregateBalance(ctx context.Context, ledgerName string, query json.RawMessage) (map[string]*big.Int, error) {
	filter, err := schema.TranslateQuery(query, dataLedgerLeaf)
	if err != nil {
		return nil, fmt.Errorf("translate query for %s: %w", ledgerName, err)
	}

	return r.client.AggregateVolumes(ctx, ledgerName, filter)
}

// ListAccounts returns the accounts in ledgerName matching query, read live,
// each carrying its per-asset balance. Used by per-account templates
// (source_parity / account_threshold per_account). It aborts with an error —
// never silently truncates — once more than limit accounts have been seen,
// enforcing the evaluation's accounts budget mid-stream (no fetch-all).
func (r *Reader) ListAccounts(ctx context.Context, ledgerName string, query json.RawMessage, limit int) ([]Account, error) {
	filter, err := schema.TranslateQuery(query, dataLedgerLeaf)
	if err != nil {
		return nil, fmt.Errorf("translate query for %s: %w", ledgerName, err)
	}

	out := make([]Account, 0, 256)
	if err := r.client.QueryAccountsFunc(ctx, ledgerName, filter, func(acct *commonpb.Account) error {
		out = append(out, accountFromProto(ledgerName, acct))
		if len(out) > limit {
			return fmt.Errorf("listAccounts: matched more than %d accounts on %q (accounts budget)", limit, ledgerName)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return out, nil
}

// accountFromProto projects a ledger account onto the engine-free Account view:
// address, string-flattened metadata, and a per-asset balance derived from each
// asset's volumes.
func accountFromProto(ledgerName string, acct *commonpb.Account) Account {
	balances := make(map[string]*big.Int, len(acct.GetVolumes()))
	for asset, v := range acct.GetVolumes() {
		balances[asset] = volumeBalance(v)
	}

	return Account{
		Address:  acct.GetAddress(),
		Ledger:   ledgerName,
		Metadata: commonpb.MetadataToMap(acct.GetMetadata()),
		Balances: balances,
	}
}

// volumeBalance derives an asset balance from a per-account volume: the
// ledger-provided Balance when present, else input − output. The fields are
// arbitrary-precision integers encoded as decimal strings; an empty or
// unparseable string is treated as 0.
func volumeBalance(v *commonpb.VolumesWithBalance) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	if v.GetBalance() != "" {
		return decimalBig(v.GetBalance())
	}

	return new(big.Int).Sub(decimalBig(v.GetInput()), decimalBig(v.GetOutput()))
}

// decimalBig parses a base-10 big.Int, returning 0 for an empty or malformed
// string (the ledger emits "" for a zero side).
func decimalBig(s string) *big.Int {
	if s == "" {
		return new(big.Int)
	}
	if n, ok := new(big.Int).SetString(s, 10); ok {
		return n
	}

	return new(big.Int)
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
