package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// CreateRuleRequest is what the API hands to the service. The service validates,
// derives explanation_cel via the template's Explain, and persists.
type CreateRuleRequest struct {
	Name         string              `json:"name"`
	TemplateKind models.TemplateKind `json:"templateKind"`
	TemplateSpec json.RawMessage     `json:"templateSpec"`
	Schedule     *models.Schedule    `json:"schedule,omitempty"`
	Severity     models.Severity     `json:"severity,omitempty"`
	// PeriodType sets how long a reconciliation period is — continuous
	// (default), daily, weekly or monthly. It scopes alerts into periods so each
	// period is an independently-closable, immutable case. This is not how often
	// the rule runs; that is Schedule. See models.PeriodType.
	PeriodType models.PeriodType `json:"periodType,omitempty"`
	// Cadence is the pre-2.5.0 name for PeriodType, still accepted so clients
	// generated against the older contract keep working.
	//
	// Deprecated: send periodType. Validate folds this into PeriodType; see
	// resolvePeriodType for the conflict rules.
	Cadence models.PeriodType `json:"cadence,omitempty"`
	// PeriodTypeWasProvided records whether `periodType` was actually present in
	// the request body, which the zero value alone cannot express: it separates
	// an explicit `"periodType": ""` (an invalid enum value, so a 400) from an
	// absent key (which defaults). Same idiom as
	// models.Schedule.SafetyMarginWasProvided.
	//
	// An explicit JSON `null` counts as absent, deliberately: SDKs routinely
	// serialise an unset optional as null, and rejecting it would break clients
	// for no gain. UnmarshalJSON sets this.
	PeriodTypeWasProvided bool              `json:"-"`
	Notifications         []string          `json:"notifications,omitempty"`
	Labels                map[string]string `json:"labels,omitempty"`
	Enabled               *bool             `json:"enabled,omitempty"`
}

