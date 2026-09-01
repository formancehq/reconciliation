package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

var positiveDecimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,18})?$`)

type ExchangeRateBoundsSpec struct {
	Sources     []V2NamedSource `json:"sources"`
	BaseSource  string          `json:"baseSource"`
	QuoteSource string          `json:"quoteSource"`
	Rate        RateConstraint  `json:"rate"`
}

// RateConstraint accepts exactly one shape: explicit inclusive min/max bounds,
// or a target with a symmetric basis-point tolerance.
type RateConstraint struct {
	Target       string `json:"target,omitempty"`
	ToleranceBps *int   `json:"toleranceBps,omitempty"`
	Min          string `json:"min,omitempty"`
	Max          string `json:"max,omitempty"`
}

type ExchangeRateBounds struct{}

func NewExchangeRateBounds() *ExchangeRateBounds { return &ExchangeRateBounds{} }

func (*ExchangeRateBounds) Kind() models.TemplateKind {
	return models.TemplateKind("exchange_rate_bounds")
}

func (t *ExchangeRateBounds) Validate(raw json.RawMessage) error {
	var spec ExchangeRateBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	byID, err := validateV2Sources(spec.Sources)
	if err != nil {
		return err
	}
	if len(spec.Sources) != 2 {
		return fmt.Errorf("%w: exchange_rate_bounds requires exactly two sources", ErrInvalidSpec)
	}
	if _, ok := byID[spec.BaseSource]; !ok {
		return fmt.Errorf("%w: unknown base Source %q (field: baseSource)", ErrInvalidSpec, spec.BaseSource)
	}
	if _, ok := byID[spec.QuoteSource]; !ok {
		return fmt.Errorf("%w: unknown quote Source %q (field: quoteSource)", ErrInvalidSpec, spec.QuoteSource)
	}
	if spec.BaseSource == spec.QuoteSource {
		return fmt.Errorf("%w: baseSource and quoteSource must refer to different sources", ErrInvalidSpec)
	}
	_, _, err = spec.Rate.bounds()
	return err
}

func (*ExchangeRateBounds) Queries(raw json.RawMessage) ([]SourceSpec, error) {
	var spec ExchangeRateBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	return v2Queries(spec.Sources), nil
}

func (t *ExchangeRateBounds) Explain(raw json.RawMessage) (string, error) {
	var spec ExchangeRateBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	min, max, err := spec.Rate.bounds()
	if err != nil {
		return "", err
	}
	return buildExchangeRateExpression(&spec, min, max), nil
}

func (t *ExchangeRateBounds) Evaluate(ctx context.Context, raw json.RawMessage, eng *engine.Engine, resolvers engine.Resolvers, _ engine.EvalInput) ([]Outcome, error) {
	var spec ExchangeRateBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return nil, err
	}
	minRate, maxRate, err := spec.Rate.bounds()
	if err != nil {
		return nil, err
	}
	resolved, err := resolveV2Sources(ctx, spec.Sources, resolvers, eng.MaxAccountsScanned())
	if err != nil {
		return nil, err
	}
	base := resolved[spec.BaseSource]
	quote := resolved[spec.QuoteSource]
	expression := buildExchangeRateExpression(&spec, minRate, maxRate)
	evidence := map[string]any{
		"schemaVersion": 2,
		"operation":     "exchange_rate_bounds",
		"base":          base.evidence(),
		"quote":         quote.evidence(),
		"effectiveBounds": map[string]any{
			"min": ratDecimal(minRate),
			"max": ratDecimal(maxRate),
		},
		"compiledCEL": expression,
	}
	fingerprint := fingerprintFor("baseSource", base.Spec.ID, "quoteSource", quote.Spec.ID)
	if base.Balance.Sign() == 0 {
		evidence["undefinedReason"] = "base_balance_zero"
		return []Outcome{{Fingerprint: fingerprint, Passed: false, Evidence: evidence}}, nil
	}

	observed := observedRate(base, quote)
	evidence["observedRate"] = map[string]any{
		"numerator":   observed.Num().String(),
		"denominator": observed.Denom().String(),
	}
	passed := observed.Cmp(minRate) >= 0 && observed.Cmp(maxRate) <= 0
	return []Outcome{{Fingerprint: fingerprint, Passed: passed, Evidence: evidence}}, nil
}

func (r RateConstraint) bounds() (*big.Rat, *big.Rat, error) {
	targetShape := r.Target != "" || r.ToleranceBps != nil
	boundsShape := r.Min != "" || r.Max != ""
	if targetShape == boundsShape {
		return nil, nil, fmt.Errorf("%w: rate must set exactly one of target+toleranceBps or min+max", ErrInvalidSpec)
	}
	if targetShape {
		if r.Target == "" || r.ToleranceBps == nil {
			return nil, nil, fmt.Errorf("%w: rate.target and rate.toleranceBps are both required", ErrInvalidSpec)
		}
		target, err := parsePositiveDecimal(r.Target, "rate.target")
		if err != nil {
			return nil, nil, err
		}
		if *r.ToleranceBps < 0 || *r.ToleranceBps > 10_000 {
			return nil, nil, fmt.Errorf("%w: rate.toleranceBps must be between 0 and 10000", ErrInvalidSpec)
		}
		minFactor := big.NewRat(int64(10_000-*r.ToleranceBps), 10_000)
		maxFactor := big.NewRat(int64(10_000+*r.ToleranceBps), 10_000)
		return new(big.Rat).Mul(target, minFactor), new(big.Rat).Mul(target, maxFactor), nil
	}
	if r.Min == "" || r.Max == "" {
		return nil, nil, fmt.Errorf("%w: rate.min and rate.max are both required", ErrInvalidSpec)
	}
	min, err := parsePositiveDecimal(r.Min, "rate.min")
	if err != nil {
		return nil, nil, err
	}
	max, err := parsePositiveDecimal(r.Max, "rate.max")
	if err != nil {
		return nil, nil, err
	}
	if min.Cmp(max) > 0 {
		return nil, nil, fmt.Errorf("%w: rate.min must be less than or equal to rate.max", ErrInvalidSpec)
	}
	return min, max, nil
}

func parsePositiveDecimal(value, field string) (*big.Rat, error) {
	if len(value) > 78 || !positiveDecimalPattern.MatchString(value) {
		return nil, fmt.Errorf("%w: %s must be a positive plain decimal string with at most 18 fractional digits", ErrInvalidSpec, field)
	}
	valueRat, ok := new(big.Rat).SetString(value)
	if !ok || valueRat.Sign() <= 0 {
		return nil, fmt.Errorf("%w: %s must be greater than zero", ErrInvalidSpec, field)
	}
	return valueRat, nil
}

func assetPrecision(asset string) int {
	if slash := strings.LastIndexByte(asset, '/'); slash >= 0 {
		precision, _ := strconv.Atoi(asset[slash+1:])
		return precision
	}
	return 0
}

func observedRate(base, quote resolvedV2Source) *big.Rat {
	numerator := new(big.Int).Mul(quote.Balance, pow10(assetPrecision(base.Spec.Asset)))
	denominator := new(big.Int).Mul(base.Balance, pow10(assetPrecision(quote.Spec.Asset)))
	return new(big.Rat).SetFrac(numerator, denominator)
}

func pow10(precision int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
}

func ratDecimal(value *big.Rat) string {
	// All accepted/derived rates have a denominator made only of 2 and 5, so
	// at most target-scale+4 digits are sufficient for an exact decimal.
	text := value.FloatString(22)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	return text
}

func buildExchangeRateExpression(spec *ExchangeRateBoundsSpec, minRate, maxRate *big.Rat) string {
	byID := make(map[string]V2NamedSource, len(spec.Sources))
	for _, source := range spec.Sources {
		byID[source.ID] = source
	}
	base := byID[spec.BaseSource]
	quote := byID[spec.QuoteSource]
	return fmt.Sprintf("exchangeRateWithin(%s, %s, %s, %s)",
		base.exactBalanceCEL(), quote.exactBalanceCEL(),
		celString(ratDecimal(minRate)), celString(ratDecimal(maxRate)))
}
