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

// InvariantSpec is the typed spec for ledger_invariant — the Buildr-style
// "sum of signed balances must net to zero (within tolerance)" template.
//
// Asset universe is determined by Tolerance keys. Assets present in the
// underlying balances but absent from Tolerance are NOT checked — this is
// deliberate: a tolerance map of {USD: 0} means "I care about USD reconciling,
// nothing else." Add the asset to Tolerance to check it.
type InvariantSpec struct {
	Terms     []InvariantTerm  `json:"terms"`
	Tolerance map[string]int64 `json:"tolerance"`
}

// InvariantTerm names one signed balance source. Sign must be +1 or -1.
type InvariantTerm struct {
	Ledger string          `json:"ledger"`
	Query  json.RawMessage `json:"query"`
	Sign   int             `json:"sign"`
}

// LedgerInvariant implements Evaluator for the ledger_invariant template.
type LedgerInvariant struct{}

func NewLedgerInvariant() *LedgerInvariant { return &LedgerInvariant{} }

func (*LedgerInvariant) Kind() models.TemplateKind { return models.TemplateLedgerInvariant }

func (t *LedgerInvariant) Validate(raw json.RawMessage) error {
	var spec InvariantSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return err
	}
	if len(spec.Terms) == 0 {
		return fmt.Errorf("%w: terms must contain at least one entry", ErrInvalidSpec)
	}
	if len(spec.Tolerance) == 0 {
		return fmt.Errorf("%w: tolerance is required (use 0 for strict equality)", ErrInvalidSpec)
	}
	for i, term := range spec.Terms {
		if term.Ledger == "" {
			return fmt.Errorf("%w: terms[%d].ledger is required", ErrInvalidSpec, i)
		}
		if !hasMeaningfulJSON(term.Query) {
			return fmt.Errorf("%w: terms[%d].query is required", ErrInvalidSpec, i)
		}
		if term.Sign != 1 && term.Sign != -1 {
			return fmt.Errorf("%w: terms[%d].sign must be +1 or -1, got %d", ErrInvalidSpec, i, term.Sign)
		}
	}
	for asset, tol := range spec.Tolerance {
		if tol < 0 {
			return fmt.Errorf("%w: tolerance for %s must be >= 0, got %d", ErrInvalidSpec, asset, tol)
		}
	}
	return nil
}

// SourceKeys returns one key per term, in term order, matching how Evaluate
// assigns them — two terms on the same ledger get "#0"/"#1". Kept in lock-step
// with Evaluate by TestSourceKeys_MatchEvaluate.
func (t *LedgerInvariant) SourceKeys(raw json.RawMessage) ([]string, error) {
	var spec InvariantSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}
	keyer := newSourceKeyer()
	keys := make([]string, 0, len(spec.Terms))
	for _, term := range spec.Terms {
		keys = append(keys, keyer.key(SourceSpec{Kind: SourceLedger, Ledger: term.Ledger}.label()))
	}
	return keys, nil
}

func (t *LedgerInvariant) Explain(raw json.RawMessage) (string, error) {
	var spec InvariantSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return "", err
	}
	// Pick the first (lex-sorted) asset as the representative example.
	assets := sortedKeys(spec.Tolerance)
	if len(assets) == 0 {
		return "", fmt.Errorf("%w: tolerance is empty — cannot pick a representative asset", ErrInvalidSpec)
	}
	return buildInvariantExpression(&spec, assets[0]), nil
}

