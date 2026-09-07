package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
)

// EvaluateRuleRequest carries the PIT context for a rule evaluation.
//   - PIT defaults to time.Now() when zero. It is the nominal instant used to
//     derive the reconciliation period and as the Tier-2 (pool) audit timestamp;
//     ledger reads are live (ADR-003).
//   - Trigger records what fired the evaluation on the capture record; defaults
//     to manual.
type EvaluateRuleRequest struct {
	PIT     time.Time
	Trigger string
}

// Evaluation trigger values recorded on the capture (ADR-003).
const (
	TriggerScheduled = "scheduled"
	TriggerManual    = "manual"
)

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

	rule, err := s.getRuleForContract(ctx, ruleID)
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
	if req.Trigger == "" {
		req.Trigger = TriggerManual
	}

	started := time.Now().UTC()
	outcomes, evalErr := ev.Evaluate(ctx, rule.TemplateSpec, s.engine, s.resolvers, engine.EvalInput{
		PIT: req.PIT,
	})
	ended := time.Now().UTC()

	evaluation := &models.Evaluation{
		ID:              uuid.New(),
		ContractVersion: rule.ContractVersion.Effective(),
		RuleID:          rule.ID,
		StartedAt:       started,
		EndedAt:         ended,
		CreatedAt:       ended,
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
		periodID := rule.PeriodType.PeriodID(req.PIT)
		if err := s.store.RecordCapture(ctx, captureInput(rule, evaluation, periodID, req.Trigger, req.PIT)); err != nil {
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
	// The period this evaluation reconciles, derived from the rule's period type
	// and the evaluation PIT. Every alert this evaluation opens/resolves is
	// scoped to it, so a new period's run never rewrites a prior period's
	// cases (see models.PeriodType.PeriodID).
	periodID := rule.PeriodType.PeriodID(req.PIT)

	// Plan alert transitions before recording the capture. The capture retains
	// every exact outcome from this evaluation; the plan independently selects
	// only the outcomes that need an alert mutation. No Ledger read is repeated
	// and no prior alert evidence is reused.
	//
	// The idempotent ledger store has no cross-op transaction, so inTx runs the
	// capture and planned transitions sequentially; each write is individually
	// idempotent per (rule, period, evaluation).
	err = s.inTx(ctx, func(ctx context.Context, st Store) error {
		activeFingerprints, err := st.ListActiveAlertFingerprints(ctx, rule.ID, periodID)
		if err != nil {
			return fmt.Errorf("plan active alerts: %w", err)
		}
		plan := planAlertTransitions(outcomes, activeFingerprints)

		evidence, err := marshalOutcomes(outcomes)
		if err != nil {
			return err
		}
		evaluation.Evidence = evidence

		if err := st.CreateEvaluation(ctx, evaluation); err != nil {
			return err
		}
		if err := st.RecordCapture(ctx, captureInput(rule, evaluation, periodID, req.Trigger, req.PIT)); err != nil {
			return err
		}
		if s.maxNewAlerts > 0 && len(plan.newFingerprints) > s.maxNewAlerts {
			return withholdAlertTransitions(ctx, st, rule, evaluation, plan, periodID, ended, s.maxNewAlerts)
		}
		return driveAlertTransitions(ctx, st, rule, evaluation, plan, periodID, ended)
	})
	if err != nil {
		return nil, err
	}

	return evaluation, nil
}

// captureInput builds the immutable capture record for an evaluation (ADR-003):
// the verdict, the complete outcome evidence the evaluation already computed,
// and what triggered the run.
func captureInput(rule *models.Rule, ev *models.Evaluation, periodID, trigger string, pit time.Time) store.CaptureInput {
	verdict := "pass"
	switch ev.Result {
	case models.EvaluationFail:
		verdict = "fail"
	case models.EvaluationError:
		verdict = "error"
	}

	return store.CaptureInput{
		RuleID:          rule.ID,
		ContractVersion: rule.ContractVersion.Effective(),
		TemplateKind:    string(rule.TemplateKind),
		PeriodID:        periodID,
		EvaluationID:    ev.ID,
		CapturedAt:      ev.EndedAt,
		Verdict:         verdict,
		Trigger:         trigger,
		Evidence:        ev.Evidence,
		RuleRevision:    rule.Revision,
		PIT:             pit,
		StartedAt:       ev.StartedAt,
		Result:          ev.Result,
		Error:           ev.Error,
	}
}

type alertTransitionKind uint8

const (
	alertTransitionOpenOrUpdate alertTransitionKind = iota
	alertTransitionAutoResolve
)

type alertOutcomeTransition struct {
	kind    alertTransitionKind
	outcome templates.Outcome
}

// alertTransitionPlan is the service-layer decision for one evaluated outcome
// set. It freezes which outcome-backed alerts will open/update or resolve, plus
// active fingerprints that disappeared from the outcome set and must resolve.
// Capture evidence and alert writes are both derived from this plan.
type alertTransitionPlan struct {
	outcomeTransitions   []alertOutcomeTransition
	disappearedActiveFPs []string

	// newFingerprints are the failing fingerprints with no alert open yet — the
	// ones this evaluation would *open* rather than update. The service caps
	// them (DefaultMaxNewAlertsPerEvaluation); updates and resolutions are not
	// capped, since neither adds to what an operator has to triage and blocking
	// them would strand alerts that are already open.
	newFingerprints []string
}

// planAlertTransitions classifies outcomes against the active alerts observed
// before persistence. Every failure opens or updates an alert. A pass is planned
// only when its fingerprint is active and therefore eligible for automatic
// resolution; unrelated passing outcomes are intentionally omitted. Active
// fingerprints absent from the outcome set are swept as resolved without
// evidence because this evaluation produced no outcome for them.
func planAlertTransitions(outcomes []templates.Outcome, activeFingerprints []string) alertTransitionPlan {
	active := make(map[string]struct{}, len(activeFingerprints))
	for _, fingerprint := range activeFingerprints {
		active[fingerprint] = struct{}{}
	}

	plan := alertTransitionPlan{
		outcomeTransitions: make([]alertOutcomeTransition, 0, len(outcomes)),
	}
	seen := make(map[string]struct{}, len(outcomes))
	opening := make(map[string]struct{}, len(outcomes))
	for _, outcome := range outcomes {
		seen[outcome.Fingerprint] = struct{}{}
		if !outcome.Passed {
			plan.outcomeTransitions = append(plan.outcomeTransitions, alertOutcomeTransition{
				kind:    alertTransitionOpenOrUpdate,
				outcome: outcome,
			})
			if _, isActive := active[outcome.Fingerprint]; !isActive {
				// Guard against a template emitting the same fingerprint twice:
				// it is one alert, so it counts once against the cap.
				if _, counted := opening[outcome.Fingerprint]; !counted {
					opening[outcome.Fingerprint] = struct{}{}
					plan.newFingerprints = append(plan.newFingerprints, outcome.Fingerprint)
				}
			}
			continue
		}
		if _, ok := active[outcome.Fingerprint]; ok {
			plan.outcomeTransitions = append(plan.outcomeTransitions, alertOutcomeTransition{
				kind:    alertTransitionAutoResolve,
				outcome: outcome,
			})
		}
	}

	// Preserve the store's order for deterministic transition application while
	// de-duplicating defensive duplicate entries from a store implementation.
	disappeared := make(map[string]struct{}, len(activeFingerprints))
	for _, fingerprint := range activeFingerprints {
		if _, ok := seen[fingerprint]; !ok {
			if _, duplicate := disappeared[fingerprint]; duplicate {
				continue
			}
			disappeared[fingerprint] = struct{}{}
			plan.disappearedActiveFPs = append(plan.disappearedActiveFPs, fingerprint)
		}
	}
	return plan
}

// driveAlertTransitions applies the precomputed plan after the capture is
// recorded. Open/update, auto-resolution, disappearance sweeping, and their
// idempotency semantics remain owned by the store.
func driveAlertTransitions(
	ctx context.Context,
	st Store,
	rule *models.Rule,
	evaluation *models.Evaluation,
	plan alertTransitionPlan,
	periodID string,
	ended time.Time,
) error {
	for _, transition := range plan.outcomeTransitions {
		outcome := transition.outcome
		if transition.kind == alertTransitionAutoResolve {
			if _, err := st.AutoResolveAlert(ctx, rule.ID, outcome.Fingerprint, periodID, evaluation.ID, ended); err != nil {
				return fmt.Errorf("auto-resolve %s: %w", outcome.Fingerprint, err)
			}
			continue
		}

		evidenceJSON, err := json.Marshal(outcome.Evidence)
		if err != nil {
			return fmt.Errorf("marshal evidence for %s: %w", outcome.Fingerprint, err)
		}
		_, err = st.OpenOrUpdateAlert(ctx, store.OpenAlertInput{
			RuleID:          rule.ID,
			ContractVersion: rule.ContractVersion.Effective(),
			Fingerprint:     outcome.Fingerprint,
			PeriodID:        periodID,
			Severity:        rule.Severity,
			EvaluationID:    evaluation.ID,
			Evidence:        evidenceJSON,
			Labels:          rule.Labels,
			OccurredAt:      ended,
		})
		if err != nil {
			return fmt.Errorf("open/update alert for %s: %w", outcome.Fingerprint, err)
		}
	}

	// Sweep is scoped to this period. Prior periods' open cases are untouched —
	// they remain the historical record for their period.
	for _, fingerprint := range plan.disappearedActiveFPs {
		if _, err := st.AutoResolveAlert(ctx, rule.ID, fingerprint, periodID, evaluation.ID, ended); err != nil {
			return fmt.Errorf("auto-resolve disappeared fingerprint %s: %w", fingerprint, err)
		}
	}
	return nil
}

// withheldFingerprintSample bounds the fingerprints listed on the meta-alert:
// enough to recognise what the evaluation found, without copying a runaway
// outcome set into durable alert evidence.
const withheldFingerprintSample = 10

// withholdAlertTransitions is the alternative to driveAlertTransitions when an
// evaluation would open more new alerts than the service permits. It applies
// **none** of the plan and raises one meta-alert naming the rule and the count.
//
// All-or-nothing is the whole point. Applying a subset would leave the outcomes
// it skipped absent from this evaluation, and the disappearance sweep resolves
// any active fingerprint an evaluation does not re-emit — so a partial
// application would auto-resolve alerts precisely because there were too many
// problems to report. Withholding everything also leaves this run's genuine
// resolutions unapplied; they are re-derived from live state on the next
// evaluation that fits under the cap, whereas a wrongly-resolved alert is a
// silent loss.
//
// The evaluation and its capture are already persisted by the caller, so the
// full outcome set stays on the record even though no alert was opened for it.
func withholdAlertTransitions(
	ctx context.Context,
	st Store,
	rule *models.Rule,
	evaluation *models.Evaluation,
	plan alertTransitionPlan,
	periodID string,
	ended time.Time,
	cap int,
) error {
	sample := plan.newFingerprints
	if len(sample) > withheldFingerprintSample {
		sample = sample[:withheldFingerprintSample]
	}

	labels := make(map[string]string, len(rule.Labels)+1)
	for k, v := range rule.Labels {
		labels[k] = v
	}
	labels["kind"] = alertCapFingerprint

	evidence, err := json.Marshal(map[string]any{
		"reason":              "new_alert_cap_exceeded",
		"ruleId":              rule.ID.String(),
		"ruleName":            rule.Name,
		"newAlerts":           len(plan.newFingerprints),
		"maxNewAlerts":        cap,
		"withheldTransitions": len(plan.outcomeTransitions) + len(plan.disappearedActiveFPs),
		"fingerprintSample":   sample,
		"evaluationId":        evaluation.ID.String(),
		"note": "Alert transitions were withheld in full: this evaluation would have opened " +
			"more new alerts than the service permits. The evaluation and its evidence are " +
			"recorded; no alert was opened, updated or resolved by it.",
	})
	if err != nil {
		return fmt.Errorf("marshal alert.cap evidence: %w", err)
	}

	// Scoped to the evaluation's own period, unlike engine.error: a withheld plan
	// is a fact about this rule in this period, so the next run that fits under
	// the cap sweeps it away as a disappeared fingerprint without anyone
	// resolving it by hand.
	if _, err := st.OpenOrUpdateAlert(ctx, store.OpenAlertInput{
		RuleID:          rule.ID,
		ContractVersion: rule.ContractVersion.Effective(),
		Fingerprint:     alertCapFingerprint,
		PeriodID:        periodID,
		Severity:        models.SeverityHigh,
		EvaluationID:    evaluation.ID,
		Evidence:        evidence,
		Labels:          labels,
		OccurredAt:      ended,
	}); err != nil {
		return fmt.Errorf("open alert.cap meta-alert for rule %s: %w", rule.ID, err)
	}
	return nil
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

	res, err := s.store.OpenOrUpdateAlert(ctx, store.OpenAlertInput{
		RuleID:          rule.ID,
		ContractVersion: rule.ContractVersion.Effective(),
		Fingerprint:     engineErrorFingerprint,
		// Engine-health is operational, not a per-period reconciliation fact:
		// a resolver timeout means "the check couldn't run", not "March didn't
		// reconcile". Keep it in the continuous scope regardless of period type.
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

// marshalOutcomes encodes the evidence persisted on the evaluation row.
//
// The caller supplies the bounded roster selected by alertTransitionPlan: every
// failing outcome plus only passing outcomes that document an automatic alert
// resolution. Persisting the full passing roster remains intentionally avoided:
// a wide rule can produce thousands of fingerprints on every evaluation.
//
// The per-entry `passed` field distinguishes break evidence from successful
// resolution evidence without changing the existing response shape.
func marshalOutcomes(outcomes []templates.Outcome) (json.RawMessage, error) {
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
	if len(enc) == 0 {
		return json.RawMessage("[]"), nil
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
