package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

var nonNegativeIntegerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// BalanceEquationSpec asserts that a signed, integer-weighted sum of named
// balances is zero within an absolute tolerance. A+B=C is represented by
// coefficients +1,+1,-1.
type BalanceEquationSpec struct {
	Sources   []V2NamedSource       `json:"sources"`
	Terms     []BalanceEquationTerm `json:"terms"`
	Tolerance string                `json:"tolerance"`
}

type BalanceEquationTerm struct {
	Source      string `json:"source"`
	Coefficient int64  `json:"coefficient"`
}

type BalanceEquation struct{}

func NewBalanceEquation() *BalanceEquation { return &BalanceEquation{} }

func (*BalanceEquation) Kind() models.TemplateKind { return models.TemplateKind("balance_equation") }

func (t *BalanceEquation) Validate(raw json.RawMessage) error {
	var spec BalanceEquationSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	byID, err := validateV2Sources(spec.Sources)
	if err != nil {
		return err
	}
	if len(spec.Terms) != len(spec.Sources) {
		return fmt.Errorf("%w: terms must reference every source exactly once", ErrInvalidSpec)
	}
	seen := make(map[string]int, len(spec.Terms))
	for i, term := range spec.Terms {
		if _, exists := byID[term.Source]; !exists {
			return fmt.Errorf("%w: unknown Source %q (field: terms[%d].source)", ErrInvalidSpec, term.Source, i)
		}
		if previous, exists := seen[term.Source]; exists {
			return fmt.Errorf("%w: Source %q is referenced more than once (fields: terms[%d].source, terms[%d].source)", ErrInvalidSpec, term.Source, previous, i)
		}
		if term.Coefficient == 0 || term.Coefficient == math.MinInt64 {
			return fmt.Errorf("%w: coefficient must be a non-zero CEL int (field: terms[%d].coefficient)", ErrInvalidSpec, i)
		}
		seen[term.Source] = i
	}
	asset := spec.Sources[0].Asset
	for i := 1; i < len(spec.Sources); i++ {
		if spec.Sources[i].Asset != asset {
			return fmt.Errorf("%w: balance_equation sources must declare the same asset (field: sources[%d].asset)", ErrInvalidSpec, i)
		}
	}
	if _, err := parseNonNegativeInteger(spec.Tolerance, "tolerance"); err != nil {
		return err
	}
	return nil
}

func (*BalanceEquation) Queries(raw json.RawMessage) ([]SourceSpec, error) {
	var spec BalanceEquationSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	return v2Queries(spec.Sources), nil
}

func (t *BalanceEquation) Explain(raw json.RawMessage) (string, error) {
	var spec BalanceEquationSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	return buildBalanceEquationExpression(&spec), nil
}

func (t *BalanceEquation) Evaluate(ctx context.Context, raw json.RawMessage, eng *engine.Engine, resolvers engine.Resolvers, _ engine.EvalInput) ([]Outcome, error) {
	var spec BalanceEquationSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return nil, err
	}
	tolerance, err := parseNonNegativeInteger(spec.Tolerance, "tolerance")
	if err != nil {
		return nil, err
	}

	expression := buildBalanceEquationExpression(&spec)

	// One outcome per asset. A fixed-asset spec has an asset universe of one, so
	// this is unchanged for it; a wildcard spec fans out the way a V1 rule with a
	// per-asset tolerance map always did.
	return evaluatePerAsset(ctx, spec.Sources, resolvers, eng.MaxAccountsScanned(),
		func(asset string, resolved map[string]resolvedV2Source) (Outcome, error) {
			residual := new(big.Int)
			sourceEvidence := make([]map[string]any, 0, len(spec.Terms))
			for _, term := range spec.Terms {
				value := resolved[term.Source]
				contribution := new(big.Int).Mul(value.Balance, big.NewInt(term.Coefficient))
				residual.Add(residual, contribution)
				evidence := value.evidence()
				evidence["coefficient"] = term.Coefficient
				evidence["contribution"] = contribution.String()
				sourceEvidence = append(sourceEvidence, evidence)
			}
			absResidual := new(big.Int).Abs(new(big.Int).Set(residual))
			return Outcome{
				Fingerprint: fingerprintFor("asset", asset),
				Passed:      absResidual.Cmp(tolerance) <= 0,
				Evidence: map[string]any{
					"schemaVersion":    2,
					"operation":        "balance_equation",
					"asset":            asset,
					"sources":          sourceEvidence,
					"residual":         residual.String(),
					"absoluteResidual": absResidual.String(),
					"tolerance":        tolerance.String(),
					"compiledCEL":      expression,
				},
			}, nil
		})
}

func parseNonNegativeInteger(value, field string) (*big.Int, error) {
	if !nonNegativeIntegerPattern.MatchString(value) {
		return nil, fmt.Errorf("%w: %s must be a non-negative base-10 integer string", ErrInvalidSpec, field)
	}
	if len(value) > 78 {
		return nil, fmt.Errorf("%w: %s must contain at most 78 digits", ErrInvalidSpec, field)
	}
	n, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a valid integer", ErrInvalidSpec, field)
	}
	return n, nil
}

func buildBalanceEquationExpression(spec *BalanceEquationSpec) string {
	byID := make(map[string]V2NamedSource, len(spec.Sources))
	for _, source := range spec.Sources {
		byID[source.ID] = source
	}
	balances := make([]string, 0, len(spec.Terms))
	coefficients := make([]string, 0, len(spec.Terms))
	for _, term := range spec.Terms {
		source := byID[term.Source]
		balances = append(balances, source.exactBalanceCEL())
		coefficients = append(coefficients, strconv.FormatInt(term.Coefficient, 10))
	}
	return fmt.Sprintf("balanceEquation([%s], [%s], %s)",
		strings.Join(balances, ", "), strings.Join(coefficients, ", "), celString(spec.Tolerance))
}
