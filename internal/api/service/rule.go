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
// derives compiled_cel via the template's Explain, and persists.
type CreateRuleRequest struct {
	Name         string              `json:"name"`
	TemplateKind models.TemplateKind `json:"templateKind"`
	TemplateSpec json.RawMessage     `json:"templateSpec"`
	Schedule     *models.Schedule    `json:"schedule,omitempty"`
	Severity     models.Severity     `json:"severity,omitempty"`
	// Cadence is the reconciliation rhythm — continuous (default), daily, or
	// monthly. It scopes alerts into periods so each period is an
	// independently-closable, immutable case. See models.Cadence.
	Cadence       models.Cadence    `json:"cadence,omitempty"`
	Notifications []string          `json:"notifications,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Enabled       *bool             `json:"enabled,omitempty"`
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
	if r.Cadence != "" && !r.Cadence.Valid() {
		return fmt.Errorf("cadence must be one of continuous, daily, weekly, monthly (got %q)", r.Cadence)
	}
	if r.Severity != "" && !r.Severity.Valid() {
		return fmt.Errorf("severity must be one of info, low, medium, high, critical (got %q)", r.Severity)
	}
	return validateSchedule(r.Schedule)
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
// representative compiled_cel for explainability, and persists the row.
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
	compiled, err := ev.Explain(req.TemplateSpec)
	if err != nil {
		return nil, fmt.Errorf("template Explain: %w", err)
	}
	// Sanity-check the representative CEL parses against the kernel. This
	// catches drift between the template's renderer and the kernel's grammar
	// at create time, not at 3 AM.
	if s.engine != nil {
		if _, err := s.engine.Compile(compiled); err != nil {
			return nil, fmt.Errorf("template compiled_cel did not parse: %w", err)
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
	cadence := req.Cadence
	if cadence == "" {
		cadence = models.CadenceContinuous
	}

	rule := &models.Rule{
		ID:            uuid.New(),
		Name:          req.Name,
		TemplateKind:  req.TemplateKind,
		TemplateSpec:  req.TemplateSpec,
		CompiledCEL:   compiled,
		Enabled:       enabled,
		Severity:      severity,
		Cadence:       cadence,
		Schedule:      req.Schedule,
		Notifications: req.Notifications,
		Labels:        req.Labels,
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
// the new spec is validated and the compiled_cel is rederived.
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
	// registry and rederive compiled_cel so explanations stay accurate.
	if patch.TemplateKind != nil || patch.TemplateSpec != nil {
		if s.templates == nil {
			return errors.New("service: templates registry not configured")
		}
		rule, err := s.store.GetRule(ctx, id)
		if err != nil {
			return err
		}
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
		compiled, err := ev.Explain(spec)
		if err != nil {
			return fmt.Errorf("template Explain: %w", err)
		}
		if s.engine != nil {
			if _, err := s.engine.Compile(compiled); err != nil {
				return fmt.Errorf("compiled_cel did not parse: %w", err)
			}
		}
		patch.CompiledCEL = &compiled
	}
	return s.store.PatchRule(ctx, id, patch)
}

// DeleteRule cascades to evaluations + alerts (and their events) via FK.
func (s *Service) DeleteRule(ctx context.Context, id uuid.UUID) error {
	return s.store.DeleteRule(ctx, id)
}

// Compile-time guarantee: the resolvers field is engine.Resolvers and the
// engine field is *engine.Engine; this avoids accidental shadowing.
var _ engine.Resolvers = (engine.Resolvers{})
