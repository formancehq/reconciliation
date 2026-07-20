// Package templates is the V1 GA public-API surface for rules. Each Evaluator
// owns a typed spec, a validator, a representative compiler (for the
// rule.compiled_cel explainability field), and an end-to-end evaluation flow
// that produces one Outcome per fingerprint axis (typically per-asset).
//
// Templates are the entire customer-facing surface at V1 GA — raw CEL is
// internal-only; see ADR-001 and the project memory `reconciliation-v1-scope-discipline`.
package templates

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// Outcome is one fingerprint-scoped pass/fail result from a template evaluation.
// A single rule evaluation produces N outcomes (one per asset / account / etc.
// depending on template). The service layer (task #5) opens/updates incidents
// from these — one incident per failing Outcome's Fingerprint.
type Outcome struct {
	// Fingerprint is the stable dedup key for incident matching. Format is
	// template-dependent but always "axis:value" segments joined by '|'.
	// Examples:
	//   "asset:USD/2"
	//   "asset:USD/2|account:merchant:m1:held"
	Fingerprint string

	// Passed is true when the rule's invariant holds for this fingerprint.
	Passed bool

	// Evidence is the breakdown the template wants to expose on the resulting
	// incident's `evidence` jsonb column — the actual balances, accounts,
	// drift, etc. examined to produce this outcome.
	Evidence map[string]any
}

// EvaluationResult is the complete result of one template execution. Source
// PITs belong to the evaluation, not to individual fingerprint outcomes; this
// also lets zero-outcome evaluations record the snapshots they actually read.
// CostUnits is the cumulative CEL runtime cost across every outcome.
type EvaluationResult struct {
	Outcomes     []Outcome
	PitPerSource map[string]time.Time
	CostUnits    int64
}

// Evaluator is the per-template contract.
type Evaluator interface {
	// Kind returns the persisted template_kind discriminator.
	Kind() models.TemplateKind

	// Validate sanity-checks the spec at rule-create time. Returns ErrInvalidSpec
	// (wrapped) on failure; the API layer surfaces these as 400 VALIDATION.
	Validate(spec json.RawMessage) error

	// Explain returns a representative CEL string for the rule.compiled_cel
	// column. This is NOT necessarily the exact expression run at evaluation
	// time — for templates that fan out per asset/account, Explain returns
	// the canonical shape for a single fingerprint. Used by fctl + the
	// future `rules explain` endpoint.
	Explain(spec json.RawMessage) (string, error)

	// Evaluate runs the full template flow against live resolvers via the
	// kernel. Returns one Outcome per fingerprint plus evaluation-level PIT and
	// CEL-cost metadata. Resolver / kernel errors
	// are returned as a single non-nil error — the caller raises an
	// engine.error meta-incident in that case.
	Evaluate(
		ctx context.Context,
		spec json.RawMessage,
		eng *engine.Engine,
		resolvers engine.Resolvers,
		in engine.EvalInput,
	) (*EvaluationResult, error)

	// SourceKeys returns the stable source keys ("<label>#<idx>") this template
	// produces for spec, in evaluation order — the exact keys Evaluate records
	// in EvaluationResult.PitPerSource. The service layer uses them to validate
	// per-source PIT overrides (EvalInput.SourcePITs) and reject unknown keys,
	// rather than silently ignoring a mistyped override.
	SourceKeys(spec json.RawMessage) ([]string, error)
}

// Registry indexes evaluators by template kind. Built once at startup and
// injected into the service layer.
type Registry struct {
	evaluators map[models.TemplateKind]Evaluator
}

// NewRegistry builds a Registry from the given evaluators. Duplicate kinds
// panic — kinds are a hardcoded enum and duplicates indicate a wiring bug.
func NewRegistry(evaluators ...Evaluator) *Registry {
	r := &Registry{evaluators: make(map[models.TemplateKind]Evaluator, len(evaluators))}
	for _, e := range evaluators {
		if _, exists := r.evaluators[e.Kind()]; exists {
			panic("templates: duplicate evaluator for kind " + string(e.Kind()))
		}
		r.evaluators[e.Kind()] = e
	}
	return r
}

// DefaultRegistry returns a Registry with all V1 GA templates registered.
func DefaultRegistry() *Registry {
	return NewRegistry(
		NewLedgerVsPoolDrift(),
		NewLedgerInvariant(),
		NewAccountThreshold(),
		NewSourceParity(),
	)
}

// Get returns the evaluator for a kind, or ErrUnknownTemplate if not found.
func (r *Registry) Get(kind models.TemplateKind) (Evaluator, error) {
	e, ok := r.evaluators[kind]
	if !ok {
		return nil, ErrUnknownTemplate
	}
	return e, nil
}

var (
	// ErrInvalidSpec wraps any spec-validation failure. Callers can errors.Is
	// to detect it and surface as 400 VALIDATION.
	ErrInvalidSpec = errors.New("templates: invalid spec")

	// ErrUnknownTemplate signals that the requested template kind isn't
	// registered. Indicates either a typo on rule create or a missing
	// template impl in the binary.
	ErrUnknownTemplate = errors.New("templates: unknown template kind")
)
