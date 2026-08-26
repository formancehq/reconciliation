package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// CreateRule inserts a new rule. explanation_cel must already be populated by
// the service layer (template Explain output) — the storage layer doesn't
// render or compile it.
func (s *Storage) CreateRule(ctx context.Context, rule *models.Rule) error {
	// Storage invariant: the period type is never empty in the DB (the
	// rule_cadence_chk CHECK rejects ''). Default the zero value so direct
	// inserts are safe even if a caller skipped the service-layer default.
	// The column is still `cadence`; see models.Rule.PeriodType.
	if rule.PeriodType == "" {
		rule.PeriodType = models.PeriodTypeContinuous
	}
	if rule.Revision == 0 {
		rule.Revision = 1
	}
	if rule.Enabled && rule.Schedule != nil && rule.Schedule.Kind == models.ScheduleCron {
		var now time.Time
		if err := s.db.NewSelect().ColumnExpr("now()").Scan(ctx, &now); err != nil {
			return e("read database time for rule schedule", err)
		}
		next, err := rule.Schedule.Next(now)
		if err != nil {
			return err
		}
		if !next.IsZero() {
			rule.NextRunAt = &next
		}
	}
	_, err := s.db.NewInsert().Model(rule).Returning("*").Exec(ctx)
	if err != nil {
		return e("failed to create rule", err)
	}
	return nil
}

// GetRule returns the rule by id, or wrapping ErrNotFound when missing.
func (s *Storage) GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error) {
	var rule models.Rule
	err := s.db.NewSelect().Model(&rule).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, e("failed to get rule", err)
	}
	return &rule, nil
}

// AssertRuleRevision locks the rule row and verifies that an evaluation still
// targets the enabled revision it loaded before performing remote reads. It
// must run in the same transaction as the evaluation and alert writes so a
// concurrent patch, disable, or delete cannot commit between this fence and
// those writes.
func (s *Storage) AssertRuleRevision(ctx context.Context, id uuid.UUID, expected int64) error {
	var revision int64
	var enabled bool
	if err := s.db.NewSelect().Model((*models.Rule)(nil)).
		Column("revision", "enabled").Where("id = ?", id).
		For("UPDATE").Scan(ctx, &revision, &enabled); err != nil {
		return e("load rule revision", err)
	}
	if !enabled || revision != expected {
		return ErrObsoleteJob
	}
	return nil
}

// DeleteRule cascades to evaluations + incidents via the FK ON DELETE CASCADE
// declared in migration #4. Returns ErrNotFound if the rule didn't exist.
func (s *Storage) DeleteRule(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.NewDelete().Model((*models.Rule)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return e("failed to delete rule", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return e("delete rule", ErrNotFound)
	}
	return nil
}

// RulePatch is the partial-update payload accepted by PatchRule. Nil fields are
// left unchanged. Mutating template_kind or template_spec requires re-validation
// by the service layer before this is called.
type RulePatch struct {
	Name           *string
	TemplateKind   *models.TemplateKind
	TemplateSpec   []byte
	ExplanationCEL *string
	Enabled        *bool
	Severity       *models.Severity
	Schedule       *models.Schedule
	Notifications  *[]string
	Labels         *map[string]string
	// ExpectedRevision fences service-layer validation that was derived from a
	// prior read. It is intentionally not part of the public PATCH payload.
	ExpectedRevision *int64
}

// PatchRule applies a partial update. Returns ErrNotFound if the rule is gone.
// The updated_at column advances automatically via the touch_updated_at trigger
// (migration #4).
func (s *Storage) PatchRule(ctx context.Context, id uuid.UUID, patch RulePatch) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return s.WithTx(tx).patchRule(ctx, id, patch)
	})
}

