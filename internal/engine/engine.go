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

// New constructs an Engine. The LedgerResolver must be non-nil for V1.
// Partial Limits are filled in from DefaultLimits — any zero-valued field
// inherits its default rather than disabling the check, so a caller passing
// only a custom MaxCELCost still gets the standard MaxAccountsScanned /
// MaxWallClock guards.
func New(resolvers Resolvers, limits Limits) (*Engine, error) {
	if resolvers.Ledger == nil {
		return nil, errors.New("engine: LedgerResolver is required")
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
//   - PIT: the nominal evaluation instant. The service derives the
//     reconciliation period from it. Ledger sources read live — a single
//     aggregate is an internally consistent snapshot (ADR-003).
type EvalInput struct {
	PIT time.Time
}

// EvalOutput is what the service layer persists onto evaluation rows + uses to
// open or update incidents. Passed=false means a data-incident path; Error
// being non-nil means an engine-error path (separate channel).
type EvalOutput struct {
	Passed    bool
	Result    any            // raw CEL eval result; useful for debugging templates
	Evidence  map[string]any // evaluator-supplied breakdown for incident.evidence
	CostUnits int64          // accounts scanned this eval (see budget)
	Error     error          // engine-side runtime error; rule may still be valid
}

// Evaluate runs the compiled program once and returns the structured outcome.
// The caller's ctx is honoured for cancellation; the engine layers a wall-clock
// deadline on top from Limits.MaxWallClock.
func (e *Engine) Evaluate(ctx context.Context, c *Compiled, in EvalInput) (*EvalOutput, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: nil compiled program", ErrEvaluate)
	}

	budget := newBudgetTracker(e.limits)
	ec := newEvalCtx(ctx, in.PIT, e.resolvers, budget)

	// Build a per-eval env that re-declares everything WITH bindings closed
	// over ec. We re-parse the source here because cel-go programs are tied
	// to the env they were compiled against; the validation env has no
	// bindings, so we can't reuse its AST for eval.
	env, err := cel.NewEnv(bindings(ec)...)
	if err != nil {
		return nil, translateRuntimeError(fmt.Errorf("build eval env: %w", err))
	}
	ast, issues := env.Compile(c.Source)
	if issues != nil && issues.Err() != nil {
		// Defensive: validation already passed, so this should not happen.
		return nil, translateRuntimeError(issues.Err())
	}
	program, err := env.Program(ast, cel.CostLimit(e.limits.MaxCELCost))
	if err != nil {
		return nil, translateRuntimeError(fmt.Errorf("build program: %w", err))
	}

	evalCtxWithDeadline, cancel := context.WithTimeout(ctx, e.limits.MaxWallClock)
	defer cancel()
	ec.ctx = evalCtxWithDeadline // propagate the deadline into resolver calls

	result, _, err := program.ContextEval(evalCtxWithDeadline, map[string]any{})
	if err != nil {
		return nil, translateRuntimeError(err)
	}

	passed, ok := result.Value().(bool)
	if !ok {
		return nil, translateRuntimeError(fmt.Errorf("expression returned %T, expected bool", result.Value()))
	}

	return &EvalOutput{
		Passed:    passed,
		Result:    result.Value(),
		Evidence:  nil, // templates layer attaches richer evidence in task #4/#5
		CostUnits: budget.AccountsScanned(),
	}, nil
}
