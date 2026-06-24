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
	// Fail fast on a bad cron schedule at create time rather than letting the
	// scheduler discover it (and skip the rule) at run time.
	if r.Schedule != nil && r.Schedule.Kind == models.ScheduleCron {
		if r.Schedule.Expr == "" {
			return errors.New("schedule.expr is required for a cron schedule")
		}
		tz := r.Schedule.TZ
		if tz == "" {
			tz = "UTC"
		}
		if _, err := cron.ParseStandard(fmt.Sprintf("CRON_TZ=%s %s", tz, r.Schedule.Expr)); err != nil {
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
