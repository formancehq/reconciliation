package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// CoverageRatioBoundsSpec compares two signed portfolios of the same asset.
// Every named source belongs to exactly one side of the ratio.
type CoverageRatioBoundsSpec struct {
	Sources          []V2NamedSource       `json:"sources"`
	NumeratorTerms   []BalanceEquationTerm `json:"numeratorTerms"`
	DenominatorTerms []BalanceEquationTerm `json:"denominatorTerms"`
	Ratio            RateConstraint        `json:"ratio"`
}

type CoverageRatioBounds struct{}

func NewCoverageRatioBounds() *CoverageRatioBounds { return &CoverageRatioBounds{} }

func (*CoverageRatioBounds) Kind() models.TemplateKind { return models.TemplateCoverageRatioBounds }

func (t *CoverageRatioBounds) Validate(raw json.RawMessage) error {
	var spec CoverageRatioBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	byID, err := validateV2Sources(spec.Sources)
	if err != nil {
		return err
	}
	if err := requireSameSourceAsset(spec.Sources, "coverage_ratio_bounds"); err != nil {
		return err
	}
	if len(spec.NumeratorTerms) == 0 {
		return fmt.Errorf("%w: numeratorTerms must contain at least one entry", ErrInvalidSpec)
	}
	if len(spec.DenominatorTerms) == 0 {
		return fmt.Errorf("%w: denominatorTerms must contain at least one entry", ErrInvalidSpec)
	}
	seen := make(map[string]string, len(spec.Sources))
	if err := validateCoverageTerms(spec.NumeratorTerms, "numeratorTerms", byID, seen); err != nil {
		return err
	}
	if err := validateCoverageTerms(spec.DenominatorTerms, "denominatorTerms", byID, seen); err != nil {
		return err
	}
	if len(seen) != len(spec.Sources) {
		for _, source := range spec.Sources {
			if _, ok := seen[source.ID]; !ok {
				return fmt.Errorf("%w: Source %q is not assigned to a portfolio (field: sources[%d].id)", ErrInvalidSpec, source.ID, byID[source.ID])
			}
		}
	}
	_, _, err = spec.Ratio.bounds()
	return err
}

func validateCoverageTerms(terms []BalanceEquationTerm, field string, byID map[string]int, seen map[string]string) error {
	for i, term := range terms {
		path := fmt.Sprintf("%s[%d]", field, i)
		if _, ok := byID[term.Source]; !ok {
			return fmt.Errorf("%w: unknown Source %q (field: %s.source)", ErrInvalidSpec, term.Source, path)
		}
		if previous, ok := seen[term.Source]; ok {
			return fmt.Errorf("%w: Source %q is assigned more than once (fields: %s, %s.source)", ErrInvalidSpec, term.Source, previous, path)
		}
		if term.Coefficient == 0 || term.Coefficient == math.MinInt64 {
			return fmt.Errorf("%w: coefficient must be a non-zero CEL int (field: %s.coefficient)", ErrInvalidSpec, path)
		}
		seen[term.Source] = path + ".source"
	}
	return nil
}

func (*CoverageRatioBounds) Queries(raw json.RawMessage) ([]SourceSpec, error) {
	var spec CoverageRatioBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	return v2Queries(spec.Sources), nil
}

func (t *CoverageRatioBounds) Explain(raw json.RawMessage) (string, error) {
	var spec CoverageRatioBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	minimum, maximum, err := spec.Ratio.bounds()
	if err != nil {
		return "", err
	}
	byID := make(map[string]V2NamedSource, len(spec.Sources))
	for _, source := range spec.Sources {
		byID[source.ID] = source
	}
	numeratorBalances, numeratorCoefficients := coverageCELTerms(spec.NumeratorTerms, byID)
	denominatorBalances, denominatorCoefficients := coverageCELTerms(spec.DenominatorTerms, byID)
	return fmt.Sprintf("coverageRatioWithin([%s], [%s], [%s], [%s], %s, %s)",
		strings.Join(numeratorBalances, ", "), strings.Join(numeratorCoefficients, ", "),
		strings.Join(denominatorBalances, ", "), strings.Join(denominatorCoefficients, ", "),
		celString(ratDecimal(minimum)), celString(ratDecimal(maximum))), nil
}

