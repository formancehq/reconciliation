package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// evalCtx bundles the per-evaluation state that builtin closures close over.
// Each Engine.Evaluate call builds a fresh evalCtx, then constructs a CEL env
// whose function bindings reference it.
type evalCtx struct {
	ctx       context.Context
	pit       time.Time
	resolvers Resolvers
	budget    *budgetTracker
	// sourceCounter assigns stable keys (ledger_set:0, ledger_set:1, …) so the
	// same expression always names its sources the same way across runs.
	sourceCounter map[SourceKind]int
}

func newEvalCtx(ctx context.Context, pit time.Time, resolvers Resolvers, budget *budgetTracker) *evalCtx {
	return &evalCtx{
		ctx:           ctx,
		pit:           pit,
		resolvers:     resolvers,
		budget:        budget,
		sourceCounter: map[SourceKind]int{},
	}
}

func (e *evalCtx) keyFor(kind SourceKind) string {
	n := e.sourceCounter[kind]
	e.sourceCounter[kind] = n + 1
	return fmt.Sprintf("%s:%d", kind, n)
}

// declarations returns the CEL env options that declare all kernel builtins
// without runtime bindings. Used by the validation env to type-check
// expressions at rule-create time without exercising any resolvers.
func declarations() []cel.EnvOption {
	srcT := celSourceType
	exactBalanceT := celExactBalanceType
	intMap := types.NewMapType(types.StringType, types.IntType)
	intList := types.NewListType(types.IntType)
	exactBalanceList := types.NewListType(exactBalanceT)

	return []cel.EnvOption{
		cel.Function("ledgerSet",
			cel.Overload("ledgerSet_string_string",
				[]*cel.Type{cel.StringType, cel.StringType},
				srcT,
			),
		),
		cel.Function("balance",
			cel.Overload("balance_source", []*cel.Type{srcT}, cel.IntType),
			cel.Overload("balance_source_string", []*cel.Type{srcT, cel.StringType}, cel.IntType),
		),
		cel.Function("balances",
			cel.Overload("balances_source", []*cel.Type{srcT}, intMap),
		),
		cel.Function("metadataInt",
			cel.Overload("metadataInt_source_string", []*cel.Type{srcT, cel.StringType}, cel.IntType),
		),
		cel.Function("sum",
			cel.Overload("sum_list_int", []*cel.Type{intList}, cel.IntType),
		),
		cel.Function("abs",
			cel.Overload("abs_int", []*cel.Type{cel.IntType}, cel.IntType),
		),
		cel.Function("exactBalance",
			cel.Overload("exactBalance_source_string_string",
				[]*cel.Type{srcT, cel.StringType, cel.StringType}, exactBalanceT),
		),
		cel.Function("balanceEquation",
			cel.Overload("balanceEquation_list_list_string",
				[]*cel.Type{exactBalanceList, intList, cel.StringType}, cel.BoolType),
		),
		cel.Function("exchangeRateWithin",
			cel.Overload("exchangeRateWithin_balance_balance_string_string",
				[]*cel.Type{exactBalanceT, exactBalanceT, cel.StringType, cel.StringType}, cel.BoolType),
		),
		cel.Function("sourceConsensus",
			cel.Overload("sourceConsensus_list_string",
				[]*cel.Type{exactBalanceList, cel.StringType}, cel.BoolType),
		),
		cel.Function("coverageRatioWithin",
			cel.Overload("coverageRatioWithin_list_list_list_list_string_string",
				[]*cel.Type{exactBalanceList, intList, exactBalanceList, intList, cel.StringType, cel.StringType}, cel.BoolType),
		),
	}
}

