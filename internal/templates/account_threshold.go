package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// ThresholdMode controls how account_threshold checks balances.
//
//   - ThresholdAggregate (V1 GA): one outcome per asset; checks the aggregated
//     balance across all accounts in the ledger set.
//   - ThresholdPerAccount (V1.1): one outcome per (asset, account) pair;
//     requires the `accounts(source)` CEL builtin which is not yet shipped.
type ThresholdMode string

const (
	ThresholdAggregate  ThresholdMode = "aggregate"
	ThresholdPerAccount ThresholdMode = "per_account"
)

// ThresholdSpec is the typed spec for account_threshold.
type ThresholdSpec struct {
	Ledger string                     `json:"ledger"`
	Query  json.RawMessage            `json:"query"`
	Mode   ThresholdMode              `json:"mode"`
	Bounds map[string]ThresholdBounds `json:"bounds"`
}

// ThresholdBounds is a per-asset inclusive lower/upper bound. At least one of
// Min or Max must be set; either may be absent for a one-sided check.
type ThresholdBounds struct {
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
}

// AccountThreshold implements Evaluator. V1 GA ships ThresholdAggregate;
// ThresholdPerAccount returns a clear "not yet implemented" error from
// Validate so customers can't create rules the engine can't evaluate.
type AccountThreshold struct{}

func NewAccountThreshold() *AccountThreshold { return &AccountThreshold{} }

func (*AccountThreshold) Kind() models.TemplateKind { return models.TemplateAccountThreshold }

func (t *AccountThreshold) Validate(raw json.RawMessage) error {
	var spec ThresholdSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	if spec.Ledger == "" {
		return fmt.Errorf("%w: ledger is required", ErrInvalidSpec)
	}
	if !hasMeaningfulJSON(spec.Query) {
		return fmt.Errorf("%w: query is required", ErrInvalidSpec)
	}
	if spec.Mode == "" {
		spec.Mode = ThresholdAggregate
	}
	switch spec.Mode {
	case ThresholdAggregate, ThresholdPerAccount:
		// ok
	default:
		return fmt.Errorf("%w: mode must be 'aggregate' or 'per_account' (got %q)", ErrInvalidSpec, spec.Mode)
	}
	if len(spec.Bounds) == 0 {
		return fmt.Errorf("%w: bounds must contain at least one asset", ErrInvalidSpec)
	}
	for asset, b := range spec.Bounds {
		if b.Min == nil && b.Max == nil {
			return fmt.Errorf("%w: bounds[%s] must set at least one of min or max", ErrInvalidSpec, asset)
		}
		if b.Min != nil && b.Max != nil && *b.Min > *b.Max {
			return fmt.Errorf("%w: bounds[%s].min (%d) must be <= max (%d)", ErrInvalidSpec, asset, *b.Min, *b.Max)
		}
	}
	return nil
}

func (t *AccountThreshold) Explain(raw json.RawMessage) (string, error) {
	var spec ThresholdSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	assets := sortedKeys(spec.Bounds)
	if len(assets) == 0 {
		return "", fmt.Errorf("%w: bounds is empty — cannot pick a representative asset", ErrInvalidSpec)
	}
	return buildThresholdExpression(&spec, assets[0]), nil
}

func (t *AccountThreshold) Evaluate(
	ctx context.Context,
	raw json.RawMessage,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	in engine.EvalInput,
) ([]Outcome, error) {
	var spec ThresholdSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	if spec.Mode == "" {
		spec.Mode = ThresholdAggregate
	}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return nil, err
	}

	pit := in.PIT
	if in.SafetyMargin > 0 {
		pit = pit.Add(-in.SafetyMargin)
	}
	src := SourceSpec{Kind: SourceLedger, Ledger: spec.Ledger, Query: spec.Query}

	if spec.Mode == ThresholdPerAccount {
		return t.evaluatePerAccount(ctx, &spec, src, eng, resolvers, pit)
	}

	ledgerBalances, err := src.resolve(ctx, resolvers, pit)
	if err != nil {
		return nil, fmt.Errorf("scout ledger balances: %w", err)
	}

	outcomes := make([]Outcome, 0, len(spec.Bounds))
	for _, asset := range sortedKeys(spec.Bounds) {
		bounds := spec.Bounds[asset]
		val := zeroIfNil(ledgerBalances[asset])

		passed := true
		if bounds.Min != nil && val.Cmp(big.NewInt(*bounds.Min)) < 0 {
			passed = false
		}
		if bounds.Max != nil && val.Cmp(big.NewInt(*bounds.Max)) > 0 {
			passed = false
		}

		expr := buildThresholdExpression(&spec, asset)
		compiled, err := eng.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("compile per-asset expression for %s: %w", asset, err)
		}
		evalOut, err := eng.Evaluate(ctx, compiled, in)
		if err != nil {
			return nil, fmt.Errorf("evaluate per-asset expression for %s: %w", asset, err)
		}
		if evalOut.Passed != passed {
			return nil, fmt.Errorf(
				"kernel/template disagreement on %s: kernel=%v, direct=%v (balance=%s bounds=%+v)",
				asset, evalOut.Passed, passed, val.String(), bounds,
			)
		}

		evidence := map[string]any{
			"asset":       asset,
			"balance":     val.String(),
			"compiledCEL": expr,
		}
		if bounds.Min != nil {
			evidence["min"] = *bounds.Min
		}
		if bounds.Max != nil {
			evidence["max"] = *bounds.Max
		}

		outcomes = append(outcomes, Outcome{
			Fingerprint:  fingerprintFor("asset", asset),
			Passed:       passed,
			Evidence:     evidence,
			PitPerSource: evalOut.PitPerSource,
		})
	}
	return outcomes, nil
}

