package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
// (stale_holds, and any rule reading a source per account). It aborts with an error —
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

// Query-validation sentinels. ValidateQuery returns these so the API layer can
// turn a bad data-ledger query into a 400 VALIDATION at rule create instead of
// letting the rule ERROR (and open an engine.error alert) at evaluation.
var (
	// ErrQueryUnsupported is a query the resolver cannot translate — an unknown
	// key, an unsupported operator, or a value of the wrong shape.
	ErrQueryUnsupported = errors.New("data-ledger query: unsupported")

	// ErrQueryIndex is a query the ledger refuses to plan: a metadata[k] filter
	// on a key with no ready, type-compatible accounts index.
	ErrQueryIndex = errors.New("data-ledger query: metadata index not ready")
)

// errProbeStop aborts the validation probe once the ledger has accepted the query
// plan and streamed its first account — the plan is what we validate, not the
// contents, so one row is enough. Swallowed by ValidateQuery, never surfaced.
var errProbeStop = errors.New("probe: stop")

// ValidateQuery reports whether query is translatable and the target ledger can
// actually plan it — the create-time guard against a rule that would only ERROR
// at evaluation (POST /rules). It translates the query (→ ErrQueryUnsupported on a
// bad operator/key/value) then issues a bounded probe read: a metadata filter on a
// key with no ready, type-compatible accounts index fails the plan with gRPC
// FailedPrecondition/InvalidArgument (→ ErrQueryIndex, wrapping the ledger's own
// message, which names the offending key). Zero matches is success; a transient
// transport error (e.g. Unavailable) is returned as-is so the caller treats it as
// 500, not 400. An address-only (or empty) query needs no index and always passes.
func (r *Reader) ValidateQuery(ctx context.Context, ledgerName string, query json.RawMessage) error {
	filter, err := schema.TranslateQuery(query, dataLedgerLeaf)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrQueryUnsupported, err)
	}

	err = r.client.QueryAccountsFunc(ctx, ledgerName, filter, func(*commonpb.Account) error {
		return errProbeStop
	})
	switch {
	case err == nil, errors.Is(err, errProbeStop):
		return nil
	case status.Code(err) == codes.FailedPrecondition, status.Code(err) == codes.InvalidArgument:
		return fmt.Errorf("%w on ledger %q: %v", ErrQueryIndex, ledgerName, err)
	default:
		return err
	}
}

// accountFromProto projects a ledger account onto the engine-free Account view:
// address, string-flattened metadata, and a per-asset balance derived from each
// asset's volumes.
func accountFromProto(ledgerName string, acct *commonpb.Account) Account {
	return Account{
		Address:  acct.GetAddress(),
		Ledger:   ledgerName,
		Metadata: commonpb.MetadataToMap(acct.GetMetadata()),
		Balances: commonpb.BalancesByAsset(acct),
	}
}

// dataLedgerLeaf maps a data-ledger source predicate (the query DSL a template
// emits) to a ledger filter. The operator set mirrors what the ledger's account
// filter supports, subject to the metadata key's declared type + accounts index
// (a rule referencing an unindexed/incompatible key is rejected at create time by
// ValidateQuery, not left to ERROR at evaluation):
//
//   - address:     `$match` only — trailing-`*` prefix, else exact.
//   - metadata[k]: `$match` (string / bool / integer equality), `$gt`/`$gte`/
//     `$lt`/`$lte` (integer or datetime-micros comparison), `$exists` (bool).
//
// Combines with `$and`/`$or`/`$not`, handled by the shared tree-walker
// (schema.TranslateQuery). `$like`/`$in` are intentionally not supported.
func dataLedgerLeaf(op, key string, value any) (*commonpb.QueryFilter, error) {
	switch {
	case key == "address":
		return addressLeaf(op, value)
	case strings.HasPrefix(key, "metadata[") && strings.HasSuffix(key, "]"):
		return metadataLeaf(op, strings.TrimSuffix(strings.TrimPrefix(key, "metadata["), "]"), value)
	default:
		return nil, fmt.Errorf("data-ledger query: unsupported key %q", key)
	}
}

// addressLeaf handles the address selector: `$match` only, with a trailing-`*`
// meaning a prefix match and a plain string an exact address.
func addressLeaf(op string, value any) (*commonpb.QueryFilter, error) {
	if op != "$match" {
		return nil, fmt.Errorf("data-ledger query: address supports only $match, got %s", op)
	}

	s, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("data-ledger query: address expects a string value, got %T", value)
	}

	if trimmed, isPrefix := strings.CutSuffix(s, "*"); isPrefix {
		return schema.FilterAddressPrefix(trimmed), nil
	}

	return schema.FilterAddressExact(s), nil
}

// metadataLeaf maps an operator on metadata[key] to a typed field condition. The
// value's JSON type picks the condition for `$match` (string/bool/int); the
// comparison operators require a numeric value; `$exists` a bool. Whether the key
// is actually queryable (declared type + index) is enforced by the ledger and
// checked up-front by ValidateQuery.
func metadataLeaf(op, key string, value any) (*commonpb.QueryFilter, error) {
	switch op {
	case "$match":
		switch v := value.(type) {
		case string:
			return schema.FilterMetadataString(key, v), nil
		case bool:
			return schema.FilterMetadataBool(key, v), nil
		default:
			n, err := asInt64(key, value)
			if err != nil {
				return nil, err
			}

			return schema.FilterMetadataInt64Range(key, &n, &n, false, false), nil
		}

	case "$gt", "$gte", "$lt", "$lte":
		n, err := asInt64(key, value)
		if err != nil {
			return nil, err
		}

		switch op {
		case "$gt":
			return schema.FilterMetadataInt64Range(key, &n, nil, true, false), nil
		case "$gte":
			return schema.FilterMetadataInt64Range(key, &n, nil, false, false), nil
		case "$lt":
			return schema.FilterMetadataInt64Range(key, nil, &n, false, true), nil
		default: // $lte
			return schema.FilterMetadataInt64Range(key, nil, &n, false, false), nil
		}

	case "$exists":
		b, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("data-ledger query: $exists on metadata[%s] expects a bool value, got %T", key, value)
		}

		exists := schema.FilterMetadataExists(key, false)
		if !b {
			return schema.FilterNot(exists), nil
		}

		return exists, nil

	default:
		return nil, fmt.Errorf("data-ledger query: unsupported operator %q on metadata[%s]", op, key)
	}
}

// asInt64 coerces a query value to int64 for numeric / datetime-micros
// comparisons. JSON numbers decode to float64 (the query walker unmarshals into
// map[string]any), so a non-integral value or one past float64's exact-integer
// range (±2^53) is rejected rather than silently truncated.
func asInt64(key string, value any) (int64, error) {
	f, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("data-ledger query: metadata[%s] expects a numeric value, got %T", key, value)
	}

	if f != math.Trunc(f) {
		return 0, fmt.Errorf("data-ledger query: metadata[%s] expects an integer, got %v", key, f)
	}

	const maxExact = 1 << 53
	if f > maxExact || f < -maxExact {
		return 0, fmt.Errorf("data-ledger query: metadata[%s] value %v exceeds the exact integer range", key, f)
	}

	return int64(f), nil
}