// bindings returns CEL env options carrying both the declarations AND the
// runtime closures over the supplied evalCtx. Built fresh per Evaluate call.
func bindings(e *evalCtx) []cel.EnvOption {
	srcT := celSourceType
	exactBalanceT := celExactBalanceType
	intMap := types.NewMapType(types.StringType, types.IntType)
	intList := types.NewListType(types.IntType)
	exactBalanceList := types.NewListType(exactBalanceT)

	return []cel.EnvOption{
		cel.Function("ledgerSet",
			cel.Overload("ledgerSet_string_string",
				[]*cel.Type{cel.StringType, cel.StringType},
				srcT,
				cel.BinaryBinding(func(ledger, query ref.Val) ref.Val {
					return e.makeLedgerSet(ledger, query)
				}),
			),
		),
		cel.Function("balance",
			cel.Overload("balance_source",
				[]*cel.Type{srcT},
				cel.IntType,
				cel.UnaryBinding(func(src ref.Val) ref.Val {
					return e.balanceSingleAsset(src)
				}),
			),
			cel.Overload("balance_source_string",
				[]*cel.Type{srcT, cel.StringType},
				cel.IntType,
				cel.BinaryBinding(func(src, asset ref.Val) ref.Val {
					return e.balanceByAsset(src, asset)
				}),
			),
		),
		cel.Function("balances",
			cel.Overload("balances_source",
				[]*cel.Type{srcT},
				intMap,
				cel.UnaryBinding(func(src ref.Val) ref.Val {
					return e.balancesAll(src)
				}),
			),
		),
		cel.Function("metadataInt",
			cel.Overload("metadataInt_source_string",
				[]*cel.Type{srcT, cel.StringType},
				cel.IntType,
				cel.BinaryBinding(func(src, key ref.Val) ref.Val {
					return e.metadataIntSum(src, key)
				}),
			),
		),
		cel.Function("sum",
			cel.Overload("sum_list_int",
				[]*cel.Type{intList},
				cel.IntType,
				cel.UnaryBinding(func(list ref.Val) ref.Val {
					return sumList(list)
				}),
			),
		),
		cel.Function("abs",
			cel.Overload("abs_int",
				[]*cel.Type{cel.IntType},
				cel.IntType,
				cel.UnaryBinding(func(v ref.Val) ref.Val {
					i, ok := v.Value().(int64)
					if !ok {
						return types.NewErr("abs: expected int, got %T", v.Value())
					}
					// math.MinInt64's negation overflows back to math.MinInt64
					// — a silent wrap that would let abs() return a negative
					// value. Reject the input instead.
					if i == math.MinInt64 {
						return types.NewErr("abs: input %d overflows int64", i)
					}
					if i < 0 {
						i = -i
					}
					return types.Int(i)
				}),
			),
		),
		cel.Function("exactBalance",
			cel.Overload("exactBalance_source_string_string",
				[]*cel.Type{srcT, cel.StringType, cel.StringType}, exactBalanceT,
				cel.FunctionBinding(e.makeExactBalance)),
		),
		cel.Function("balanceEquation",
			cel.Overload("balanceEquation_list_list_string",
				[]*cel.Type{exactBalanceList, intList, cel.StringType}, cel.BoolType,
				cel.FunctionBinding(e.balanceEquation)),
		),
		cel.Function("exchangeRateWithin",
			cel.Overload("exchangeRateWithin_balance_balance_string_string",
				[]*cel.Type{exactBalanceT, exactBalanceT, cel.StringType, cel.StringType}, cel.BoolType,
				cel.FunctionBinding(e.exchangeRateWithin)),
		),
		cel.Function("sourceConsensus",
			cel.Overload("sourceConsensus_list_string",
				[]*cel.Type{exactBalanceList, cel.StringType}, cel.BoolType,
				cel.FunctionBinding(e.sourceConsensus)),
		),
		cel.Function("coverageRatioWithin",
			cel.Overload("coverageRatioWithin_list_list_list_list_string_string",
				[]*cel.Type{exactBalanceList, intList, exactBalanceList, intList, cel.StringType, cel.StringType}, cel.BoolType,
				cel.FunctionBinding(e.coverageRatioWithin)),
		),
	}
}

// makeLedgerSet builds a LedgerSet Source. Query is accepted as a JSON string;
// callers (templates) serialize the metadata-query JSON before passing it in.
func (e *evalCtx) makeLedgerSet(ledger, query ref.Val) ref.Val {
	l, ok := ledger.Value().(string)
	if !ok {
		return types.NewErr("ledgerSet: ledger must be string, got %T", ledger.Value())
	}
	q, ok := query.Value().(string)
	if !ok {
		return types.NewErr("ledgerSet: query must be string, got %T", query.Value())
	}
	// Ledger source: read live (ADR-003). Key is assigned for stable naming.
	return &Source{
		Kind:   SourceLedgerSet,
		Key:    e.keyFor(SourceLedgerSet),
		Ledger: l,
		Query:  json.RawMessage(q),
	}
}

func (e *evalCtx) resolveBalances(src *Source) (map[string]*big.Int, error) {
	switch src.Kind {
	case SourceLedgerSet:
		if e.resolvers.Ledger == nil {
			return nil, fmt.Errorf("ledger resolver not configured")
		}
		return e.resolvers.Ledger.AggregateBalance(e.ctx, src.Ledger, src.Query)
	default:
		return nil, fmt.Errorf("unsupported source kind: %s", src.Kind)
	}
}

// balanceSingleAsset returns a single int64 balance. If the source has more
// than one asset, returns an error — callers must use balance(source, asset)
// to disambiguate.
func (e *evalCtx) balanceSingleAsset(src ref.Val) ref.Val {
	s, ok := src.Value().(*Source)
	if !ok {
		return types.NewErr("balance: expected Source, got %T", src.Value())
	}
	balances, err := e.resolveBalances(s)
	if err != nil {
		return types.NewErr("balance(%s): %v", s.Kind, err)
	}
	if len(balances) == 0 {
		return types.Int(0)
	}
	if len(balances) > 1 {
		return types.NewErr("balance(%s): source has %d assets; use balance(source, asset) to disambiguate", s.Kind, len(balances))
	}
	for _, v := range balances {
		return bigIntToInt(v)
	}
	return types.Int(0) // unreachable
}

