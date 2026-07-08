package engine

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// SourceKind discriminates Source variants. The kernel's resolvers dispatch on
// this; new kinds (e.g. ExternalGL in V2) are added by registering a new
// resolver implementation and a new builtin constructor — the kernel proper
// stays unchanged.
type SourceKind string

const (
	SourceLedgerSet      SourceKind = "ledger_set"
	SourcePaymentsPool   SourceKind = "payments_pool"
	SourceLedgerPostings SourceKind = "ledger_postings" // V1.1+
)

// Source is the first-class kernel primitive (see ADR-001). It is an opaque
// CEL value: built by source-constructor builtins (ledgerSet, pool, …) and
// consumed by aggregator builtins (balance, balances, accounts, …).
//
// The read anchor is not carried per-Source: ledger sources read live (a single
// aggregate is an internally consistent snapshot), and Tier-2 (pool) sources read
// latest, recording their audit PIT in pitPerSource (ADR-003).
type Source struct {
	Kind   SourceKind
	Key    string          // stable key used in pit_per_source map, e.g. "payments_pool:0"
	Ledger string          // LedgerSet, LedgerPostings
	Query  json.RawMessage // LedgerSet, LedgerPostings — the metadata-query JSON
	PoolID string          // PaymentsPool
	Window time.Duration   // LedgerPostings (V1.1+)
}

// celSourceType is the named opaque type CEL uses to type-check source-handling
// expressions. Renames here are breaking changes for post-GA power-mode users.
var celSourceType = types.NewOpaqueType("formance.engine.Source")

// CELType returns the type instance for the opaque source type. Exposed for
// builtin declarations.
func CELType() *types.Type { return celSourceType }

// ConvertToNative satisfies cel-go's ref.Val convention — we return the
// pointer as-is so resolvers can read back the original Go struct.
func (s *Source) ConvertToNative(typeDesc reflect.Type) (any, error) {
	return s, nil
}

// ConvertToType is required by ref.Val; sources don't convert to other CEL
// types. Returning an error value keeps CEL evaluation safe rather than
// surfacing the panic via types.NewErr.
func (s *Source) ConvertToType(typeVal ref.Type) ref.Val {
	if typeVal == celSourceType {
		return s
	}
	return types.NewErr("type conversion not allowed: %s -> %s", celSourceType, typeVal)
}

// Equal compares two sources structurally. Two sources are equal if their
// kind, ledger, query (string-equal), pool id, and window match. PIT is not
// part of identity — it's resolved per-evaluation.
func (s *Source) Equal(other ref.Val) ref.Val {
	o, ok := other.(*Source)
	if !ok {
		return types.False
	}
	if s.Kind != o.Kind || s.Ledger != o.Ledger || s.PoolID != o.PoolID || s.Window != o.Window {
		return types.False
	}
	return types.Bool(string(s.Query) == string(o.Query))
}

// Type returns the CEL type.
func (s *Source) Type() ref.Type { return celSourceType }

// Value returns the underlying Go value (the Source itself).
func (s *Source) Value() any { return s }