// evaluatePerAccount fans the rule out into one Outcome per (account, asset).
// Unlike the aggregate path it reads each matched account's balance directly
// via ListAccounts (volumes), so there is no kernel cross-check — the value
// does not come from a balance(ledgerSet) CEL call, and re-querying every
// account through the kernel would be N extra ledger round-trips comparing two
// different SDK paths. The per-account CEL is still rendered into evidence for
// explainability. The accounts budget (eng.MaxAccountsScanned) bounds the
// fan-out; the resolver errors rather than truncating past it.
func (t *AccountThreshold) evaluatePerAccount(
	ctx context.Context,
	spec *ThresholdSpec,
	src SourceSpec,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	pit time.Time,
) ([]Outcome, error) {
	accounts, err := src.resolveAccounts(ctx, resolvers, pit, eng.MaxAccountsScanned())
	if err != nil {
		return nil, fmt.Errorf("scout accounts on %s: %w", src.label(), err)
	}
	pitPerSource := map[string]time.Time{src.label(): pit}
	assets := sortedKeys(spec.Bounds)
	outcomes := make([]Outcome, 0, len(accounts)*len(assets))
	for _, acct := range accounts {
		for _, asset := range assets {
			bounds := spec.Bounds[asset]
			val := zeroIfNil(acct.Balances[asset])

			passed := true
			if bounds.Min != nil && val.Cmp(big.NewInt(*bounds.Min)) < 0 {
				passed = false
			}
			if bounds.Max != nil && val.Cmp(big.NewInt(*bounds.Max)) > 0 {
				passed = false
			}

			evidence := map[string]any{
				"asset":       asset,
				"account":     acct.Address,
				"balance":     val.String(),
				"compiledCEL": buildPerAccountExpression(src, acct.Address, asset, bounds),
			}
			if bounds.Min != nil {
				evidence["min"] = *bounds.Min
			}
			if bounds.Max != nil {
				evidence["max"] = *bounds.Max
			}

			outcomes = append(outcomes, Outcome{
				Fingerprint:  fingerprintFor("asset", asset, "account", acct.Address),
				Passed:       passed,
				Evidence:     evidence,
				PitPerSource: pitPerSource,
			})
		}
	}
	return outcomes, nil
}

// buildPerAccountExpression renders the single-account CEL form for evidence
// (explainability): the same min/max check as aggregate, but against a
// single-address ledgerSet.
func buildPerAccountExpression(src SourceSpec, address, asset string, bounds ThresholdBounds) string {
	bal := src.celTermForAccount(address, celString(asset))
	parts := []string{}
	if bounds.Min != nil {
		parts = append(parts, fmt.Sprintf("%s >= %d", bal, *bounds.Min))
	}
	if bounds.Max != nil {
		parts = append(parts, fmt.Sprintf("%s <= %d", bal, *bounds.Max))
	}
	return strings.Join(parts, " && ")
}

// buildThresholdExpression renders the per-asset CEL string for aggregate mode.
// Combines optional min/max bounds with &&; an absent bound is omitted from
// the expression (rather than emitting `>= INT64_MIN` and the like).
func buildThresholdExpression(spec *ThresholdSpec, asset string) string {
	bal := fmt.Sprintf("balance(ledgerSet(%s, %s), %s)", celString(spec.Ledger), celJSON(spec.Query), celString(asset))
	bounds := spec.Bounds[asset]
	parts := []string{}
	if bounds.Min != nil {
		parts = append(parts, fmt.Sprintf("%s >= %d", bal, *bounds.Min))
	}
	if bounds.Max != nil {
		parts = append(parts, fmt.Sprintf("%s <= %d", bal, *bounds.Max))
	}
	return strings.Join(parts, " && ")
}