func (e *evalCtx) balanceByAsset(src, asset ref.Val) ref.Val {
	s, ok := src.Value().(*Source)
	if !ok {
		return types.NewErr("balance: expected Source, got %T", src.Value())
	}
	a, ok := asset.Value().(string)
	if !ok {
		return types.NewErr("balance: asset must be string, got %T", asset.Value())
	}
	balances, err := e.resolveBalances(s)
	if err != nil {
		return types.NewErr("balance(%s,%s): %v", s.Kind, a, err)
	}
	v, present := balances[a]
	if !present {
		return types.Int(0)
	}
	return bigIntToInt(v)
}

func (e *evalCtx) balancesAll(src ref.Val) ref.Val {
	s, ok := src.Value().(*Source)
	if !ok {
		return types.NewErr("balances: expected Source, got %T", src.Value())
	}
	balances, err := e.resolveBalances(s)
	if err != nil {
		return types.NewErr("balances(%s): %v", s.Kind, err)
	}
	out := make(map[ref.Val]ref.Val, len(balances))
	for asset, amount := range balances {
		out[types.String(asset)] = bigIntToInt(amount)
	}
	return types.DefaultTypeAdapter.NativeToValue(out)
}

// metadataIntSum is the metadataInt(source, key) builtin: it reads the integer
// metadata field `key` off every account in the source's matched set and returns
// their sum. This is the read primitive for an externally-synced balance stored
// in account metadata rather than posted (a "mirror" account) — see the
// account_metadata source. The Go template path shares SumAccountMetadataInt, so
// the two agree by construction.
func (e *evalCtx) metadataIntSum(src, key ref.Val) ref.Val {
	s, ok := src.Value().(*Source)
	if !ok {
		return types.NewErr("metadataInt: expected Source, got %T", src.Value())
	}
	k, ok := key.Value().(string)
	if !ok {
		return types.NewErr("metadataInt: key must be string, got %T", key.Value())
	}
	if e.resolvers.Ledger == nil {
		return types.NewErr("metadataInt: ledger resolver not configured")
	}
	accts, err := e.resolvers.Ledger.ListAccounts(e.ctx, s.Ledger, s.Query, e.budget.limits.MaxAccountsScanned)
	if err != nil {
		return types.NewErr("metadataInt(%s): %v", k, err)
	}
	total, err := SumAccountMetadataInt(accts, k)
	if err != nil {
		return types.NewErr("metadataInt(%s): %v", k, err)
	}
	return bigIntToInt(total)
}

// SumAccountMetadataInt sums the base-10 integer metadata field `key` across the
// given accounts (minor units, matching ledger amount conventions). Shared by
// the kernel's metadataInt builtin and the templates' Go evaluation path so both
// compute the same value. A matched account missing the key, or holding a
// non-integer value, is an error — a synced balance that didn't populate is a
// real problem, surfaced rather than silently read as zero.
func SumAccountMetadataInt(accts []Account, key string) (*big.Int, error) {
	total := new(big.Int)
	for _, a := range accts {
		raw, present := a.Metadata[key]
		if !present {
			return nil, fmt.Errorf("account %q has no metadata[%s]", a.Address, key)
		}
		n, err := parseMetadataInt(a.Address, key, raw)
		if err != nil {
			return nil, err
		}
		total.Add(total, n)
	}
	return total, nil
}

// parseMetadataInt parses a base-10 integer metadata value (minor units),
// tolerating surrounding whitespace.
func parseMetadataInt(address, key, raw string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(strings.TrimSpace(raw), 10)
	if !ok {
		return nil, fmt.Errorf("account %q metadata[%s]=%q is not a base-10 integer", address, key, raw)
	}
	return n, nil
}

func sumList(list ref.Val) ref.Val {
	iter, ok := list.(interface {
		Iterator() interface {
			HasNext() ref.Val
			Next() ref.Val
		}
	})
	_ = iter
	_ = ok
	// cel-go's traits.Lister exposes Get(idx) + Size(); use that.
	lister, isLister := list.(interface {
		Get(idx ref.Val) ref.Val
		Size() ref.Val
	})
	if !isLister {
		return types.NewErr("sum: expected list, got %T", list)
	}
	sizeV := lister.Size()
	size, _ := sizeV.Value().(int64)
	var total int64
	for i := int64(0); i < size; i++ {
		item := lister.Get(types.Int(i))
		v, ok := item.Value().(int64)
		if !ok {
			return types.NewErr("sum: list element %d is not int (got %T)", i, item.Value())
		}
		total += v
	}
	return types.Int(total)
}

// bigIntToInt converts a *big.Int to a CEL int. Returns an error value if the
// number exceeds int64 range — kernel V1 doesn't ship a bigint CEL type;
// templates are expected to operate within int64. Revisit if a customer
// surfaces overflow in V1.1+.
func bigIntToInt(b *big.Int) ref.Val {
	if b == nil {
		return types.Int(0)
	}
	if !b.IsInt64() {
		return types.NewErr("balance value %s exceeds int64 range", b.String())
	}
	return types.Int(b.Int64())
}
