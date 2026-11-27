package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/templates"
)

// PreviewRuleRequest is a rule configuration to test before it is saved: the
// template and its spec, as a create or an edit would send them.
type PreviewRuleRequest struct {
	TemplateKind models.TemplateKind `json:"templateKind"`
	TemplateSpec json.RawMessage     `json:"templateSpec"`
}

// RulePreview is what a dry run of a rule configuration found on the current
// balances. Result is pass, fail or error, like an evaluation's; Evidence has
// the capture evidence format. Nothing of it is stored.
type RulePreview struct {
	Result    models.EvaluationResult
	Evidence  json.RawMessage
	Error     string
	PIT       time.Time
	StartedAt time.Time
	EndedAt   time.Time
}

// PreviewRule evaluates a rule configuration on the current balances without
// writing anything: no rule, no evaluation, no capture, no alert. The spec is
// validated as on create, so an invalid configuration is an ErrInvalidSpec
// (400); a failure while reading the sources is a preview with result error,
// as the same failure on a saved rule would be an ERROR evaluation.
func (s *Service) PreviewRule(ctx context.Context, req *PreviewRuleRequest) (*RulePreview, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", templates.ErrInvalidSpec)
	}
	if s.engine == nil || s.templates == nil {
		return nil, errors.New("service: engine + templates registry are required to preview rules")
	}
	if req.TemplateKind == "" {
		return nil, fmt.Errorf("%w: templateKind is required", templates.ErrInvalidSpec)
	}
	if len(req.TemplateSpec) == 0 {
		return nil, fmt.Errorf("%w: templateSpec is required", templates.ErrInvalidSpec)
	}
	if err := validateTemplateContract(createContractVersion(ctx), req.TemplateKind); err != nil {
		return nil, fmt.Errorf("%w: %v", templates.ErrInvalidSpec, err)
	}
	ev, err := s.templates.Get(req.TemplateKind)
	if err != nil {
		return nil, err
	}
	if err := ev.Validate(req.TemplateSpec); err != nil {
		return nil, err
	}
	if err := s.validateQueries(ctx, ev, req.TemplateSpec); err != nil {
		return nil, err
	}

	preview := &RulePreview{PIT: time.Now().UTC()}
	preview.StartedAt = time.Now().UTC()
	outcomes, evalErr := ev.Evaluate(ctx, req.TemplateSpec, s.engine, s.resolvers, engine.EvalInput{PIT: preview.PIT})
	preview.EndedAt = time.Now().UTC()

	if evalErr != nil {
		preview.Result = models.EvaluationError
		preview.Error = evalErr.Error()
		if preview.Evidence, err = marshalErrorEvidence(evalErr); err != nil {
			return nil, err
		}
		return preview, nil
	}
	preview.Result = models.EvaluationPass
	for _, o := range outcomes {
		if !o.Passed {
			preview.Result = models.EvaluationFail
			break
		}
	}
	if preview.Evidence, err = marshalOutcomes(outcomes); err != nil {
		return nil, err
	}
	return preview, nil
}
