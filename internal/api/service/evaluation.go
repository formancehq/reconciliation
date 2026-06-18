package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
)

// EvaluateRuleRequest carries the PIT context for a rule evaluation.
//   - PIT defaults to time.Now() when zero.
//   - SafetyMargin is honoured *exactly* — including zero. Defaults belong at
//     the caller (the API handler picks 30s when the client omits the field).
//     This split exists so a caller that explicitly wants zero margin (the
//     demo runner, integration tests) can ask for it without the service
//     silently clobbering them back to the production-style 30s.
type EvaluateRuleRequest struct {
	PIT          time.Time
	SafetyMargin time.Duration
}

// EvaluateRule runs the template for the rule, persists an Evaluation row, and
// opens/updates/auto-resolves incidents per fingerprint. Returns the persisted
// evaluation.
//
// On engine error (resolver timeout, CEL builtin throw, budget exceeded), an
// `engine.error` meta-incident is opened — not data incidents — and the
// evaluation is persisted with result=ERROR. This keeps engine-health noise off
// the financial-incident channel.
func (s *Service) EvaluateRule(ctx context.Context, ruleID uuid.UUID, req EvaluateRuleRequest) (*models.Evaluation, error) {
	if s.engine == nil || s.templates == nil {
		return nil, errors.New("service: engine + templates registry are required to evaluate rules")
	}

	rule, err := s.store.GetRule(ctx, ruleID)
	if err != nil {
		return nil, err
	}
	if !rule.Enabled {
		return nil, fmt.Errorf("rule %s is disabled", rule.ID)
	}

	ev, err := s.templates.Get(rule.TemplateKind)
	if err != nil {
		return nil, err
	}

	if req.PIT.IsZero() {
		req.PIT = time.Now().UTC()
	}
	// SafetyMargin is intentionally NOT defaulted here — see the type comment.

	started := time.Now().UTC()
	outcomes, evalErr := ev.Evaluate(ctx, rule.TemplateSpec, s.engine, s.resolvers, engine.EvalInput{
		PIT:          req.PIT,
		SafetyMargin: req.SafetyMargin,
	})
	ended := time.Now().UTC()

	evaluation := &models.Evaluation{
		ID:           uuid.New(),
		RuleID:       rule.ID,
		StartedAt:    started,
		EndedAt:      ended,
		PitPerSource: mergePitPerSource(outcomes),
	}

	if evalErr != nil {
		evaluation.Result = models.EvaluationError
		evaluation.Error = evalErr.Error()
		evaluation.Evidence = marshalErrorEvidence(evalErr)
		if err := s.store.CreateEvaluation(ctx, evaluation); err != nil {
			return nil, err
		}
		if _, mErr := s.openEngineErrorIncident(ctx, rule, evaluation, evalErr); mErr != nil {
			return nil, fmt.Errorf("evaluation persisted but engine.error meta-incident failed: %w", mErr)
		}
		return evaluation, nil
	}

	evaluation.Result = models.EvaluationPass
	for _, o := range outcomes {
		if !o.Passed {
			evaluation.Result = models.EvaluationFail
			break
		}
	}
	evaluation.Evidence = marshalOutcomes(outcomes)
	if err := s.store.CreateEvaluation(ctx, evaluation); err != nil {
		return nil, err
	}

	// Drive the incident layer: open/update for failures, auto-resolve for passes.
	for _, o := range outcomes {
		if o.Passed {
			if _, err := s.store.AutoResolveIncident(ctx, rule.ID, o.Fingerprint, evaluation.ID, ended); err != nil {
				return nil, fmt.Errorf("auto-resolve %s: %w", o.Fingerprint, err)
			}
			continue
		}
		evidenceJSON, err := json.Marshal(o.Evidence)
		if err != nil {
			return nil, fmt.Errorf("marshal evidence for %s: %w", o.Fingerprint, err)
		}
		_, err = s.store.OpenOrUpdateIncident(ctx, storage.OpenIncidentInput{
			RuleID:       rule.ID,
			Fingerprint:  o.Fingerprint,
			Severity:     rule.Severity,
			EvaluationID: evaluation.ID,
			Evidence:     evidenceJSON,
			Labels:       rule.Labels,
			OccurredAt:   ended,
		})
		if err != nil {
			return nil, fmt.Errorf("open/update incident for %s: %w", o.Fingerprint, err)
		}
	}

	return evaluation, nil
}

// GetEvaluation is a passthrough.
func (s *Service) GetEvaluation(ctx context.Context, id uuid.UUID) (*models.Evaluation, error) {
	return s.store.GetEvaluation(ctx, id)
}

// ListEvaluations is a passthrough.
func (s *Service) ListEvaluations(ctx context.Context, q storage.GetEvaluationsQuery) (*bunpaginate.Cursor[models.Evaluation], error) {
	return s.store.ListEvaluations(ctx, q)
}

// openEngineErrorIncident opens (or updates, if recurring) the synthetic
// meta-incident that surfaces engine-side failures separately from data
// incidents. Labelled with `kind: engine.error` so digests can route them to
// an engine-health channel.
func (s *Service) openEngineErrorIncident(ctx context.Context, rule *models.Rule, ev *models.Evaluation, evalErr error) (*models.Incident, error) {
	labels := make(map[string]string, len(rule.Labels)+1)
	for k, v := range rule.Labels {
		labels[k] = v
	}
	labels["kind"] = engineErrorFingerprint

	evidence, _ := json.Marshal(map[string]any{
		"error":        evalErr.Error(),
		"evaluationId": ev.ID.String(),
	})

	res, err := s.store.OpenOrUpdateIncident(ctx, storage.OpenIncidentInput{
		RuleID:       rule.ID,
		Fingerprint:  engineErrorFingerprint,
		Severity:     models.SeverityHigh,
		EvaluationID: ev.ID,
		Evidence:     evidence,
		Labels:       labels,
		OccurredAt:   ev.EndedAt,
	})
	if err != nil {
		return nil, err
	}
	return res.Incident, nil
}

// mergePitPerSource collapses per-outcome PIT maps into a single evaluation-row
// map. Per-outcome maps are typically identical (same Source resolved at the
// same PIT for every asset), so the merge is a straightforward "last write
// wins" — different PIT values for the same Source key would be a kernel bug.
func mergePitPerSource(outcomes []templates.Outcome) map[string]time.Time {
	if len(outcomes) == 0 {
		return map[string]time.Time{}
	}
	out := map[string]time.Time{}
	for _, o := range outcomes {
		for k, v := range o.PitPerSource {
			out[k] = v
		}
	}
	return out
}

func marshalOutcomes(outcomes []templates.Outcome) json.RawMessage {
	if len(outcomes) == 0 {
		return json.RawMessage("[]")
	}
	type encoded struct {
		Fingerprint string         `json:"fingerprint"`
		Passed      bool           `json:"passed"`
		Evidence    map[string]any `json:"evidence,omitempty"`
	}
	enc := make([]encoded, 0, len(outcomes))
	for _, o := range outcomes {
		enc = append(enc, encoded{
			Fingerprint: o.Fingerprint,
			Passed:      o.Passed,
			Evidence:    o.Evidence,
		})
	}
	b, _ := json.Marshal(enc)
	return b
}

func marshalErrorEvidence(err error) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"error": err.Error()})
	return b
}
