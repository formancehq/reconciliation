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
// opens/updates/auto-resolves alerts per fingerprint. Returns the persisted
// evaluation.
//
// On engine error (resolver timeout, CEL builtin throw, budget exceeded), an
// `engine.error` meta-alert is opened — not data alerts — and the
// evaluation is persisted with result=ERROR. This keeps engine-health noise off
// the financial-alert channel.
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

	pitPerSource, mergeErr := mergePitPerSource(outcomes)
	if mergeErr != nil {
		// Promote to a kernel error: a kernel/template disagreement is an
		// engine-health problem, not a data alert.
		evalErr = mergeErr
	}
	evaluation := &models.Evaluation{
		ID:           uuid.New(),
		RuleID:       rule.ID,
		StartedAt:    started,
		EndedAt:      ended,
		PitPerSource: pitPerSource,
	}

	if evalErr != nil {
		evaluation.Result = models.EvaluationError
		evaluation.Error = evalErr.Error()
		ev, err := marshalErrorEvidence(evalErr)
		if err != nil {
			return nil, err
		}
		evaluation.Evidence = ev
		if err := s.store.CreateEvaluation(ctx, evaluation); err != nil {
			return nil, err
		}
		if _, mErr := s.openEngineErrorAlert(ctx, rule, evaluation, evalErr); mErr != nil {
			return nil, fmt.Errorf("evaluation persisted but engine.error meta-alert failed: %w", mErr)
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
	evidence, mErr := marshalOutcomes(outcomes)
	if mErr != nil {
		return nil, mErr
	}
	evaluation.Evidence = evidence

	// The period this evaluation reconciles, derived from the rule's cadence
	// and the evaluation PIT. Every alert this evaluation opens/resolves is
	// scoped to it, so a new period's run never rewrites a prior period's
	// cases (see models.Cadence.PeriodID).
	periodID := rule.Cadence.PeriodID(req.PIT)

	// Atomicity: persist the evaluation row AND drive every alert transition
	// under a single transaction. A mid-loop failure would otherwise leave a
	// committed eval visible to the API while the alert table reflects only
	// some of the outcomes — inconsistent state the UI cannot recover from.
	//
	// Each call into driveAlerts also appends rows to alert_event, so the
	// audit log stays consistent with the alert table by construction.
	err = s.inTx(ctx, func(ctx context.Context, store Store) error {
		if err := store.CreateEvaluation(ctx, evaluation); err != nil {
			return err
		}
		return driveAlerts(ctx, store, rule, evaluation, outcomes, periodID, ended)
	})
	if err != nil {
		return nil, err
	}

	return evaluation, nil
}

// driveAlerts applies the evaluation's outcomes to the alert layer.
// Three cases:
//   - failing outcome → OpenOrUpdateAlert (which handles first-open, reopen
//     after resolve, and update-while-open all in one place)
//   - passing outcome → AutoResolveAlert for that fingerprint
//   - fingerprint disappears entirely (no outcome at all) → AutoResolveAlert
//
// The third case matters for templates like ledger_vs_pool_drift whose
// asset union is dynamic: when both sides of a USD imbalance clear to zero,
// "USD/2" simply stops appearing as an outcome. Without the sweep below,
// that alert would stay OPEN forever despite the condition having cleared.
func driveAlerts(
	ctx context.Context,
	store Store,
	rule *models.Rule,
	evaluation *models.Evaluation,
	outcomes []templates.Outcome,
	periodID string,
	ended time.Time,
) error {
	seenFingerprints := make(map[string]struct{}, len(outcomes))
	for _, o := range outcomes {
		seenFingerprints[o.Fingerprint] = struct{}{}
		if o.Passed {
			if _, err := store.AutoResolveAlert(ctx, rule.ID, o.Fingerprint, periodID, evaluation.ID, ended); err != nil {
				return fmt.Errorf("auto-resolve %s: %w", o.Fingerprint, err)
			}
			continue
		}
		evidenceJSON, err := json.Marshal(o.Evidence)
		if err != nil {
			return fmt.Errorf("marshal evidence for %s: %w", o.Fingerprint, err)
		}
		_, err = store.OpenOrUpdateAlert(ctx, storage.OpenAlertInput{
			RuleID:       rule.ID,
			Fingerprint:  o.Fingerprint,
			PeriodID:     periodID,
			Severity:     rule.Severity,
			EvaluationID: evaluation.ID,
			Evidence:     evidenceJSON,
			Labels:       rule.Labels,
			OccurredAt:   ended,
		})
		if err != nil {
			return fmt.Errorf("open/update alert for %s: %w", o.Fingerprint, err)
		}
	}

	// Sweep is scoped to this period: a fingerprint that cleared this round
	// auto-resolves its case for THIS period only. Prior periods' open cases
	// are untouched — they remain the historical record for their period.
	activeFPs, err := store.ListActiveAlertFingerprints(ctx, rule.ID, periodID)
	if err != nil {
		return fmt.Errorf("sweep active alerts: %w", err)
	}
	for _, fp := range activeFPs {
		if _, seen := seenFingerprints[fp]; seen {
			continue
		}
		if _, err := store.AutoResolveAlert(ctx, rule.ID, fp, periodID, evaluation.ID, ended); err != nil {
			return fmt.Errorf("auto-resolve disappeared fingerprint %s: %w", fp, err)
		}
	}
	return nil
}

// GetEvaluation is a passthrough.
func (s *Service) GetEvaluation(ctx context.Context, id uuid.UUID) (*models.Evaluation, error) {
	return s.store.GetEvaluation(ctx, id)
}

// ListEvaluations is a passthrough.
func (s *Service) ListEvaluations(ctx context.Context, q storage.GetEvaluationsQuery) (*bunpaginate.Cursor[models.Evaluation], error) {
	return s.store.ListEvaluations(ctx, q)
}

// openEngineErrorAlert opens (or updates / reopens, if recurring) the
// synthetic meta-alert that surfaces engine-side failures separately from
// data alerts. Labelled with `kind: engine.error` so digests can route them to
// an engine-health channel.
func (s *Service) openEngineErrorAlert(ctx context.Context, rule *models.Rule, ev *models.Evaluation, evalErr error) (*models.Alert, error) {
	labels := make(map[string]string, len(rule.Labels)+1)
	for k, v := range rule.Labels {
		labels[k] = v
	}
	labels["kind"] = engineErrorFingerprint

	evidence, mErr := json.Marshal(map[string]any{
		"error":        evalErr.Error(),
		"evaluationId": ev.ID.String(),
	})
	if mErr != nil {
		return nil, fmt.Errorf("marshal engine.error evidence: %w", mErr)
	}

	res, err := s.store.OpenOrUpdateAlert(ctx, storage.OpenAlertInput{
		RuleID:      rule.ID,
		Fingerprint: engineErrorFingerprint,
		// Engine-health is operational, not a per-period reconciliation fact:
		// a resolver timeout means "the check couldn't run", not "March didn't
		// reconcile". Keep it in the continuous scope regardless of cadence.
		PeriodID:     models.ContinuousPeriod,
		Severity:     models.SeverityHigh,
		EvaluationID: ev.ID,
		Evidence:     evidence,
		Labels:       labels,
		OccurredAt:   ev.EndedAt,
	})
	if err != nil {
		return nil, err
	}
	return res.Alert, nil
}

// mergePitPerSource collapses per-outcome PIT maps into a single evaluation-row
// map. Per-outcome maps must be identical for the same Source key — every
// template resolves a given Source at one PIT for the whole evaluation. A
// disagreement is a kernel/template contract bug, not a "last write wins"
// situation, so surface it as an error rather than silently picking one.
func mergePitPerSource(outcomes []templates.Outcome) (map[string]time.Time, error) {
	if len(outcomes) == 0 {
		return map[string]time.Time{}, nil
	}
	out := map[string]time.Time{}
	for _, o := range outcomes {
		for k, v := range o.PitPerSource {
			if existing, ok := out[k]; ok && !existing.Equal(v) {
				return nil, fmt.Errorf(
					"kernel/template contract violation: source %q reported PIT %s and %s in the same evaluation",
					k, existing.Format(time.RFC3339Nano), v.Format(time.RFC3339Nano),
				)
			}
			out[k] = v
		}
	}
	return out, nil
}

func marshalOutcomes(outcomes []templates.Outcome) (json.RawMessage, error) {
	if len(outcomes) == 0 {
		return json.RawMessage("[]"), nil
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
	b, err := json.Marshal(enc)
	if err != nil {
		return nil, fmt.Errorf("marshal outcomes evidence: %w", err)
	}
	return b, nil
}

func marshalErrorEvidence(err error) (json.RawMessage, error) {
	b, mErr := json.Marshal(map[string]any{"error": err.Error()})
	if mErr != nil {
		return nil, fmt.Errorf("marshal error evidence: %w", mErr)
	}
	return b, nil
}
