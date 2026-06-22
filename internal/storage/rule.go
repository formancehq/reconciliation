package storage

import (
	"context"
	"fmt"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// CreateRule inserts a new rule. compiled_cel must already be populated by the
// service layer (template Explain output) — the storage layer doesn't compile.
func (s *Storage) CreateRule(ctx context.Context, rule *models.Rule) error {
	// Storage invariant: cadence is never empty in the DB (the rule_cadence_chk
	// CHECK rejects ''). Default the zero value so direct inserts are safe even
	// if a caller skipped the service-layer default.
	if rule.Cadence == "" {
		rule.Cadence = models.CadenceContinuous
	}
	_, err := s.db.NewInsert().Model(rule).Exec(ctx)
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
	Name          *string
	TemplateKind  *models.TemplateKind
	TemplateSpec  []byte
	CompiledCEL   *string
	Enabled       *bool
	Severity      *models.Severity
	Schedule      *models.Schedule
	Notifications *[]string
	Labels        *map[string]string
}

// PatchRule applies a partial update. Returns ErrNotFound if the rule is gone.
// The updated_at column advances automatically via the touch_updated_at trigger
// (migration #4).
func (s *Storage) PatchRule(ctx context.Context, id uuid.UUID, patch RulePatch) error {
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
	if patch.CompiledCEL != nil {
		q = q.Set("compiled_cel = ?", *patch.CompiledCEL)
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
	res, err := q.Exec(ctx)
	if err != nil {
		return e("failed to patch rule", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return e("patch rule", ErrNotFound)
	}
	return nil
}

func (s *Storage) buildRuleListQuery(selectQuery *bun.SelectQuery, where string, args []any) *bun.SelectQuery {
	selectQuery = selectQuery.Order("created_at DESC")
	if where != "" {
		return selectQuery.Where(where, args...)
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
			return s.buildRuleListQuery(query, where, args)
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
			return fmt.Sprintf("%s %s ?", col, query.DefaultComparisonOperatorsMapping[operator]), []any{value}, nil
		default:
			return "", nil, errors.Wrapf(ErrInvalidQuery, "unknown key '%s' when building rule query", key)
		}
	}))
}

type RulesFilters struct{}

type GetRulesQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[RulesFilters]]

func NewGetRulesQuery(opts PaginatedQueryOptions[RulesFilters]) GetRulesQuery {
	return GetRulesQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}
