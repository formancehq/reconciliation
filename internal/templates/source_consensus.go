package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// SourceConsensusSpec checks that independent records of the same asset all
// exist and agree within one symmetric max-minus-min tolerance.
type SourceConsensusSpec struct {
	Sources   []V2NamedSource `json:"sources"`
	Tolerance string          `json:"tolerance"`
}

type SourceConsensus struct{}

func NewSourceConsensus() *SourceConsensus { return &SourceConsensus{} }

func (*SourceConsensus) Kind() models.TemplateKind { return models.TemplateSourceConsensus }

func (t *SourceConsensus) Validate(raw json.RawMessage) error {
	var spec SourceConsensusSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	if _, err := validateV2Sources(spec.Sources); err != nil {
		return err
	}
	if err := requireSameSourceAsset(spec.Sources, "source_consensus"); err != nil {
		return err
	}
	_, err := parseNonNegativeInteger(spec.Tolerance, "tolerance")
	return err
}

func (*SourceConsensus) Queries(raw json.RawMessage) ([]SourceSpec, error) {
	var spec SourceConsensusSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	return v2Queries(spec.Sources), nil
}

func (*SourceConsensus) Explain(raw json.RawMessage) (string, error) {
	var spec SourceConsensusSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	balances := make([]string, 0, len(spec.Sources))
	for _, source := range spec.Sources {
		balances = append(balances, source.exactBalanceCEL())
	}
	return fmt.Sprintf("sourceConsensus([%s], %s)", strings.Join(balances, ", "), celString(spec.Tolerance)), nil
}

func (t *SourceConsensus) Evaluate(ctx context.Context, raw json.RawMessage, eng *engine.Engine, resolvers engine.Resolvers, _ engine.EvalInput) ([]Outcome, error) {
	var spec SourceConsensusSpec
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

	expression, _ := t.Explain(raw)

	return evaluatePerAsset(ctx, spec.Sources, resolvers, eng.MaxAccountsScanned(),
		func(asset string, resolvedSources map[string]resolvedV2Source) (Outcome, error) {
			return sourceConsensusOutcome(&spec, asset, resolvedSources, tolerance, expression), nil
		})
}

// sourceConsensusOutcome is the per-asset arithmetic: every source must be
// present and the widest spread must stay within tolerance.
func sourceConsensusOutcome(
	spec *SourceConsensusSpec,
	asset string,
	resolvedSources map[string]resolvedV2Source,
	tolerance *big.Int,
	expression string,
) Outcome {
	sourceEvidence := make([]map[string]any, 0, len(spec.Sources))
	missing := make([]string, 0)
	var minimum, maximum *resolvedV2Source
	for _, source := range spec.Sources {
		resolved := resolvedSources[source.ID]
		sourceEvidence = append(sourceEvidence, resolved.evidence())
		if !resolved.Present {
			missing = append(missing, source.ID)
		}
		if minimum == nil || resolved.Balance.Cmp(minimum.Balance) < 0 {
			candidate := resolved
			minimum = &candidate
		}
		if maximum == nil || resolved.Balance.Cmp(maximum.Balance) > 0 {
			candidate := resolved
			maximum = &candidate
		}
	}

	spread := new(big.Int).Sub(maximum.Balance, minimum.Balance)
	return Outcome{
		Fingerprint: fingerprintFor("asset", asset),
		Passed:      len(missing) == 0 && spread.Cmp(tolerance) <= 0,
		Evidence: map[string]any{
			"schemaVersion":  2,
			"operation":      "source_consensus",
			"asset":          asset,
			"sources":        sourceEvidence,
			"minimumSource":  minimum.Spec.ID,
			"minimumBalance": minimum.Balance.String(),
			"maximumSource":  maximum.Spec.ID,
			"maximumBalance": maximum.Balance.String(),
			"spread":         spread.String(),
			"tolerance":      tolerance.String(),
			"missingSources": missing,
			"compiledCEL":    expression,
		},
	}
}

func requireSameSourceAsset(sources []V2NamedSource, operation string) error {
	asset := sources[0].Asset
	for i := 1; i < len(sources); i++ {
		if sources[i].Asset != asset {
			return fmt.Errorf("%w: %s sources must declare the same asset (field: sources[%d].asset)", ErrInvalidSpec, operation, i)
		}
	}
	return nil
}
