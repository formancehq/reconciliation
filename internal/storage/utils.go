package storage

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/uptrace/bun"
)

// hexOf renders a digest for logs, comparisons and API responses. Hex rather
// than base64 because these values get pasted into audit reports and compared by
// eye.
func hexOf(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}

func paginateWithOffset[FILTERS any, RETURN any](s *Storage, ctx context.Context,
	q *bunpaginate.OffsetPaginatedQuery[FILTERS], builders ...func(query *bun.SelectQuery) *bun.SelectQuery) (*bunpaginate.Cursor[RETURN], error) {

	query := s.db.NewSelect()
	for _, builder := range builders {
		query = query.Apply(builder)
	}

	return bunpaginate.UsingOffset[FILTERS, RETURN](ctx, query, *q)
}

type PaginatedQueryOptions[T any] struct {
	QueryBuilder query.Builder `json:"qb"`
	PageSize     uint64        `json:"pageSize"`
	Options      T             `json:"options"`
}

func (opts *PaginatedQueryOptions[T]) UnmarshalJSON(data []byte) error {
	type base struct {
		PageSize uint64          `json:"pageSize"`
		Options  T               `json:"options"`
		RawQB    json.RawMessage `json:"qb"`
	}

	var value base
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}

	opts.PageSize = value.PageSize
	opts.Options = value.Options

	if len(value.RawQB) == 0 || string(value.RawQB) == "null" {
		opts.QueryBuilder = nil
		return nil
	}

	queryBuilder, err := query.ParseJSON(string(value.RawQB))
	if err != nil {
		return err
	}
	opts.QueryBuilder = queryBuilder

	return nil
}

func (opts PaginatedQueryOptions[T]) WithQueryBuilder(qb query.Builder) PaginatedQueryOptions[T] {
	opts.QueryBuilder = qb

	return opts
}

func (opts PaginatedQueryOptions[T]) WithPageSize(pageSize uint64) PaginatedQueryOptions[T] {
	opts.PageSize = pageSize

	return opts
}

func NewPaginatedQueryOptions[T any](options T) PaginatedQueryOptions[T] {
	return PaginatedQueryOptions[T]{
		Options:  options,
		PageSize: bunpaginate.QueryDefaultPageSize,
	}
}