func (s *Storage) patchRule(ctx context.Context, id uuid.UUID, patch RulePatch) error {
	q := s.db.NewUpdate().Model((*models.Rule)(nil)).Where("id = ?", id)
	touched := false
	if patch.Name != nil {
		q = q.Set("name = ?", *patch.Name)
		touched = true
	}
	if patch.TemplateKind != nil {
		q = q.Set("template_kind = ?", string(*patch.TemplateKind))
		touched = true
	}
	if patch.TemplateSpec != nil {
		q = q.Set("template_spec = ?", patch.TemplateSpec)
		touched = true
	}
	if patch.ExplanationCEL != nil {
		q = q.Set("explanation_cel = ?", *patch.ExplanationCEL)
		touched = true
	}
	if patch.Enabled != nil {
		q = q.Set("enabled = ?", *patch.Enabled)
		touched = true
	}
	if patch.Severity != nil {
		q = q.Set("severity = ?", string(*patch.Severity))
		touched = true
	}
	if patch.Schedule != nil {
		q = q.Set("schedule = ?", patch.Schedule)
		touched = true
	}
	if patch.Notifications != nil {
		q = q.Set("notifications = ?", *patch.Notifications)
		touched = true
	}
	if patch.Labels != nil {
		q = q.Set("labels = ?", *patch.Labels)
		touched = true
	}
	if !touched {
		// Empty patch is a valid request shape, but the caller still needs to
		// know whether the rule exists — otherwise a typo'd id returns 200 OK
		// for a no-op that actually missed. Verify existence explicitly.
		exists, err := s.db.NewSelect().
			Model((*models.Rule)(nil)).
			Where("id = ?", id).
			Exists(ctx)
		if err != nil {
			return e("patch rule existence check", err)
		}
		if !exists {
			return e("patch rule", ErrNotFound)
		}
		return nil
	}

	var current models.Rule
	if err := s.db.NewSelect().Model(&current).Where("id = ?", id).For("UPDATE").Scan(ctx); err != nil {
		return e("load rule for patch", err)
	}
	if patch.ExpectedRevision != nil && current.Revision != *patch.ExpectedRevision {
		return ErrRuleRevisionConflict
	}
	effectiveSchedule := current.Schedule
	if patch.Schedule != nil {
		effectiveSchedule = patch.Schedule
	}
	effectiveEnabled := current.Enabled
	if patch.Enabled != nil {
		effectiveEnabled = *patch.Enabled
	}
	var nextRunAt any
	if effectiveEnabled && effectiveSchedule != nil && effectiveSchedule.Kind == models.ScheduleCron {
		var now time.Time
		if err := s.db.NewSelect().ColumnExpr("now()").Scan(ctx, &now); err != nil {
			return e("read database time for patched schedule", err)
		}
		next, err := effectiveSchedule.Next(now)
		if err != nil {
			return err
		}
		nextRunAt = nullTime(next)
	}
	q = q.Set("revision = revision + 1").Set("next_run_at = ?", nextRunAt)
	res, err := q.Exec(ctx)
	if err != nil {
		return e("failed to patch rule", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return e("patch rule", ErrNotFound)
	}
	if _, err := s.db.NewUpdate().Model((*models.EvaluationJob)(nil)).
		Set("status = ?", models.EvaluationJobCancelled).
		Set("last_error = ?", "rule revised").
		Where("rule_id = ? AND rule_revision <= ? AND status = ?", id, current.Revision, models.EvaluationJobPending).
		Exec(ctx); err != nil {
		return e("cancel superseded evaluation jobs", err)
	}
	return nil
}

func (s *Storage) buildRuleListQuery(selectQuery *bun.SelectQuery, where string, args []any, filters RulesFilters) *bun.SelectQuery {
	selectQuery = selectQuery.Order("created_at DESC")
	if where != "" {
		selectQuery = selectQuery.Where(where, args...)
	}
	if filters.EnabledOnly {
		selectQuery = selectQuery.Where("enabled = ?", true)
	}
	if filters.ScheduleKind != "" {
		// `schedule` is jsonb; on_demand rules — and rules with no schedule at
		// all — have a NULL `->>'kind'` and are excluded, which is exactly what
		// filtering to cron wants. Pushing this into SQL means the scheduler's
		// page budget is spent on rules it can actually fire, instead of being
		// consumed by unrelated rules with the relevant ones paged out of reach.
		selectQuery = selectQuery.Where("schedule->>'kind' = ?", string(filters.ScheduleKind))
	}
	return selectQuery
}

// ListRules returns a cursor-paginated set of rules. Filters: id, name,
// templateKind, enabled, ledger (matches template_spec.ledger if present).
func (s *Storage) ListRules(ctx context.Context, q GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error) {
	var (
		where string
		args  []any
		err   error
	)
	if q.Options.QueryBuilder != nil {
		where, args, err = s.ruleQueryContext(q.Options.QueryBuilder)
		if err != nil {
			return nil, err
		}
	}
	return paginateWithOffset[PaginatedQueryOptions[RulesFilters], models.Rule](s, ctx,
		(*bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[RulesFilters]])(&q),
		func(query *bun.SelectQuery) *bun.SelectQuery {
			return s.buildRuleListQuery(query, where, args, q.Options.Options)
		},
	)
}

func (s *Storage) ruleQueryContext(qb query.Builder) (string, []any, error) {
	return qb.Build(query.ContextFn(func(key, operator string, value any) (string, []any, error) {
		switch key {
		case "id", "name":
			if operator != "$match" {
				return "", nil, errors.Wrap(ErrInvalidQuery, "'id' and 'name' columns can only be used with $match")
			}
			return fmt.Sprintf("%s = ?", key), []any{value}, nil
		case "templateKind":
			if operator != "$match" {
				return "", nil, errors.Wrap(ErrInvalidQuery, "'templateKind' can only be used with $match")
			}
			return "template_kind = ?", []any{value}, nil
		case "enabled":
			if operator != "$match" {
				return "", nil, errors.Wrap(ErrInvalidQuery, "'enabled' can only be used with $match")
			}
			return "enabled = ?", []any{value}, nil
		case "createdAt", "updatedAt":
			col := "created_at"
			if key == "updatedAt" {
				col = "updated_at"
			}
			sqlOperator, ok := query.DefaultComparisonOperatorsMapping[operator]
			if !ok {
				return "", nil, errors.Wrapf(ErrInvalidQuery, "operator '%s' is not supported for '%s'", operator, key)
			}
			return fmt.Sprintf("%s %s ?", col, sqlOperator), []any{value}, nil
		default:
			return "", nil, errors.Wrapf(ErrInvalidQuery, "unknown key '%s' when building rule query", key)
		}
	}))
}

// RulesFilters narrows a rule list. The zero value applies no filter (the API
// default). The scheduler sets EnabledOnly + ScheduleKind so the database
// returns only the rules it can fire. json tags keep the filter stable across
// cursor pagination.
type RulesFilters struct {
	EnabledOnly  bool                `json:"enabledOnly,omitempty"`
	ScheduleKind models.ScheduleKind `json:"scheduleKind,omitempty"`
}

type GetRulesQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[RulesFilters]]

func NewGetRulesQuery(opts PaginatedQueryOptions[RulesFilters]) GetRulesQuery {
	return GetRulesQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}
