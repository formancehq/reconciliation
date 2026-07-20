package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
)

// Engine is the rule kernel. It is concurrency-safe; a single Engine instance
// is the long-lived dependency injected into the service layer.
type Engine struct {
	// validationEnv has all builtins DECLARED (no bindings). Used at Compile
	// time to parse + type-check expressions cheaply and without exercising
	// any resolvers.
	validationEnv *cel.Env

	resolvers Resolvers
	limits    Limits
}

// New constructs an Engine. Both resolvers must be non-nil for V1.
// Partial Limits are filled in from DefaultLimits — any zero-valued field
// inherits its default rather than disabling the check, so a caller passing
// only a custom MaxCELCost still gets the standard MaxAccountsScanned /
// MaxWallClock guards.
func New(resolvers Resolvers, limits Limits) (*Engine, error) {
	if resolvers.Ledger == nil {
		return nil, errors.New("engine: LedgerResolver is required")
	}
	if resolvers.Payments == nil {
		return nil, errors.New("engine: PaymentsResolver is required")
	}
	limits = mergeLimits(limits, DefaultLimits)

	env, err := cel.NewEnv(declarations()...)
	if err != nil {
		return nil, fmt.Errorf("engine: build validation env: %w", err)
	}

	return &Engine{
		validationEnv: env,
		resolvers:     resolvers,
		limits:        limits,
	}, nil
}

// MaxAccountsScanned is the per-evaluation cap on accounts a template may fetch
// via ListAccounts. Templates that fan out per-account (e.g. account_threshold
// per_account) pass this as the resolver's limit so an unbounded ledger can't
// blow up a single evaluation. Mirrors the budget the CEL accounts() path
// enforces internally.
func (e *Engine) MaxAccountsScanned() int { return e.limits.MaxAccountsScanned }

// MaxWallClock is the per-evaluation wall-clock budget the engine enforces on
// resolver calls inside Evaluate. Exposed so the service can bound the WHOLE
// template evaluation — the asset-discovery "scout" reads templates do before
// entering the kernel included — not just each kernel Evaluate call.
func (e *Engine) MaxWallClock() time.Duration { return e.limits.MaxWallClock }

// Compiled is a validated rule expression. Stored on the Rule row as
// `compiled_cel` for explainability. Compiled is intentionally a value type
// holding only the source string — re-parsing per evaluation is cheap and
// avoids the env-binding coupling between compile and eval.
type Compiled struct {
	Source string
}

// Compile parses + type-checks an expression against the kernel's builtin
// surface. Returns ErrCompile-wrapped errors for invalid expressions so the
// API layer can surface them as 400 VALIDATION.
func (e *Engine) Compile(expression string) (*Compiled, error) {
	if expression == "" {
		return nil, fmt.Errorf("%w: expression is empty", ErrCompile)
	}
	ast, issues := e.validationEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, translateCompileError(issues, expression)
	}
	if !ast.OutputType().IsAssignableType(cel.BoolType) {
		return nil, fmt.Errorf("%w: expression must evaluate to a boolean (got %s)", ErrCompile, ast.OutputType().String())
	}
	return &Compiled{Source: expression}, nil
}

// EvalInput carries the per-evaluation knobs.
//   - PIT: the default point-in-time every Source resolves at; a source may be
//     overridden per-source via SourcePITs (see ADR-002, PIT-per-source).
//   - PITExplicit: true when the caller supplied a PIT (vs the service defaulting
//     it to now). It gates the payments-pool read: an explicit past PIT is read
//     point-in-time (GET /v3/pools/{id}/balances?at=), while the "as of now"
//     default reads latest — a PIT read at ~now falls past the pool's last
//     balance movement and returns empty (the balance-window tail; see ADR-002).
//   - SourcePITs: optional effective per-source replay instants keyed by the template's stable
//     source key ("ledger:<name>#<idx>", "pool:<id>#<idx>"). Lets one evaluation
//     read each side at a different instant — the legacy reconciledAtLedger vs
//     reconciledAtPayments contract, generalised to any multi-source template.
//     Consumed by the template layer; a present override is used exactly as
//     supplied and implies an explicit point-in-time read for that source.
//   - SafetyMargin: subtracted from the default PIT only. SourcePITs already
//     contain effective replay instants and must not be adjusted a second time.
type EvalInput struct {
	PIT          time.Time
	PITExplicit  bool
	SourcePITs   map[string]time.Time
	SafetyMargin time.Duration
}

