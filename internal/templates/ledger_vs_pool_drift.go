package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// DriftSpec is the typed spec for the ledger_vs_pool_drift template. Mirrors
// the legacy Policy shape so existing customers' policies port 1:1 via the
// /policies facade (task #6).
//
// Sign convention (inherited from the legacy reconciliation):
//
//	ledger balance + pool balance == 0 (per asset)
//
// i.e. the ledger side is expected to be the *negative* of the pool side
// (obligations vs cash held). This is documented in spec §6.1 and surfaced
// in the resulting `evidence.drift` field.
type DriftSpec struct {
	Ledger         string             `json:"ledger"`
	LedgerQuery    json.RawMessage    `json:"ledgerQuery"`
	PaymentsPoolID string             `json:"paymentsPoolID"`

	// Tolerance is the per-asset acceptable drift. Missing assets default to 0
	// (strict equality). Assets present on either side but missing from
	// Tolerance are checked against 0.
	Tolerance map[string]int64 `json:"tolerance,omitempty"`
}

// LedgerVsPoolDrift implements Evaluator for the ledger_vs_pool_drift template.
type LedgerVsPoolDrift struct{}

func NewLedgerVsPoolDrift() *LedgerVsPoolDrift { return &LedgerVsPoolDrift{} }

func (*LedgerVsPoolDrift) Kind() models.TemplateKind { return models.TemplateLedgerVsPoolDrift }

func (t *LedgerVsPoolDrift) Validate(raw json.RawMessage) error {
	var spec DriftSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	if spec.Ledger == "" {
		return fmt.Errorf("%w: ledger is required", ErrInvalidSpec)
	}
	if !hasMeaningfulJSON(spec.LedgerQuery) {
		return fmt.Errorf("%w: ledgerQuery is required", ErrInvalidSpec)
	}
	if spec.PaymentsPoolID == "" {
		return fmt.Errorf("%w: paymentsPoolID is required", ErrInvalidSpec)
	}
	for asset, tol := range spec.Tolerance {
		if tol < 0 {
			return fmt.Errorf("%w: tolerance for %s must be >= 0, got %d", ErrInvalidSpec, asset, tol)
		}
	}
	return nil
}

// Explain returns the canonical per-asset CEL form. At evaluation time the
// asset literal is substituted with the actual asset code; this representative
// version uses `<asset>` as a literal placeholder so the saved compiled_cel
// reads as a description of the rule's invariant.
func (t *LedgerVsPoolDrift) Explain(raw json.RawMessage) (string, error) {
	var spec DriftSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	tol := "0"
	if len(spec.Tolerance) > 0 {
		// Show the first (lex-sorted) tolerance value; runtime uses the actual map.
		for _, asset := range sortedKeys(spec.Tolerance) {
			tol = fmt.Sprintf("%d /* tolerance for %s */", spec.Tolerance[asset], asset)
			break
		}
	}
	return fmt.Sprintf(
		`abs(balance(ledgerSet(%s, %s), "<asset>") + balance(pool(%s), "<asset>")) <= %s`,
		celString(spec.Ledger), celJSON(spec.LedgerQuery), celString(spec.PaymentsPoolID), tol,
	), nil
}

func (t *LedgerVsPoolDrift) Evaluate(
	ctx context.Context,
	raw json.RawMessage,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	in engine.EvalInput,
) ([]Outcome, error) {
	var spec DriftSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}

	if err := requireResolvers(resolvers, "ledger", "payments"); err != nil {
		return nil, err
	}

	// Discover the asset universe by querying both sides. This is the V1
	// ledger_vs_pool_drift contract — check every asset present on either
	// side, not just those mentioned in spec.Tolerance.
	pit := in.PIT
	if in.SafetyMargin > 0 {
		pit = pit.Add(-in.SafetyMargin)
	}
	ledgerBalances, err := resolvers.Ledger.AggregateBalance(ctx, spec.Ledger, spec.LedgerQuery, pit)
	if err != nil {
		return nil, fmt.Errorf("scout ledger balances: %w", err)
	}
	poolBalances, err := resolvers.Payments.PoolBalanceLatest(ctx, spec.PaymentsPoolID)
	if err != nil {
		return nil, fmt.Errorf("scout pool balances: %w", err)
	}

	assets := unionAssets(ledgerBalances, poolBalances)
	outcomes := make([]Outcome, 0, len(assets))

	for _, asset := range assets {
		tolerance := spec.Tolerance[asset] // 0 if absent

		ledgerVal := zeroIfNil(ledgerBalances[asset])
		poolVal := zeroIfNil(poolBalances[asset])
		drift := new(big.Int).Add(ledgerVal, poolVal)
		driftAbs := new(big.Int).Abs(drift)
		passed := driftAbs.Cmp(big.NewInt(tolerance)) <= 0

		// Also evaluate via the kernel so the audit trail captures the exact
		// CEL run, and so any future divergence between this template and
		// raw-CEL semantics surfaces immediately.
		expr := fmt.Sprintf(
			`abs(balance(ledgerSet(%s, %s), %s) + balance(pool(%s), %s)) <= %d`,
			celString(spec.Ledger), celJSON(spec.LedgerQuery), celString(asset),
			celString(spec.PaymentsPoolID), celString(asset), tolerance,
		)
		compiled, err := eng.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("compile per-asset expression for %s: %w", asset, err)
		}
		evalOut, err := eng.Evaluate(ctx, compiled, in)
		if err != nil {
			return nil, fmt.Errorf("evaluate per-asset expression for %s: %w", asset, err)
		}
		// Defensive: kernel result must agree with our direct math. Any
		// divergence is a kernel/template contract bug.
		if evalOut.Passed != passed {
			return nil, fmt.Errorf(
				"kernel/template disagreement on %s: kernel=%v, direct=%v (drift=%s tolerance=%d)",
				asset, evalOut.Passed, passed, driftAbs.String(), tolerance,
			)
		}

		outcomes = append(outcomes, Outcome{
			Fingerprint: fingerprintFor("asset", asset),
			Passed:      passed,
			Evidence: map[string]any{
				"asset":          asset,
				"ledgerBalance":  ledgerVal.String(),
				"poolBalance":    poolVal.String(),
				"drift":          driftAbs.String(),
				"tolerance":      tolerance,
				"signedDrift":    drift.String(),
				"compiledCEL":    expr,
			},
			PitPerSource: evalOut.PitPerSource,
		})
	}
	return outcomes, nil
}

func zeroIfNil(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return v
}
