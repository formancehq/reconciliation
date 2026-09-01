package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// ParitySpec is the typed spec for source_parity: two balance sources that
// should agree, per asset, within tolerance. The invariant checked per asset is:
//
//	abs(balance(left) - balance(right)) <= tolerance
//
// Each side is a SourceSpec (a ledger account set), so the template expresses
// any ledger↔ledger pairing — e.g. a sub-ledger reconciled against a control
// account on another ledger — without a bespoke template per pairing. This is
// the "two independent records of the same money match" primitive.
type ParitySpec struct {
	Left  SourceSpec `json:"left"`
	Right SourceSpec `json:"right"`

	// Scope is aggregate (default) or per_account. per_account compares the two
	// sources account-by-account (aligned by address) and emits one Outcome per
	// (account, asset).
	Scope Scope `json:"scope,omitempty"`

	// Tolerance is the per-asset acceptable absolute difference. Missing assets
	// default to 0 (strict equality). Assets present on either side but absent
	// from Tolerance are checked against 0.
	Tolerance map[string]int64 `json:"tolerance,omitempty"`
}

// SourceParity implements Evaluator for the source_parity template.
type SourceParity struct{}

func NewSourceParity() *SourceParity { return &SourceParity{} }

func (*SourceParity) Kind() models.TemplateKind { return models.TemplateSourceParity }

func (t *SourceParity) Validate(raw json.RawMessage) error {
	var spec ParitySpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	if err := spec.Left.validateAs("left", "A"); err != nil {
		return err
	}
	if err := spec.Right.validateAs("right", "B"); err != nil {
		return err
	}
	if !spec.Scope.Valid() {
		return fmt.Errorf("%w: scope must be 'aggregate' or 'per_account' (got %q)", ErrInvalidSpec, spec.Scope)
	}
	// per_account aligns two sources by account address on their per-account
	// balances; an account_metadata source is an aggregate scalar with no
	// per-account breakdown, so it is aggregate-only.
	if spec.Scope == ScopePerAccount &&
		(spec.Left.kind() == SourceAccountMetadata || spec.Right.kind() == SourceAccountMetadata) {
		return fmt.Errorf("%w: per_account scope does not support an account_metadata source (aggregate only)", ErrInvalidSpec)
	}
	if spec.Left.kind() == SourceAccountMetadata &&
		spec.Right.kind() == SourceAccountMetadata &&
		spec.Left.Asset != spec.Right.Asset {
		return fmt.Errorf("%w: Source A and Source B must declare the same asset for account_metadata (fields: left.asset, right.asset; values: %q, %q)", ErrInvalidSpec, spec.Left.Asset, spec.Right.Asset)
	}
	for asset, tol := range spec.Tolerance {
		if tol < 0 {
			return fmt.Errorf("%w: tolerance for %s must be >= 0, got %d", ErrInvalidSpec, asset, tol)
		}
	}
	return nil
}

// Queries returns both sides as (ledger, query) sources for create-time query
// validation. Both are ledger sources at V1 (SourceSpec is ledger-only).
func (t *SourceParity) Queries(raw json.RawMessage) ([]SourceSpec, error) {
	var spec ParitySpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}

	return []SourceSpec{spec.Left, spec.Right}, nil
}

// Explain returns the canonical per-asset CEL form with `<asset>` as a literal
// placeholder.
func (t *SourceParity) Explain(raw json.RawMessage) (string, error) {
	var spec ParitySpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	tol := "0"
	for _, asset := range sortedKeys(spec.Tolerance) {
		tol = fmt.Sprintf("%d", spec.Tolerance[asset])
		break
	}
	return fmt.Sprintf(
		`abs(%s - %s) <= %s`,
		spec.Left.celTerm(`"<asset>"`), spec.Right.celTerm(`"<asset>"`), tol,
	), nil
}

