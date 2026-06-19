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
// Sign convention. The invariant checked per asset is:
//
//	abs(ledgerSign * ledger_balance  +  pool_balance) <= tolerance
//
// LedgerSign ∈ {+1, -1} flips the ledger term only. The legacy /policies
// reconciliation hard-coded `ledger + pool == 0` (i.e. ledger expected to be
// the negative of pool). That worked for "obligations vs cash held" topologies
// where ledger balances are signed negative, but couldn't express the
// opposite: e.g. a "held" account on the ledger (positive balance) being
// compared against a positive pool balance. With LedgerSign = -1, both sides
// can be naturally positive and still reconcile to zero. Default +1 (the
// natural-signed case); legacy policies port through with LedgerSign = +1
// because the legacy expectation that ledger is already signed negative
// continues to balance out.
type DriftSpec struct {
	Ledger         string          `json:"ledger"`
	LedgerQuery    json.RawMessage `json:"ledgerQuery"`
	PaymentsPoolID string          `json:"paymentsPoolID"`

	// LedgerSign is +1 or -1. Defaults to +1 when zero. Applied to the
	// ledger balance term only — the pool balance is taken as-is.
	LedgerSign int `json:"ledgerSign,omitempty"`

	// Tolerance is the per-asset acceptable drift. Missing assets default to 0
	// (strict equality). Assets present on either side but missing from
	// Tolerance are checked against 0.
	Tolerance map[string]int64 `json:"tolerance,omitempty"`
}

// effectiveLedgerSign returns the sign to apply, defaulting unset (0) to +1.
// Validate guarantees the value is either 0, +1, or -1, so this is a safe
// 3-way collapse used inside Evaluate / Explain.
func (s *DriftSpec) effectiveLedgerSign() int {
	if s.LedgerSign == 0 {
		return 1
	}
	return s.LedgerSign
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
	switch spec.LedgerSign {
	case 0, 1, -1: // 0 means "unset → default +1"
	default:
		return fmt.Errorf("%w: ledgerSign must be +1 or -1, got %d", ErrInvalidSpec, spec.LedgerSign)
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
		// Show the first (lex-sorted) tolerance value; runtime uses the actual
		// map. Render a bare integer literal — CEL has no block-comment syntax,
		// so an annotation like `50 /* tolerance for EUR/2 */` makes the
		// representative expression fail the create-time compile check.
		for _, asset := range sortedKeys(spec.Tolerance) {
			tol = fmt.Sprintf("%d", spec.Tolerance[asset])
			break
		}
	}
	return fmt.Sprintf(
		`abs(%s + balance(pool(%s), "<asset>")) <= %s`,
		signedLedgerTerm(spec.effectiveLedgerSign(), spec.Ledger, spec.LedgerQuery, `"<asset>"`),
		celString(spec.PaymentsPoolID), tol,
	), nil
}

// signedLedgerTerm renders the ledger balance with its sign applied. For
// +1 the leading sign is omitted; for -1 the term becomes `-balance(…)`.
// Keeps the compiled CEL identical to the prior shape when ledgerSign is
// the default, so legacy rules round-trip unchanged.
func signedLedgerTerm(sign int, ledger string, query json.RawMessage, assetExpr string) string {
	term := fmt.Sprintf(`balance(ledgerSet(%s, %s), %s)`, celString(ledger), celJSON(query), assetExpr)
	if sign == -1 {
		return "-" + term
	}
	return term
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

	sign := spec.effectiveLedgerSign()
	for _, asset := range assets {
		tolerance := spec.Tolerance[asset] // 0 if absent

		rawLedger := zeroIfNil(ledgerBalances[asset])
		ledgerVal := new(big.Int).Mul(big.NewInt(int64(sign)), rawLedger)
		poolVal := zeroIfNil(poolBalances[asset])
		drift := new(big.Int).Add(ledgerVal, poolVal)
		driftAbs := new(big.Int).Abs(drift)
		passed := driftAbs.Cmp(big.NewInt(tolerance)) <= 0

		// Also evaluate via the kernel so the audit trail captures the exact
		// CEL run, and so any future divergence between this template and
		// raw-CEL semantics surfaces immediately.
		expr := fmt.Sprintf(
			`abs(%s + balance(pool(%s), %s)) <= %d`,
			signedLedgerTerm(sign, spec.Ledger, spec.LedgerQuery, celString(asset)),
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
				"asset": asset,
				// Raw value as fetched from the resolver, before LedgerSign.
				// Audit consumers comparing against the ledger UI need this.
				"ledgerBalanceRaw": rawLedger.String(),
				// Effective ledger contribution to the sum (rawLedger * sign).
				"ledgerBalance": ledgerVal.String(),
				"ledgerSign":    sign,
				"poolBalance":   poolVal.String(),
				"drift":         driftAbs.String(),
				"tolerance":     tolerance,
				"signedDrift":   drift.String(),
				"compiledCEL":   expr,
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
