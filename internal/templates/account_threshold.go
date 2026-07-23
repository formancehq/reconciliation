package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// ThresholdMode controls how account_threshold checks balances.
//
//   - ThresholdAggregate: one outcome per asset; checks the aggregated balance
//     across all accounts in the ledger set.
//   - ThresholdPerAccount: fans the rule out into one outcome per
//     (account, asset) pair — the rule's query resolves to a set of accounts
//     (bounded by Engine.MaxAccountsScanned) and each is checked individually,
//     so a single rule can raise an alert per matching account.
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

// AccountThreshold implements Evaluator for both threshold modes: aggregate
// (one outcome per asset) and per_account (one outcome per resolved account ×
// asset). V1 exposes aggregate only — per_account is parked at Validate (see
// perAccountParkedMsg); its Evaluate path is retained for re-introduction.
// Evaluate dispatches on spec.Mode.
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
	case ThresholdAggregate:
		// ok
	case ThresholdPerAccount:
		// Parked in V1 — creation is blocked here, but evaluatePerAccount below
		// is retained and still tested via the Evaluate path. See perAccountParkedMsg.
		return fmt.Errorf("%w: %s", ErrInvalidSpec, perAccountParkedMsg)
	default:
		return fmt.Errorf("%w: mode must be 'aggregate' (got %q)", ErrInvalidSpec, spec.Mode)
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

// SourceKeys returns the single ledger source key. Kept in lock-step with
// Evaluate by TestSourceKeys_MatchEvaluate.
func (t *AccountThreshold) SourceKeys(raw json.RawMessage) ([]string, error) {
	var spec ThresholdSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	src := SourceSpec{Kind: SourceLedger, Ledger: spec.Ledger, Query: spec.Query}
	return []string{newSourceKeyer().key(src.label())}, nil
}

func (t *AccountThreshold) Evaluate(
	ctx context.Context,
	raw json.RawMessage,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	in engine.EvalInput,
) (*EvaluationResult, error) {
	var spec ThresholdSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	if spec.Mode == "" {
		spec.Mode = ThresholdAggregate
	}
	src := SourceSpec{Kind: SourceLedger, Ledger: spec.Ledger, Query: spec.Query}
	// Single ledger source — one key, one PIT (explicit is irrelevant to a
	// ledger source, which always reads point-in-time at pit).
	srcKey := newSourceKeyer().key(src.label())
	pit, _ := effectiveSourcePIT(in, srcKey)
	result := &EvaluationResult{PitPerSource: map[string]time.Time{}}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return result, err
	}

	if spec.Mode == ThresholdPerAccount {
		return t.evaluatePerAccount(ctx, &spec, src, eng, resolvers, result, srcKey, pit, in)
	}

	ledgerBalances, resolvedAt, err := src.resolve(ctx, resolvers, pit, false)
	if err != nil {
		return result, fmt.Errorf("scout ledger balances: %w", err)
	}
	result.PitPerSource[srcKey] = resolvedAt

	outcomes := make([]Outcome, 0, len(spec.Bounds))
	expressions := make([]string, 0, len(spec.Bounds))
	for _, asset := range sortedKeys(spec.Bounds) {
		bounds := spec.Bounds[asset]
		val := zeroIfNil(ledgerBalances[asset])
		passed := thresholdPassed(val, bounds)

		expr := buildThresholdExpression(&spec, asset)

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
			Fingerprint: fingerprintFor("asset", asset),
			Passed:      passed,
			Evidence:    evidence,
			Proof:       thresholdProof(val, bounds),
		})
		expressions = append(expressions, snapshotVerdictExpression(
			buildSnapshotThresholdExpression(val, bounds), passed, val,
		))
	}
	result.Outcomes = outcomes
	if err := applyKernelVerdicts(ctx, eng, in, result, expressions); err != nil {
		return result, err
	}
	return result, nil
}

// evaluatePerAccount fans the rule out into one Outcome per (account, asset).
// It reads each matched account's balance directly via ListAccounts (volumes),
// then makes CEL authoritative over those snapshot values without re-querying
// every account. The source-shaped per-account CEL remains in evidence for
// explainability. The accounts budget (eng.MaxAccountsScanned) bounds the
// fan-out; the resolver errors rather than truncating past it.
func (t *AccountThreshold) evaluatePerAccount(
	ctx context.Context,
	spec *ThresholdSpec,
	src SourceSpec,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	result *EvaluationResult,
	srcKey string,
	pit time.Time,
	in engine.EvalInput,
) (*EvaluationResult, error) {
	accounts, err := src.resolveAccounts(ctx, resolvers, pit, eng.MaxAccountsScanned())
	if err != nil {
		return result, fmt.Errorf("scout accounts on %s: %w", src.label(), err)
	}
	result.PitPerSource[srcKey] = pit
	assets := sortedKeys(spec.Bounds)
	outcomes := make([]Outcome, 0, len(accounts)*len(assets))
	expressions := make([]string, 0, len(accounts)*len(assets))
	for _, acct := range accounts {
		for _, asset := range assets {
			bounds := spec.Bounds[asset]
			val := zeroIfNil(acct.Balances[asset])
			passed := thresholdPassed(val, bounds)

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
				Fingerprint: fingerprintFor("asset", asset, "account", acct.Address),
				Passed:      passed,
				Evidence:    evidence,
				Proof:       thresholdProof(val, bounds),
			})
			expressions = append(expressions, snapshotVerdictExpression(
				buildSnapshotThresholdExpression(val, bounds), passed, val,
			))
		}
	}
	result.Outcomes = outcomes
	if err := applyKernelVerdicts(ctx, eng, in, result, expressions); err != nil {
		return result, err
	}
	return result, nil
}

func thresholdPassed(value *big.Int, bounds ThresholdBounds) bool {
	if bounds.Min != nil && value.Cmp(big.NewInt(*bounds.Min)) < 0 {
		return false
	}
	if bounds.Max != nil && value.Cmp(big.NewInt(*bounds.Max)) > 0 {
		return false
	}
	return true
}

func thresholdProof(value *big.Int, bounds ThresholdBounds) map[string]string {
	proof := map[string]string{"balance": value.String()}
	if bounds.Min != nil {
		proof["min"] = strconv.FormatInt(*bounds.Min, 10)
	}
	if bounds.Max != nil {
		proof["max"] = strconv.FormatInt(*bounds.Max, 10)
	}
	return proof
}

func buildSnapshotThresholdExpression(value *big.Int, bounds ThresholdBounds) string {
	term := "(" + value.String() + ")"
	parts := []string{}
	if bounds.Min != nil {
		parts = append(parts, fmt.Sprintf("%s >= %d", term, *bounds.Min))
	}
	if bounds.Max != nil {
		parts = append(parts, fmt.Sprintf("%s <= %d", term, *bounds.Max))
	}
	return strings.Join(parts, " && ")
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
