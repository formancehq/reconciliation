package store

import (
	"encoding/json"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/models"
)

type PaginatedQueryOptions[T any] struct {
	QueryBuilder query.Builder `json:"qb"`
	PageSize     uint64        `json:"pageSize"`
	Options      T             `json:"options"`
}

// UnmarshalJSON decodes the query builder explicitly. QueryBuilder is an
// interface, so encoding/json cannot reconstruct it from the `qb` object on its
// own and every cursor carrying a filter would fail to decode — a filtered list
// request would 400 on its second page (main: #88, TS-496).
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

type RulesFilters struct {
	ContractVersion *models.ContractVersion
}

type GetRulesQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[RulesFilters]]

func NewGetRulesQuery(opts PaginatedQueryOptions[RulesFilters]) GetRulesQuery {
	return GetRulesQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}

type AlertsFilters struct {
	ContractVersion *models.ContractVersion
}

type GetAlertsQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[AlertsFilters]]

func NewGetAlertsQuery(opts PaginatedQueryOptions[AlertsFilters]) GetAlertsQuery {
	return GetAlertsQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}

type AlertEventsFilters struct{}

type GetAlertEventsQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[AlertEventsFilters]]

func NewGetAlertEventsQuery(opts PaginatedQueryOptions[AlertEventsFilters]) GetAlertEventsQuery {
	return GetAlertEventsQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}

// CapturesFilters scopes a rule's capture history. Period is optional: empty
// lists every period of the rule; set, it scopes to that (rule, period) bucket.
type CapturesFilters struct {
	Period          string                  `json:"period"`
	ContractVersion *models.ContractVersion `json:"-"`
}

type GetCapturesQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[CapturesFilters]]

func NewGetCapturesQuery(opts PaginatedQueryOptions[CapturesFilters]) GetCapturesQuery {
	return GetCapturesQuery{
		PageSize: opts.PageSize,
		Order:    bunpaginate.OrderAsc,
		Options:  opts,
	}
}

type GetRuleActivitiesQuery bunpaginate.OffsetPaginatedQuery[PaginatedQueryOptions[RuleActivitiesFilters]]

func NewGetRuleActivitiesQuery(opts PaginatedQueryOptions[RuleActivitiesFilters]) GetRuleActivitiesQuery {
	return GetRuleActivitiesQuery{PageSize: opts.PageSize, Order: bunpaginate.OrderAsc, Options: opts}
}