func coverageCELTerms(terms []BalanceEquationTerm, byID map[string]V2NamedSource) ([]string, []string) {
	balances := make([]string, 0, len(terms))
	coefficients := make([]string, 0, len(terms))
	for _, term := range terms {
		balances = append(balances, byID[term.Source].exactBalanceCEL())
		coefficients = append(coefficients, strconv.FormatInt(term.Coefficient, 10))
	}
	return balances, coefficients
}

func (t *CoverageRatioBounds) Evaluate(ctx context.Context, raw json.RawMessage, eng *engine.Engine, resolvers engine.Resolvers, _ engine.EvalInput) ([]Outcome, error) {
	var spec CoverageRatioBoundsSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return nil, err
	}
	minimum, maximum, err := spec.Ratio.bounds()
	if err != nil {
		return nil, err
	}
	return evaluatePerAsset(ctx, spec.Sources, resolvers, eng.MaxAccountsScanned(),
		func(asset string, resolved map[string]resolvedV2Source) (Outcome, error) {
			// Rendered per asset so a wildcard rule's outcomes do not all carry
			// the same "*" expression.
			perAsset := spec
			perAsset.Sources = sourcesForAsset(spec.Sources, asset)
			expression, _ := t.Explain(mustSpecJSON(&perAsset))
			return coverageRatioOutcome(&spec, asset, resolved, minimum, maximum, expression), nil
		})
}

// coverageRatioOutcome is the per-asset arithmetic: the exact ratio of two
// signed portfolios against inclusive bounds.
func coverageRatioOutcome(
	spec *CoverageRatioBoundsSpec,
	asset string,
	resolved map[string]resolvedV2Source,
	minimum, maximum *big.Rat,
	expression string,
) Outcome {
	numeratorTotal, numeratorEvidence := coveragePortfolio(spec.NumeratorTerms, resolved)
	denominatorTotal, denominatorEvidence := coveragePortfolio(spec.DenominatorTerms, resolved)
	evidence := map[string]any{
		"schemaVersion": 2,
		"operation":     "coverage_ratio_bounds",
		"asset":         asset,
		"numerator": map[string]any{
			"sources": numeratorEvidence,
			"total":   numeratorTotal.String(),
		},
		"denominator": map[string]any{
			"sources": denominatorEvidence,
			"total":   denominatorTotal.String(),
		},
		"effectiveBounds": map[string]any{
			"min": ratDecimal(minimum),
			"max": ratDecimal(maximum),
		},
		"compiledCEL": expression,
	}
	fingerprint := fingerprintFor("asset", asset)
	if denominatorTotal.Sign() == 0 {
		evidence["undefinedReason"] = "denominator_total_zero"
		return Outcome{Fingerprint: fingerprint, Passed: false, Evidence: evidence}
	}
	observed := new(big.Rat).SetFrac(numeratorTotal, denominatorTotal)
	evidence["observedRatio"] = map[string]any{
		"numerator":   observed.Num().String(),
		"denominator": observed.Denom().String(),
	}
	return Outcome{
		Fingerprint: fingerprint,
		Passed:      observed.Cmp(minimum) >= 0 && observed.Cmp(maximum) <= 0,
		Evidence:    evidence,
	}
}

func coveragePortfolio(terms []BalanceEquationTerm, resolved map[string]resolvedV2Source) (*big.Int, []map[string]any) {
	total := new(big.Int)
	evidence := make([]map[string]any, 0, len(terms))
	for _, term := range terms {
		value := resolved[term.Source]
		contribution := new(big.Int).Mul(value.Balance, big.NewInt(term.Coefficient))
		total.Add(total, contribution)
		sourceEvidence := value.evidence()
		sourceEvidence["coefficient"] = term.Coefficient
		sourceEvidence["contribution"] = contribution.String()
		evidence = append(evidence, sourceEvidence)
	}
	return total, evidence
}
