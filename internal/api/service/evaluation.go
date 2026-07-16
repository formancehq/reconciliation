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
	// SourcePITs holds optional per-source PIT overrides, keyed by the template's
	// stable source key ("<label>#<idx>", as recorded in Evaluation.PitPerSource).
	// A source with an override reads at that instant instead of the default PIT;
	// this is the reconciledAtLedger-vs-reconciledAtPayments contract, generalised.
	SourcePITs map[string]time.Time
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
		// A disabled rule is a predictable client-side invalid-state, not a
		// server fault: wrap with ErrValidation so handleServiceErrors maps it
		// to 400 rather than falling through to 500.
		return nil, fmt.Errorf("%w: rule %s is disabled", ErrValidation, rule.ID)
	}

	ev, err := s.templates.Get(rule.TemplateKind)
	if err != nil {
		return nil, err
	}

	// Reject per-source PIT overrides that name a source this rule's template
	// doesn't have — a mistyped key would otherwise be silently ignored, leaving
	// the caller to believe they pinned a source they didn't. Fail loudly (400)
	// instead. Valid keys are exactly the ones echoed back in pit_per_source.
	if len(req.SourcePITs) > 0 {
		validKeys, err := ev.SourceKeys(rule.TemplateSpec)
		if err != nil {
			return nil, err
		}
		valid := make(map[string]struct{}, len(validKeys))
		for _, k := range validKeys {
			valid[k] = struct{}{}
		}
		for k := range req.SourcePITs {
			if _, ok := valid[k]; !ok {
				return nil, fmt.Errorf("%w: sourcePITs: unknown source key %q for template %s (valid keys: %v)", ErrValidation, k, rule.TemplateKind, validKeys)
			}
		}
	}

	// Serialise the whole read+persist window against other evaluations of the
	// same rule. Two evaluations read their sources at different instants and
	// then commit separately; without this an older evaluation could commit
	// after a newer one and reopen an alert the newer evaluation just resolved,
	// with stale evidence (plus a spurious reopen webhook). Holding the lock
	// across both steps makes same-rule evaluations fully serial — the one that
	// commits last is the one that read last. Different rules never contend.
	// (PR #83 review — stale-evaluation ordering.)
	var evaluation *models.Evaluation
	if err := s.withRuleLock(ctx, rule.ID, func(ctx context.Context) error {
		var runErr error
		evaluation, runErr = s.runEvaluation(ctx, rule, ev, req)
		return runErr
	}); err != nil {
		return nil, err
	}
	return evaluation, nil
}

// runEvaluation executes one evaluation of rule against ev and persists it,
// opening/updating/auto-resolving alerts per fingerprint. It is always called
// under s.withRuleLock, so the default PIT is stamped HERE (not at request
// entry): a trigger that waited on the lock reads its sources — and scopes its
// period — as of the instant it actually runs, keeping the read order and the
// commit order identical. An explicitly supplied PIT is honoured unchanged (the
// demo runner / integration tests reconcile as of a fixed instant).
func (s *Service) runEvaluation(ctx context.Context, rule *models.Rule, ev templates.Evaluator, req EvaluateRuleRequest) (*models.Evaluation, error) {
	// A caller-supplied PIT is an explicit historical read; a zero PIT (the
	// scheduler, and on-demand "reconcile now") defaults to now and is NOT
	// explicit. The flag gates the payments-pool read (point-in-time vs latest)
	// for sources without their own override — see engine.EvalInput.
	pitExplicit := !req.PIT.IsZero()
	if req.PIT.IsZero() {
		req.PIT = time.Now().UTC()
	}
	// SafetyMargin is intentionally NOT defaulted here — see the type comment.

	// Bound the WHOLE template evaluation — the asset-discovery scout reads
	// included — by the engine's wall-clock budget. The kernel deadlines its own
	// resolver calls inside Evaluate, but templates scout on the bare ctx before
	// entering the kernel; without this a hung ledger/payments call during
	// discovery would run unbounded (the SDK client timeout is 24h) and, under
	// the per-rule lock, pin a connection and block the rule. (PR #83 review.)
	evalCtx := ctx
	if mw := s.engine.MaxWallClock(); mw > 0 {
		var cancel context.CancelFunc
		evalCtx, cancel = context.WithTimeout(ctx, mw)
		defer cancel()
	}

	started := time.Now().UTC()
	outcomes, evalErr := ev.Evaluate(evalCtx, rule.TemplateSpec, s.engine, s.resolvers, engine.EvalInput{
		PIT:          req.PIT,
		PITExplicit:  pitExplicit,
		SourcePITs:   req.SourcePITs,
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

	// The period this evaluation reconciles. Bucketed from the SAME effective
	// instant the resolvers read at — req.PIT minus the safety margin — not the
	// raw PIT: an evaluation just after a period boundary with a positive margin
	// reads the previous period's data, and its alert must be scoped to that
	// period so a later rerun of the real period touches the same case. Every
	// alert this evaluation opens/resolves is scoped to it, so a new period's run
	// never rewrites a prior period's cases (see models.Cadence.PeriodID).
	periodID := rule.Cadence.PeriodID(req.PIT.Add(-req.SafetyMargin))

	// Atomicity: persist the evaluation row AND drive every alert transition
	// under a single transaction. A mid-loop failure would otherwise leave a
	// committed eval visible to the API while the alert table reflects only
	// some of the outcomes — inconsistent state the UI cannot recover from.
	//
	// Each call into driveAlerts also appends rows to alert_event, so the
	// audit log stays consistent with the alert table by construction.
	if err := s.inTx(ctx, func(ctx context.Context, store Store) error {
		if err := store.CreateEvaluation(ctx, evaluation); err != nil {
			return err
		}
		if err := driveAlerts(ctx, store, rule, evaluation, outcomes, periodID, ended); err != nil {
			return err
		}
		// A successful evaluation means the engine ran cleanly — clear any open
		// engine.error meta-alert. It lives in the continuous period (engine
		// health is not a per-period reconciliation fact), so the period-scoped
		// sweep in driveAlerts never reaches it for daily/weekly/monthly rules;
		// resolve it explicitly. No-op when no engine.error alert is active.
		if _, err := store.AutoResolveAlert(ctx, rule.ID, engineErrorFingerprint, models.ContinuousPeriod, evaluation.ID, ended); err != nil {
			return fmt.Errorf("auto-resolve engine.error: %w", err)
		}
		return nil
	}); err != nil {
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

// marshalOutcomes encodes the evidence persisted on the evaluation row.
//
// Only FAILING outcomes are persisted. A rule's outcome list covers every
// fingerprint it touched — passing included — but storing the passing roster on
// every tick is pure write amplification: for a wide rule (thousands of
// fingerprints) it is a multi-thousand-element JSONB rewritten every evaluation,
// almost all of it "still fine". The failing subset is the part anyone queries,
// and it mirrors what the alert layer records. The evaluation's `result`
// (PASS/FAIL/ERROR) already carries the pass/fail verdict; an all-PASS
// evaluation therefore persists `[]`.
//
// The per-entry `passed` field is retained (always false here) so consumers
// that parse the array keep a stable shape.
func marshalOutcomes(outcomes []templates.Outcome) (json.RawMessage, error) {
	type encoded struct {
		Fingerprint string         `json:"fingerprint"`
		Passed      bool           `json:"passed"`
		Evidence    map[string]any `json:"evidence,omitempty"`
	}
	enc := make([]encoded, 0, len(outcomes))
	for _, o := range outcomes {
		if o.Passed {
			continue
		}
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