func (t *SourceParity) Evaluate(
	ctx context.Context,
	raw json.RawMessage,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	in engine.EvalInput,
) ([]Outcome, error) {
	var spec ParitySpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return nil, err
	}

	if spec.Scope == ScopePerAccount {
		return t.evaluatePerAccount(ctx, &spec, eng, resolvers)
	}

	limit := eng.MaxAccountsScanned()
	leftBalances, err := spec.Left.resolve(ctx, resolvers, limit)
	if err != nil {
		return nil, fmt.Errorf("scout %s: %w", spec.Left.label(), err)
	}
	rightBalances, err := spec.Right.resolve(ctx, resolvers, limit)
	if err != nil {
		return nil, fmt.Errorf("scout %s: %w", spec.Right.label(), err)
	}

	assets := unionAssets(leftBalances, rightBalances)
	// An account_metadata source defines a one-key/one-asset control. Scope the
	// comparison to that declared asset even if the ledger account set holds
	// other assets; those belong in separate rules.
	if spec.Left.kind() == SourceAccountMetadata {
		assets = []string{spec.Left.Asset}
	} else if spec.Right.kind() == SourceAccountMetadata {
		assets = []string{spec.Right.Asset}
	}
	outcomes := make([]Outcome, 0, len(assets))
	for _, asset := range assets {
		tolerance := spec.Tolerance[asset] // 0 if absent

		leftVal := zeroIfNil(leftBalances[asset])
		rightVal := zeroIfNil(rightBalances[asset])
		diff := new(big.Int).Sub(leftVal, rightVal)
		diffAbs := new(big.Int).Abs(diff)
		passed := diffAbs.Cmp(big.NewInt(tolerance)) <= 0

		// The canonical CEL form is rendered into evidence for explainability;
		// the direct big.Int math above is authoritative. Kernel/template
		// equivalence is a golden-tested code property (TestCrossCheck_*), not a
		// per-evaluation runtime check — a live read is identical on both paths,
		// so re-running it through the kernel added cost, not safety.
		expr := fmt.Sprintf(
			`abs(%s - %s) <= %d`,
			spec.Left.celTerm(celString(asset)), spec.Right.celTerm(celString(asset)), tolerance,
		)

		outcomes = append(outcomes, Outcome{
			Fingerprint: fingerprintFor("asset", asset),
			Passed:      passed,
			Evidence: map[string]any{
				"asset":        asset,
				"leftSource":   spec.Left.label(),
				"leftBalance":  leftVal.String(),
				"rightSource":  spec.Right.label(),
				"rightBalance": rightVal.String(),
				"difference":   diffAbs.String(),
				"signedDiff":   diff.String(),
				"tolerance":    tolerance,
				"compiledCEL":  expr,
			},
		})
	}
	return outcomes, nil
}

// evaluatePerAccount compares the two ledger sources account-by-account, aligned
// by address, emitting one Outcome per (account, asset). Like account_threshold
// per_account it reads balances via ListAccounts (no kernel cross-check — the
// values aren't from a balance(ledgerSet) CEL call); the per-account CEL is
// rendered into evidence for explainability. Both sources are guaranteed
// ledger by Validate.
func (t *SourceParity) evaluatePerAccount(
	ctx context.Context,
	spec *ParitySpec,
	eng *engine.Engine,
	resolvers engine.Resolvers,
) ([]Outcome, error) {
	limit := eng.MaxAccountsScanned()
	leftAccts, err := spec.Left.resolveAccounts(ctx, resolvers, limit)
	if err != nil {
		return nil, fmt.Errorf("scout %s accounts: %w", spec.Left.label(), err)
	}
	rightAccts, err := spec.Right.resolveAccounts(ctx, resolvers, limit)
	if err != nil {
		return nil, fmt.Errorf("scout %s accounts: %w", spec.Right.label(), err)
	}
	leftByAddr := accountsByAddress(leftAccts)
	rightByAddr := accountsByAddress(rightAccts)

	outcomes := make([]Outcome, 0, len(leftByAddr))
	for _, addr := range unionAssets(leftByAddr, rightByAddr) { // sorted union of addresses
		lBal, rBal := leftByAddr[addr], rightByAddr[addr]
		for _, asset := range unionAssets(lBal, rBal) {
			tolerance := spec.Tolerance[asset]
			leftVal := zeroIfNil(lBal[asset])
			rightVal := zeroIfNil(rBal[asset])
			diff := new(big.Int).Sub(leftVal, rightVal)
			diffAbs := new(big.Int).Abs(diff)
			passed := diffAbs.Cmp(big.NewInt(tolerance)) <= 0

			outcomes = append(outcomes, Outcome{
				Fingerprint: fingerprintFor("asset", asset, "account", addr),
				Passed:      passed,
				Evidence: map[string]any{
					"asset":        asset,
					"account":      addr,
					"leftSource":   spec.Left.label(),
					"leftBalance":  leftVal.String(),
					"rightSource":  spec.Right.label(),
					"rightBalance": rightVal.String(),
					"difference":   diffAbs.String(),
					"signedDiff":   diff.String(),
					"tolerance":    tolerance,
					"compiledCEL": fmt.Sprintf(`abs(%s - %s) <= %d`,
						spec.Left.celTermForAccount(addr, celString(asset)),
						spec.Right.celTermForAccount(addr, celString(asset)), tolerance),
				},
			})
		}
	}
	return outcomes, nil
}
