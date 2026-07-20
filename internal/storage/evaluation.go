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

// CreateEvaluation inserts an evaluation row. The service layer is responsible
// for setting started_at / ended_at / result / pit_per_source / evidence — the
// storage layer reloads database-generated fields into ev before returning.
func (s *Storage) CreateEvaluation(ctx context.Context, ev *models.Evaluation) error {
	_, err := s.db.NewInsert().Model(ev).Returning("*").Exec(ctx)
	if err != nil {
		return e("failed to create evaluation", err)
	}
	return nil
}

// GetEvaluation returns the evaluation by id or ErrNotFound.
func (s *Storage) GetEvaluation(ctx context.Context, id uuid.UUID) (*models.Evaluation, error) {
	var ev models.Evaluation
	err := s.db.NewSelect().Model(&ev).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, e("failed to get evaluation", err)
	}
	return &ev, nil
}

func (s *Storage) buildEvaluationListQuery(selectQuery *bun.SelectQuery, where string, args []any) *bun.SelectQuery {
	selectQuery = selectQuery.Order("created_at DESC")
	if where != "" {
		return selectQuery.Where(where, args...)
	}
	return selectQuery
}

// ListEvaluations returns a cursor-paginated set of evaluations. Common filter:
// ruleID (matches the FK).
func (s *Storage) ListEvaluations(ctx context.Context, q GetEvaluationsQuery) (*bunpaginate.Cursor[models.Evaluation], error) {
	var (
		where string
		args  []any
		err   error
	)
	if q.Options.QueryBuilder != nil {
		where, args, err = s.evaluationQueryContext(q.Options.QueryBuilder)
		if err != nil {
			return nil, err
		}
	}
	return paginateWithOffset[PaginatedQueryOptions[EvaluationsFilters], models.Evaluation](s, ctx,
		(*bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[EvaluationsFilters]])(&q),
		func(query *bun.SelectQuery) *bun.SelectQuery {
			return s.buildEvaluationListQuery(query, where, args)
		},
	)
}

func (s *Storage) evaluationQueryContext(qb query.Builder) (string, []any, error) {
	return qb.Build(query.ContextFn(func(key, operator string, value any) (string, []any, error) {
		switch key {
		case "id", "result":
			if operator != "$match" {
				return "", nil, errors.Wrap(ErrInvalidQuery, "'id' / 'result' can only be used with $match")
			}
			return fmt.Sprintf("%s = ?", key), []any{value}, nil
		case "ruleID":
			if operator != "$match" {
				return "", nil, errors.Wrap(ErrInvalidQuery, "'ruleID' can only be used with $match")
			}
			return "rule_id = ?", []any{value}, nil
		case "createdAt", "startedAt", "endedAt":
			col := map[string]string{"createdAt": "created_at", "startedAt": "started_at", "endedAt": "ended_at"}[key]
			sqlOperator, ok := query.DefaultComparisonOperatorsMapping[operator]
			if !ok {
				return "", nil, errors.Wrapf(ErrInvalidQuery, "operator '%s' is not supported for '%s'", operator, key)
			}
			return fmt.Sprintf("%s %s ?", col, sqlOperator), []any{value}, nil
		default:
			return "", nil, errors.Wrapf(ErrInvalidQuery, "unknown key '%s' when building evaluation query", key)
		}
	}))
}

type EvaluationsFilters struct{}

type GetEvaluationsQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[EvaluationsFilters]]

func NewGetEvaluationsQuery(opts PaginatedQueryOptions[EvaluationsFilters]) GetEvaluationsQuery {
	return GetEvaluationsQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}
