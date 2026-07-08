package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// ParitySpec is the typed spec for source_parity: two balance sources that
// should agree, per asset, within tolerance. The invariant checked per asset is:
//
//	abs(balance(left) - balance(right)) <= tolerance
//
// Each side is a SourceSpec, so the same template expresses ledger↔ledger (sub-ledger vs control
// account), ledger↔pool, and pool↔pool — without a bespoke template per pairing. This is the
// "two independent records of the same money match" primitive.
type ParitySpec struct {
	Left  SourceSpec `json:"left"`
	Right SourceSpec `json:"right"`

	// Scope is aggregate (default) or per_account. per_account compares the two
	// sources account-by-account (aligned by address) and emits one Outcome per
	// (account, asset); it requires both sides to be ledger sources.
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
	if err := spec.Left.Validate("left"); err != nil {
		return err
	}
	if err := spec.Right.Validate("right"); err != nil {
		return err
	}
	if !spec.Scope.Valid() {
		return fmt.Errorf("%w: scope must be 'aggregate' or 'per_account' (got %q)", ErrInvalidSpec, spec.Scope)
	}
	if spec.Scope == ScopePerAccount && (!spec.Left.supportsPerAccount() || !spec.Right.supportsPerAccount()) {
		return fmt.Errorf("%w: per_account scope requires both sources to be ledger sources (pools are aggregate-only)", ErrInvalidSpec)
	}
	for asset, tol := range spec.Tolerance {
		if tol < 0 {
			return fmt.Errorf("%w: tolerance for %s must be >= 0, got %d", ErrInvalidSpec, asset, tol)
		}
	}
	return nil
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
	if err := requireResolvers(resolvers, spec.Left.resolverNeed(), spec.Right.resolverNeed()); err != nil {
		return nil, err
	}

	if spec.Scope == ScopePerAccount {
		return t.evaluatePerAccount(ctx, &spec, eng, resolvers, in)
	}

	leftBalances, err := spec.Left.resolve(ctx, resolvers, in)
	if err != nil {
		return nil, fmt.Errorf("scout %s: %w", spec.Left.label(), err)
	}
	rightBalances, err := spec.Right.resolve(ctx, resolvers, in)
	if err != nil {
		return nil, fmt.Errorf("scout %s: %w", spec.Right.label(), err)
	}

	assets := unionAssets(leftBalances, rightBalances)
	// Ledger sources are checkpoint-anchored (ADR-002); only pool sources record
	// an audit PIT. Same for every asset in this evaluation, so compute it once.
	pitPerSource := poolPitPerSource(in.PIT, spec.Left, spec.Right)
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
		// per-evaluation runtime check — a checkpoint/live read is identical on
		// both paths, so re-running it through the kernel added cost, not safety.
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
			PitPerSource: pitPerSource,
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
	in engine.EvalInput,
) ([]Outcome, error) {
	limit := eng.MaxAccountsScanned()
	leftAccts, err := spec.Left.resolveAccounts(ctx, resolvers, in, limit)
	if err != nil {
		return nil, fmt.Errorf("scout %s accounts: %w", spec.Left.label(), err)
	}
	rightAccts, err := spec.Right.resolveAccounts(ctx, resolvers, in, limit)
	if err != nil {
		return nil, fmt.Errorf("scout %s accounts: %w", spec.Right.label(), err)
	}
	leftByAddr := accountsByAddress(leftAccts)
	rightByAddr := accountsByAddress(rightAccts)
	// Both sides are ledger sources (Validate enforces it) → Tier-1, anchored by
	// the shared checkpoint, so pitPerSource stays empty (ADR-002 §10.1).
	pitPerSource := map[string]time.Time{}

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
				PitPerSource: pitPerSource,
			})
		}
	}
	return outcomes, nil
}