// EvalOutput is what the service layer persists onto evaluation rows + uses to
// open or update incidents. Passed=false means a data-incident path; Error
// being non-nil means an engine-error path (separate channel).
type EvalOutput struct {
	Passed       bool
	Result       any                  // raw CEL eval result; useful for debugging templates
	Evidence     map[string]any       // evaluator-supplied breakdown for incident.evidence
	PitPerSource map[string]time.Time // resolved PIT per Source, by stable key
	CostUnits    int64                // actual CEL runtime cost reported by cel-go
	Error        error                // engine-side runtime error; rule may still be valid
}

// Evaluate runs the compiled program once and returns the structured outcome.
// The caller's ctx is honoured for cancellation; the engine layers a wall-clock
// deadline on top from Limits.MaxWallClock.
func (e *Engine) Evaluate(ctx context.Context, c *Compiled, in EvalInput) (*EvalOutput, error) {
	outputs, err := e.EvaluateBatch(ctx, []*Compiled{c}, in, e.resolvers)
	if err != nil {
		return nil, err
	}
	return outputs[0], nil
}

// EvaluateBatch evaluates a template's fingerprint expressions under one
// wall-clock deadline and one cumulative CEL-cost budget. Callers can supply
// the production resolvers for source-shaped expressions or no resolvers for
// template expressions that already contain scouted snapshot values.
func (e *Engine) EvaluateBatch(
	ctx context.Context,
	compiled []*Compiled,
	in EvalInput,
	resolvers Resolvers,
) ([]*EvalOutput, error) {
	if len(compiled) == 0 {
		return []*EvalOutput{}, nil
	}

	evalCtxWithDeadline, cancel := context.WithTimeout(ctx, e.limits.MaxWallClock)
	defer cancel()

	remainingCost := e.limits.MaxCELCost
	outputs := make([]*EvalOutput, 0, len(compiled))
	for i, expression := range compiled {
		if remainingCost == 0 {
			return nil, fmt.Errorf("%w: cumulative CEL runtime cost exceeded after %d expressions", ErrEvaluate, i)
		}
		output, cost, err := e.evaluateOne(evalCtxWithDeadline, expression, in, resolvers, remainingCost)
		if err != nil {
			return nil, err
		}
		if cost > remainingCost {
			return nil, fmt.Errorf("%w: CEL reported cost %d above remaining limit %d", ErrEvaluate, cost, remainingCost)
		}
		remainingCost -= cost
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func (e *Engine) evaluateOne(
	ctx context.Context,
	c *Compiled,
	in EvalInput,
	resolvers Resolvers,
	costLimit uint64,
) (*EvalOutput, uint64, error) {
	if c == nil {
		return nil, 0, fmt.Errorf("%w: nil compiled program", ErrEvaluate)
	}
	pit := in.PIT
	if in.SafetyMargin > 0 {
		pit = pit.Add(-in.SafetyMargin)
	}

	budget := newBudgetTracker(e.limits)
	ec := newEvalCtx(ctx, pit, in.PITExplicit, resolvers, budget)

	// Build a per-eval env that re-declares everything WITH bindings closed
	// over ec. We re-parse the source here because cel-go programs are tied
	// to the env they were compiled against; the validation env has no
	// bindings, so we can't reuse its AST for eval.
	env, err := cel.NewEnv(bindings(ec)...)
	if err != nil {
		return nil, 0, translateRuntimeError(fmt.Errorf("build eval env: %w", err))
	}
	ast, issues := env.Compile(c.Source)
	if issues != nil && issues.Err() != nil {
		// Defensive: validation already passed, so this should not happen.
		return nil, 0, translateRuntimeError(issues.Err())
	}
	program, err := env.Program(ast, cel.CostLimit(costLimit))
	if err != nil {
		return nil, 0, translateRuntimeError(fmt.Errorf("build program: %w", err))
	}

	ec.ctx = ctx

	result, details, err := program.ContextEval(ctx, map[string]any{})
	if err != nil {
		return nil, 0, translateRuntimeError(err)
	}

	passed, ok := result.Value().(bool)
	if !ok {
		return nil, 0, translateRuntimeError(fmt.Errorf("expression returned %T, expected bool", result.Value()))
	}
	var actualCost uint64
	if details != nil && details.ActualCost() != nil {
		actualCost = *details.ActualCost()
	}

	return &EvalOutput{
		Passed:       passed,
		Result:       result.Value(),
		Evidence:     nil, // templates layer attaches richer evidence in task #4/#5
		PitPerSource: ec.pitPerSource,
		CostUnits:    int64(actualCost),
	}, actualCost, nil
}
