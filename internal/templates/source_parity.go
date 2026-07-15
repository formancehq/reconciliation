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
// Each side is a SourceSpec, so the same template expresses ledger↔pool
// (today's ledger_vs_pool_drift use case), ledger↔ledger (sub-ledger vs control
// account), and pool↔pool — without a bespoke template per pairing. This is the
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
// placeholder (see LedgerVsPoolDrift.Explain for the convention).
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

	pit := in.PIT
	if in.SafetyMargin > 0 {
		pit = pit.Add(-in.SafetyMargin)
	}

	if spec.Scope == ScopePerAccount {
		return t.evaluatePerAccount(ctx, &spec, eng, resolvers, pit)
	}

	leftBalances, err := spec.Left.resolve(ctx, resolvers, pit)
	if err != nil {
		return nil, fmt.Errorf("scout %s: %w", spec.Left.label(), err)
	}
	rightBalances, err := spec.Right.resolve(ctx, resolvers, pit)
	if err != nil {
		return nil, fmt.Errorf("scout %s: %w", spec.Right.label(), err)
	}

	assets := unionAssets(leftBalances, rightBalances)
	outcomes := make([]Outcome, 0, len(assets))
	pitPerSource := pitForSources(pit, spec.Left.label(), spec.Right.label())
	for _, asset := range assets {
		tolerance := spec.Tolerance[asset] // 0 if absent

		leftVal := zeroIfNil(leftBalances[asset])
		rightVal := zeroIfNil(rightBalances[asset])
		diff := new(big.Int).Sub(leftVal, rightVal)
		diffAbs := new(big.Int).Abs(diff)
		passed := diffAbs.Cmp(big.NewInt(tolerance)) <= 0

		// compiledCEL is rendered for evidence/explainability only, not run: the
		// verdict is the direct math above. A pool source has no point-in-time
		// read, so re-resolving it through the kernel could diverge on benign
		// `latest` timing (the same TOCTOU ledger_vs_pool_drift documents) — and
		// a source_parity can compare against a pool. Direct-math ≡ CEL is proven
		// by TestKernelParity_Aggregate; the renderer↔grammar contract is checked
		// once at rule-create time by the service's engine.Compile guard.
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
	pit time.Time,
) ([]Outcome, error) {
	limit := eng.MaxAccountsScanned()
	leftAccts, err := spec.Left.resolveAccounts(ctx, resolvers, pit, limit)
	if err != nil {
		return nil, fmt.Errorf("scout %s accounts: %w", spec.Left.label(), err)
	}
	rightAccts, err := spec.Right.resolveAccounts(ctx, resolvers, pit, limit)
	if err != nil {
		return nil, fmt.Errorf("scout %s accounts: %w", spec.Right.label(), err)
	}
	leftByAddr := accountsByAddress(leftAccts)
	rightByAddr := accountsByAddress(rightAccts)
	pitPerSource := map[string]time.Time{spec.Left.label(): pit, spec.Right.label(): pit}

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
