package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
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
	// pitPerSource records the PIT each Source actually resolved at, keyed by
	// Source.Key. Persisted on the Evaluation row for audit replay.
	pitPerSource map[string]time.Time
	// sourceCounter assigns stable keys (ledgerSet:0, ledgerSet:1, pool:0, …)
	// so the same expression always names its sources the same way across runs.
	sourceCounter map[SourceKind]int
}

func newEvalCtx(ctx context.Context, pit time.Time, resolvers Resolvers, budget *budgetTracker) *evalCtx {
	return &evalCtx{
		ctx:           ctx,
		pit:           pit,
		resolvers:     resolvers,
		budget:        budget,
		pitPerSource:  map[string]time.Time{},
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
	intMap := types.NewMapType(types.StringType, types.IntType)
	intList := types.NewListType(types.IntType)

	return []cel.EnvOption{
		cel.Function("ledgerSet",
			cel.Overload("ledgerSet_string_string",
				[]*cel.Type{cel.StringType, cel.StringType},
				srcT,
			),
		),
		cel.Function("pool",
			cel.Overload("pool_string",
				[]*cel.Type{cel.StringType},
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
		cel.Function("sum",
			cel.Overload("sum_list_int", []*cel.Type{intList}, cel.IntType),
		),
		cel.Function("abs",
			cel.Overload("abs_int", []*cel.Type{cel.IntType}, cel.IntType),
		),
	}
}

// bindings returns CEL env options carrying both the declarations AND the
// runtime closures over the supplied evalCtx. Built fresh per Evaluate call.
func bindings(e *evalCtx) []cel.EnvOption {
	srcT := celSourceType
	intMap := types.NewMapType(types.StringType, types.IntType)
	intList := types.NewListType(types.IntType)

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
		cel.Function("pool",
			cel.Overload("pool_string",
				[]*cel.Type{cel.StringType},
				srcT,
				cel.UnaryBinding(func(poolID ref.Val) ref.Val {
					return e.makePool(poolID)
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
	src := &Source{
		Kind:   SourceLedgerSet,
		Key:    e.keyFor(SourceLedgerSet),
		Ledger: l,
		Query:  json.RawMessage(q),
		PIT:    e.pit,
	}
	e.pitPerSource[src.Key] = e.pit
	return src
}

func (e *evalCtx) makePool(poolID ref.Val) ref.Val {
	id, ok := poolID.Value().(string)
	if !ok {
		return types.NewErr("pool: id must be string, got %T", poolID.Value())
	}
	src := &Source{
		Kind:   SourcePaymentsPool,
		Key:    e.keyFor(SourcePaymentsPool),
		PoolID: id,
		PIT:    e.pit, // recorded for audit; payments-side resolver reads "latest"
	}
	e.pitPerSource[src.Key] = e.pit
	return src
}

func (e *evalCtx) resolveBalances(src *Source) (map[string]*big.Int, error) {
	switch src.Kind {
	case SourceLedgerSet:
		if e.resolvers.Ledger == nil {
			return nil, fmt.Errorf("ledger resolver not configured")
		}
		return e.resolvers.Ledger.AggregateBalance(e.ctx, src.Ledger, src.Query, src.PIT)
	case SourcePaymentsPool:
		if e.resolvers.Payments == nil {
			return nil, fmt.Errorf("payments resolver not configured")
		}
		return e.resolvers.Payments.PoolBalanceLatest(e.ctx, src.PoolID)
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