func (t *LedgerInvariant) Evaluate(
	ctx context.Context,
	raw json.RawMessage,
	eng *engine.Engine,
	resolvers engine.Resolvers,
	in engine.EvalInput,
) (*EvaluationResult, error) {
	var spec InvariantSpec
	if err := unmarshalSpec(raw, &spec); err != nil {
		return nil, err
	}

	// Scout each term's balances at its own PIT. Terms are all ledger sources,
	// so each reads point-in-time; two terms on the same ledger get distinct keys
	// ("ledger:x#0", "ledger:x#1") so they can be overridden — and audited —
	// independently. Done up-front so we can build deterministic evidence and a
	// snapshot-CEL expression list keyed by spec.Tolerance asset order.
	keyer := newSourceKeyer()
	result := &EvaluationResult{PitPerSource: make(map[string]time.Time, len(spec.Terms))}
	if err := requireResolvers(resolvers, "ledger"); err != nil {
		return result, err
	}
	termBalances := make([]map[string]*big.Int, len(spec.Terms))
	for i, term := range spec.Terms {
		key := keyer.key(SourceSpec{Kind: SourceLedger, Ledger: term.Ledger}.label())
		pit, _ := effectiveSourcePIT(in, key)
		b, err := resolvers.Ledger.AggregateBalance(ctx, term.Ledger, term.Query, pit)
		if err != nil {
			return result, fmt.Errorf("scout terms[%d] (%s): %w", i, term.Ledger, err)
		}
		termBalances[i] = b
		result.PitPerSource[key] = pit
	}

	outcomes := make([]Outcome, 0, len(spec.Tolerance))
	expressions := make([]string, 0, len(spec.Tolerance))
	for _, asset := range sortedKeys(spec.Tolerance) {
		tolerance := spec.Tolerance[asset]

		// Direct math. grossPos / grossNeg accumulate the positive- and
		// negative-signed contributions — the two sides that should net (e.g. the
		// held(+) vs obligation(-) totals) — so the green proof carries the real
		// balances checked, not just the ~0 sum.
		signedSum := big.NewInt(0)
		grossPos := big.NewInt(0)
		grossNeg := big.NewInt(0)
		termValues := make([]string, 0, len(spec.Terms))
		celValues := make([]*big.Int, 0, len(spec.Terms)*2+2)
		runningSum := big.NewInt(0)
		for i, term := range spec.Terms {
			v := zeroIfNil(termBalances[i][asset])
			signed := new(big.Int).Mul(big.NewInt(int64(term.Sign)), v)
			signedSum.Add(signedSum, signed)
			if signed.Sign() < 0 {
				grossNeg.Add(grossNeg, signed)
			} else {
				grossPos.Add(grossPos, signed)
			}
			termValues = append(termValues, signed.String())
			celValues = append(celValues, signed)
			runningSum = new(big.Int).Add(runningSum, signed)
			celValues = append(celValues, new(big.Int).Set(runningSum))
		}
		driftAbs := new(big.Int).Abs(signedSum)
		passed := driftAbs.Cmp(big.NewInt(tolerance)) <= 0
		expr := buildInvariantExpression(&spec, asset)

		outcomes = append(outcomes, Outcome{
			Fingerprint: fingerprintFor("asset", asset),
			Passed:      passed,
			Evidence: map[string]any{
				"asset":       asset,
				"signedSum":   signedSum.String(),
				"absDrift":    driftAbs.String(),
				"tolerance":   tolerance,
				"termValues":  termValues,
				"compiledCEL": expr,
			},
			// Green proof: the two netting sides. residual is positive+negative.
			Proof: map[string]string{
				"positive": grossPos.String(), "negative": grossNeg.String(),
				"tolerance": strconv.FormatInt(tolerance, 10),
			},
		})
		celValues = append(celValues, signedSum, driftAbs)
		expressions = append(expressions, snapshotVerdictExpression(
			fmt.Sprintf(`abs(%s) <= %d`, strings.Join(termValues, " + "), tolerance),
			passed, celValues...,
		))
	}
	result.Outcomes = outcomes
	if err := applyKernelVerdicts(ctx, eng, in, result, expressions); err != nil {
		return result, err
	}
	return result, nil
}

// buildInvariantExpression renders the per-asset CEL string. Negative-sign
// terms are emitted as `-balance(...)` (CEL unary minus); the result is
//
//	abs(t0 + t1 + ...) <= TOL
func buildInvariantExpression(spec *InvariantSpec, asset string) string {
	parts := make([]string, 0, len(spec.Terms))
	for _, term := range spec.Terms {
		bal := fmt.Sprintf("balance(ledgerSet(%s, %s), %s)", celString(term.Ledger), celJSON(term.Query), celString(asset))
		if term.Sign < 0 {
			bal = "-" + bal
		}
		parts = append(parts, bal)
	}
	return fmt.Sprintf(`abs(%s) <= %d`, strings.Join(parts, " + "), spec.Tolerance[asset])
}
