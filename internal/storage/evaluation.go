package storage

import (
	"context"
	"fmt"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

// CreateEvaluation inserts an evaluation row and journals it. The service layer
// is responsible for setting started_at / ended_at / result / pit_per_source /
// evidence — the storage layer reloads database-generated fields into ev before
// returning.
//
// The journal entry is written here, in the caller's transaction, rather than by
// the runner: an evaluation that committed without being recorded is exactly the
// hole this feature closes, and the only reliable way to prevent it is to make
// the two writes inseparable.
//
// Every result is recorded, PASS included. A journal of failures answers "what
// went wrong"; an auditor is asking the harder question, "prove the control ran
// on the days nothing went wrong", and only the passes answer that.
func (s *Storage) CreateEvaluation(ctx context.Context, ev *models.Evaluation) error {
	// Self-wrapping, like CreateRule: the journal append needs a transaction to
	// hold the chain lock, and a nested RunInTx is a savepoint, so the runner's
	// existing outer transaction still owns the commit.
	return s.RunInTx(ctx, func(ctx context.Context, store *Storage) error {
		return store.createEvaluation(ctx, ev)
	})
}

func (s *Storage) createEvaluation(ctx context.Context, ev *models.Evaluation) error {
	if _, err := s.db.NewInsert().Model(ev).Returning("*").Exec(ctx); err != nil {
		return e("failed to create evaluation", err)
	}

	memento, err := audit.NewEvaluationMemento(ev)
	if err != nil {
		return err
	}
	raw, err := audit.BuildMemento(memento)
	if err != nil {
		return err
	}

	subject := audit.SubjectFrom(ctx)
	entry, err := s.AppendAuditEntry(ctx, AppendAuditInput{
		At:           ev.EndedAt,
		Kind:         models.AuditEvaluationCommitted,
		RuleID:       &ev.RuleID,
		RuleRevision: ev.RuleRevision,
		EvaluationID: &ev.ID,
		PeriodID:     ev.PeriodID,
		Subject:      subject,
		Memento:      raw,
	})
	if err != nil {
		return err
	}

	if _, err := s.db.NewUpdate().Model((*models.Evaluation)(nil)).
		Set("audit_sequence = ?", entry.Sequence).
		Where("id = ?", ev.ID).Exec(ctx); err != nil {
		return e("link evaluation to audit entry", err)
	}
	ev.AuditSequence = &entry.Sequence
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
		case "periodID":
			if operator != "$match" {
				return "", nil, errors.Wrap(ErrInvalidQuery, "'periodID' can only be used with $match")
			}
			return "period_id = ?", []any{value}, nil
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
