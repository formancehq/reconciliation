package engine

import (
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

// ExactBalance is an opaque CEL value that defers a scalar balance read until
// an exact-arithmetic predicate evaluates it. It avoids converting ledger
// volumes through CEL's int64 or double types.
type ExactBalance struct {
	Source      *Source
	Asset       string
	MetadataKey string
}

var celExactBalanceType = types.NewOpaqueType("formance.engine.ExactBalance")

func (b *ExactBalance) ConvertToNative(reflect.Type) (any, error) { return b, nil }
func (b *ExactBalance) ConvertToType(typeVal ref.Type) ref.Val {
	if typeVal == celExactBalanceType {
		return b
	}
	return types.NewErr("type conversion not allowed: %s -> %s", celExactBalanceType, typeVal)
}
func (b *ExactBalance) Equal(other ref.Val) ref.Val {
	o, ok := other.(*ExactBalance)
	if !ok {
		return types.False
	}
	return types.Bool(b.Asset == o.Asset && b.MetadataKey == o.MetadataKey && b.Source.Equal(o.Source) == types.True)
}
func (b *ExactBalance) Type() ref.Type { return celExactBalanceType }
func (b *ExactBalance) Value() any     { return b }

func (e *evalCtx) makeExactBalance(args ...ref.Val) ref.Val {
	if len(args) != 3 {
		return types.NewErr("exactBalance: expected 3 arguments")
	}
	source, ok := args[0].Value().(*Source)
	if !ok {
		return types.NewErr("exactBalance: source must be Source, got %T", args[0].Value())
	}
	asset, ok := args[1].Value().(string)
	if !ok {
		return types.NewErr("exactBalance: asset must be string, got %T", args[1].Value())
	}
	metadataKey, ok := args[2].Value().(string)
	if !ok {
		return types.NewErr("exactBalance: metadata key must be string, got %T", args[2].Value())
	}
	return &ExactBalance{Source: source, Asset: asset, MetadataKey: metadataKey}
}

func (e *evalCtx) resolveExactBalance(balance *ExactBalance) (*big.Int, error) {
	value, _, err := e.resolveExactBalanceWithPresence(balance)
	return value, err
}

func (e *evalCtx) resolveExactBalanceWithPresence(balance *ExactBalance) (*big.Int, bool, error) {
	if balance.MetadataKey != "" {
		if e.resolvers.Ledger == nil {
			return nil, false, fmt.Errorf("ledger resolver not configured")
		}
		accounts, err := e.resolvers.Ledger.ListAccounts(e.ctx, balance.Source.Ledger, balance.Source.Query, e.budget.limits.MaxAccountsScanned)
		if err != nil {
			return nil, false, err
		}
		value, err := SumAccountMetadataInt(accounts, balance.MetadataKey)
		return value, true, err
	}
	balances, err := e.resolveBalances(balance.Source)
	if err != nil {
		return nil, false, err
	}
	value, present := balances[balance.Asset]
	if !present || value == nil {
		return new(big.Int), false, nil
	}
	return new(big.Int).Set(value), true, nil
}

func (e *evalCtx) balanceEquation(args ...ref.Val) ref.Val {
	if len(args) != 3 {
		return types.NewErr("balanceEquation: expected 3 arguments")
	}
	balances, err := exactBalanceList(args[0])
	if err != nil {
		return types.NewErr("balanceEquation: %v", err)
	}
	coefficients, err := intList(args[1])
	if err != nil {
		return types.NewErr("balanceEquation: %v", err)
	}
	if len(balances) != len(coefficients) {
		return types.NewErr("balanceEquation: balances and coefficients have different lengths")
	}
	toleranceText, ok := args[2].Value().(string)
	if !ok || len(toleranceText) > 78 || !exactIntegerPattern.MatchString(toleranceText) {
		return types.NewErr("balanceEquation: tolerance must be a non-negative integer string of at most 78 digits")
	}
	tolerance, ok := new(big.Int).SetString(toleranceText, 10)
	if !ok || tolerance.Sign() < 0 {
		return types.NewErr("balanceEquation: invalid tolerance %q", toleranceText)
	}
	residual := new(big.Int)
	for i, balance := range balances {
		value, resolveErr := e.resolveExactBalance(balance)
		if resolveErr != nil {
			return types.NewErr("balanceEquation: resolve source %d: %v", i, resolveErr)
		}
		residual.Add(residual, new(big.Int).Mul(value, big.NewInt(coefficients[i])))
	}
	return types.Bool(new(big.Int).Abs(residual).Cmp(tolerance) <= 0)
}

func (e *evalCtx) exchangeRateWithin(args ...ref.Val) ref.Val {
	if len(args) != 4 {
		return types.NewErr("exchangeRateWithin: expected 4 arguments")
	}
	base, ok := args[0].Value().(*ExactBalance)
	if !ok {
		return types.NewErr("exchangeRateWithin: base must be ExactBalance")
	}
	quote, ok := args[1].Value().(*ExactBalance)
	if !ok {
		return types.NewErr("exchangeRateWithin: quote must be ExactBalance")
	}
	min, err := exactPositiveDecimal(args[2])
	if err != nil {
		return types.NewErr("exchangeRateWithin: min: %v", err)
	}
	max, err := exactPositiveDecimal(args[3])
	if err != nil {
		return types.NewErr("exchangeRateWithin: max: %v", err)
	}
	baseValue, err := e.resolveExactBalance(base)
	if err != nil {
		return types.NewErr("exchangeRateWithin: resolve base: %v", err)
	}
	if baseValue.Sign() == 0 {
		return types.False
	}
	quoteValue, err := e.resolveExactBalance(quote)
	if err != nil {
		return types.NewErr("exchangeRateWithin: resolve quote: %v", err)
	}
	observed := new(big.Rat).SetFrac(
		new(big.Int).Mul(quoteValue, exactPow10(exactAssetPrecision(base.Asset))),
		new(big.Int).Mul(baseValue, exactPow10(exactAssetPrecision(quote.Asset))),
	)
	return types.Bool(observed.Cmp(min) >= 0 && observed.Cmp(max) <= 0)
}

func (e *evalCtx) sourceConsensus(args ...ref.Val) ref.Val {
	if len(args) != 2 {
		return types.NewErr("sourceConsensus: expected 2 arguments")
	}
	balances, err := exactBalanceList(args[0])
	if err != nil {
		return types.NewErr("sourceConsensus: %v", err)
	}
	if len(balances) < 2 {
		return types.NewErr("sourceConsensus: expected at least two balances")
	}
	tolerance, err := exactNonNegativeInteger(args[1])
	if err != nil {
		return types.NewErr("sourceConsensus: tolerance: %v", err)
	}

	var minimum, maximum *big.Int
	for i, balance := range balances {
		value, present, resolveErr := e.resolveExactBalanceWithPresence(balance)
		if resolveErr != nil {
			return types.NewErr("sourceConsensus: resolve source %d: %v", i, resolveErr)
		}
		if !present {
			return types.False
		}
		if minimum == nil || value.Cmp(minimum) < 0 {
			minimum = new(big.Int).Set(value)
		}
		if maximum == nil || value.Cmp(maximum) > 0 {
			maximum = new(big.Int).Set(value)
		}
	}

	spread := new(big.Int).Sub(maximum, minimum)
	return types.Bool(spread.Cmp(tolerance) <= 0)
}

func (e *evalCtx) coverageRatioWithin(args ...ref.Val) ref.Val {
	if len(args) != 6 {
		return types.NewErr("coverageRatioWithin: expected 6 arguments")
	}
	numerator, err := e.weightedExactTotal(args[0], args[1], "numerator")
	if err != nil {
		return types.NewErr("coverageRatioWithin: %v", err)
	}
	denominator, err := e.weightedExactTotal(args[2], args[3], "denominator")
	if err != nil {
		return types.NewErr("coverageRatioWithin: %v", err)
	}
	if denominator.Sign() == 0 {
		return types.False
	}
	minimum, err := exactPositiveDecimal(args[4])
	if err != nil {
		return types.NewErr("coverageRatioWithin: min: %v", err)
	}
	maximum, err := exactPositiveDecimal(args[5])
	if err != nil {
		return types.NewErr("coverageRatioWithin: max: %v", err)
	}
	observed := new(big.Rat).SetFrac(numerator, denominator)
	return types.Bool(observed.Cmp(minimum) >= 0 && observed.Cmp(maximum) <= 0)
}

func (e *evalCtx) weightedExactTotal(balanceValue, coefficientValue ref.Val, label string) (*big.Int, error) {
	balances, err := exactBalanceList(balanceValue)
	if err != nil {
		return nil, fmt.Errorf("%s balances: %w", label, err)
	}
	coefficients, err := intList(coefficientValue)
	if err != nil {
		return nil, fmt.Errorf("%s coefficients: %w", label, err)
	}
	if len(balances) != len(coefficients) {
		return nil, fmt.Errorf("%s balances and coefficients have different lengths", label)
	}
	total := new(big.Int)
	for i, balance := range balances {
		value, resolveErr := e.resolveExactBalance(balance)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve %s source %d: %w", label, i, resolveErr)
		}
		total.Add(total, new(big.Int).Mul(value, big.NewInt(coefficients[i])))
	}
	return total, nil
}