// UnmarshalJSON decodes the request and records whether `periodType` was
// present, which the zero value alone cannot express.
func (r *CreateRuleRequest) UnmarshalJSON(data []byte) error {
	// plain drops the method set so this does not recurse.
	type plain CreateRuleRequest
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var probe struct {
		PeriodType *models.PeriodType `json:"periodType"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	decoded.PeriodTypeWasProvided = probe.PeriodType != nil

	*r = CreateRuleRequest(decoded)
	return nil
}

// Validate is invoked at the API boundary; surface the result as 400 VALIDATION.
func (r *CreateRuleRequest) Validate() error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	if r.TemplateKind == "" {
		return errors.New("templateKind is required")
	}
	if len(r.TemplateSpec) == 0 {
		return errors.New("templateSpec is required")
	}
	// Judge an explicitly supplied periodType before folding the legacy key, so
	// an invalid value is always an error rather than something a valid
	// `cadence` could paper over. "" is not an enum member, so
	// `{"periodType":""}` is a 400 rather than a silent default.
	if r.PeriodTypeWasProvided && !r.PeriodType.Valid() {
		return fmt.Errorf("periodType must be one of continuous, daily, weekly, monthly (got %q)", r.PeriodType)
	}
	if err := r.resolvePeriodType(); err != nil {
		return err
	}
	// Catches a value set programmatically rather than decoded from JSON, where
	// PeriodTypeWasProvided is not set.
	if r.PeriodType != "" && !r.PeriodType.Valid() {
		return fmt.Errorf("periodType must be one of continuous, daily, weekly, monthly (got %q)", r.PeriodType)
	}
	if r.Severity != "" && !r.Severity.Valid() {
		return fmt.Errorf("severity must be one of info, low, medium, high, critical (got %q)", r.Severity)
	}
	return validateSchedule(r.Schedule)
}

// resolvePeriodType folds the deprecated `cadence` key into PeriodType so the
// rest of the service only ever reads one field.
//
// Both keys are accepted during the deprecation window, but if both carry a
// value they must agree. Silently picking a winner is the failure mode worth
// avoiding: a client that sends a stale `cadence` alongside a new `periodType`
// would otherwise get a rule that buckets differently from what it asked for,
// with no error — discovered at period close rather than at create time.
//
// An empty `cadence` is treated as unset rather than rejected. Tightening the
// deprecated key is the one thing this deprecation must not do: a pre-2.5.0
// client that sends `"cadence": ""` for "no selection" kept working before, and
// turning that into a 400 would break exactly the callers the alias exists to
// protect. `periodType`, being new, is held to the enum strictly.
//
// Called from Validate, which CreateRule invokes itself, so there is no path
// that reaches persistence with the legacy key unresolved.
func (r *CreateRuleRequest) resolvePeriodType() error {
	switch {
	case r.Cadence == "":
		// Absent, or explicitly empty — treated the same, so nothing to fold.
	case r.PeriodType != "" && r.PeriodType != r.Cadence:
		return fmt.Errorf(
			"cadence and periodType disagree (%q vs %q); cadence is deprecated, send periodType alone",
			r.Cadence, r.PeriodType)
	default:
		r.PeriodType = r.Cadence
		r.PeriodTypeWasProvided = true
	}

	r.Cadence = ""
	return nil
}

// validateSchedule checks a schedule's kind is a known enum value and, for a
// cron schedule, that the expression is present and parses. A nil schedule is
// valid (no schedule ⇒ on-demand). Shared by create validation and PatchRule so
// a malformed schedule is rejected on BOTH paths rather than persisted and then
// silently skipped by the scheduler every tick.
func validateSchedule(s *models.Schedule) error {
	if s == nil {
		return nil
	}
	if s.SafetyMargin < 0 {
		return errors.New("schedule.safetyMargin must be non-negative")
	}
	if !s.Kind.Valid() {
		return fmt.Errorf("schedule.kind must be one of on_demand, cron (got %q)", s.Kind)
	}
	if s.Kind == models.ScheduleCron {
		if s.Expr == "" {
			return errors.New("schedule.expr is required for a cron schedule")
		}
		tz := s.TZ
		if tz == "" {
			tz = "UTC"
		}
		if _, err := cron.ParseStandard(fmt.Sprintf("CRON_TZ=%s %s", tz, s.Expr)); err != nil {
			return fmt.Errorf("schedule.expr is not a valid cron expression: %v", err)
		}
	}
	return nil
}

// CreateRule validates the spec via the relevant template, derives the
// representative explanation_cel for explainability, and persists the row.
func (s *Service) CreateRule(ctx context.Context, req *CreateRuleRequest) (*models.Rule, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", templates.ErrInvalidSpec)
	}
	if s.templates == nil {
		return nil, errors.New("service: templates registry not configured")
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", templates.ErrInvalidSpec, err)
	}

	ev, err := s.templates.Get(req.TemplateKind)
	if err != nil {
		return nil, err
	}
	if err := ev.Validate(req.TemplateSpec); err != nil {
		return nil, err
	}
	explanation, err := ev.Explain(req.TemplateSpec)
	if err != nil {
		return nil, fmt.Errorf("template Explain: %w", err)
	}
	// Sanity-check the representative CEL parses against the kernel. This
	// catches drift between the template's renderer and the kernel's grammar
	// at create time, not at 3 AM.
	if s.engine != nil {
		if _, err := s.engine.Compile(explanation); err != nil {
			return nil, fmt.Errorf("template explanation_cel did not parse: %w", err)
		}
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	severity := req.Severity
	if severity == "" {
		severity = models.SeverityMedium
	}
	periodType := req.PeriodType
	if periodType == "" {
		periodType = models.PeriodTypeContinuous
	}

	rule := &models.Rule{
		ID:             uuid.New(),
		Name:           req.Name,
		TemplateKind:   req.TemplateKind,
		TemplateSpec:   req.TemplateSpec,
		ExplanationCEL: explanation,
		Enabled:        enabled,
		Severity:       severity,
		PeriodType:     periodType,
		Schedule:       req.Schedule,
		Notifications:  req.Notifications,
		Labels:         req.Labels,
	}
	if err := s.store.CreateRule(ctx, rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// GetRule returns one rule or storage.ErrNotFound.
func (s *Service) GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error) {
	return s.store.GetRule(ctx, id)
}

// ListRules is a passthrough — the storage layer handles pagination + filters.
func (s *Service) ListRules(ctx context.Context, q storage.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error) {
	return s.store.ListRules(ctx, q)
}

// PatchRule applies a partial update. If templateKind or templateSpec changes,
// the new spec is validated and the explanation_cel is rederived.
func (s *Service) PatchRule(ctx context.Context, id uuid.UUID, patch storage.RulePatch) error {
	// Validate the mutable enum/schedule fields with the same rules as create,
	// so a patch can't slip past an invalid severity (→ DB CHECK → 500) or a
	// malformed/empty schedule (→ silently skipped by the scheduler). Wrap in
	// ErrValidation so handleServiceErrors maps them to 400.
	if patch.Severity != nil && !patch.Severity.Valid() {
		return fmt.Errorf("%w: severity must be one of info, low, medium, high, critical (got %q)", ErrValidation, *patch.Severity)
	}
	if patch.Schedule != nil {
		if err := validateSchedule(patch.Schedule); err != nil {
			return fmt.Errorf("%w: %v", ErrValidation, err)
		}
	}

	// If the caller is changing the template surface, re-validate against the
	// registry and rederive explanation_cel so explanations stay accurate.
	if patch.TemplateKind != nil || patch.TemplateSpec != nil {
		if s.templates == nil {
			return errors.New("service: templates registry not configured")
		}
		rule, err := s.store.GetRule(ctx, id)
		if err != nil {
			return err
		}
		expectedRevision := rule.Revision
		patch.ExpectedRevision = &expectedRevision
		kind := rule.TemplateKind
		if patch.TemplateKind != nil {
			kind = *patch.TemplateKind
		}
		spec := rule.TemplateSpec
		if patch.TemplateSpec != nil {
			spec = patch.TemplateSpec
		}
		ev, err := s.templates.Get(kind)
		if err != nil {
			return err
		}
		if err := ev.Validate(spec); err != nil {
			return err
		}
		explanation, err := ev.Explain(spec)
		if err != nil {
			return fmt.Errorf("template Explain: %w", err)
		}
		if s.engine != nil {
			if _, err := s.engine.Compile(explanation); err != nil {
				return fmt.Errorf("explanation_cel did not parse: %w", err)
			}
		}
		patch.ExplanationCEL = &explanation
	}
	if err := s.store.PatchRule(ctx, id, patch); err != nil {
		if errors.Is(err, storage.ErrRuleRevisionConflict) {
			return fmt.Errorf("%w: rule %s changed while validating the patch", ErrRuleChanged, id)
		}
		return err
	}
	return nil
}

// DeleteRule cascades to evaluations + alerts (and their events) via FK.
func (s *Service) DeleteRule(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteRule(ctx, id)
}

// Compile-time guarantee: the resolvers field is engine.Resolvers and the
// engine field is *engine.Engine; this avoids accidental shadowing.
var _ engine.Resolvers = (engine.Resolvers{})