func exactBalanceList(value ref.Val) ([]*ExactBalance, error) {
	lister, ok := value.(traits.Lister)
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", value)
	}
	size, ok := lister.Size().Value().(int64)
	if !ok {
		return nil, fmt.Errorf("invalid list size")
	}
	out := make([]*ExactBalance, 0, size)
	for i := int64(0); i < size; i++ {
		item, ok := lister.Get(types.Int(i)).Value().(*ExactBalance)
		if !ok {
			return nil, fmt.Errorf("element %d is not ExactBalance", i)
		}
		out = append(out, item)
	}
	return out, nil
}

func intList(value ref.Val) ([]int64, error) {
	lister, ok := value.(traits.Lister)
	if !ok {
		return nil, fmt.Errorf("expected list, got %T", value)
	}
	size, ok := lister.Size().Value().(int64)
	if !ok {
		return nil, fmt.Errorf("invalid list size")
	}
	out := make([]int64, 0, size)
	for i := int64(0); i < size; i++ {
		item, ok := lister.Get(types.Int(i)).Value().(int64)
		if !ok {
			return nil, fmt.Errorf("element %d is not int", i)
		}
		out = append(out, item)
	}
	return out, nil
}

var exactDecimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
var exactIntegerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

func exactNonNegativeInteger(value ref.Val) (*big.Int, error) {
	text, ok := value.Value().(string)
	if !ok || len(text) > 78 || !exactIntegerPattern.MatchString(text) {
		return nil, fmt.Errorf("must be a non-negative integer string of at most 78 digits")
	}
	integer, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, fmt.Errorf("invalid integer %q", text)
	}
	return integer, nil
}

func exactPositiveDecimal(value ref.Val) (*big.Rat, error) {
	text, ok := value.Value().(string)
	if !ok || len(text) > 78 || !exactDecimalPattern.MatchString(text) {
		return nil, fmt.Errorf("must be a plain decimal string")
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok || r.Sign() <= 0 {
		return nil, fmt.Errorf("must be greater than zero")
	}
	return r, nil
}

func exactAssetPrecision(asset string) int {
	if slash := strings.LastIndexByte(asset, '/'); slash >= 0 {
		precision, _ := strconv.Atoi(asset[slash+1:])
		return precision
	}
	return 0
}

func exactPow10(precision int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
}
