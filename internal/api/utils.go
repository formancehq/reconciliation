package api

import (
	"io"
	"net/http"

	"github.com/formancehq/go-libs/pointer"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/store"
)

func getQueryBuilder(r *http.Request) (query.Builder, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	if len(data) > 0 {
		return query.ParseJSON(string(data))
	}

	// If we don't have a body, we use the query param
	return query.ParseJSON(r.URL.Query().Get("query"))
}

func getPaginatedQueryOptionsRules(r *http.Request) (*store.PaginatedQueryOptions[store.RulesFilters], error) {
	qb, err := getQueryBuilder(r)
	if err != nil {
		return nil, err
	}
	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}
	return pointer.For(store.NewPaginatedQueryOptions(store.RulesFilters{}).
		WithQueryBuilder(qb).
		WithPageSize(pageSize)), nil
}

func getPaginatedQueryOptionsAlerts(r *http.Request) (*store.PaginatedQueryOptions[store.AlertsFilters], error) {
	qb, err := getQueryBuilder(r)
	if err != nil {
		return nil, err
	}
	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}
	return pointer.For(store.NewPaginatedQueryOptions(store.AlertsFilters{}).
		WithQueryBuilder(qb).
		WithPageSize(pageSize)), nil
}

func getPaginatedQueryOptionsAlertEvents(r *http.Request) (*store.PaginatedQueryOptions[store.AlertEventsFilters], error) {
	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}
	// The alert id comes from the path, not a query builder — events have no
	// user-facing filter surface, so only page size is read here.
	return pointer.For(store.NewPaginatedQueryOptions(store.AlertEventsFilters{}).
		WithPageSize(pageSize)), nil
}

func getPaginatedQueryOptionsCaptures(r *http.Request) (*store.PaginatedQueryOptions[store.CapturesFilters], error) {
	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}
	// The rule id comes from the path; `period` is the one optional filter
	// (scope to a single reconciliation period).
	return pointer.For(store.NewPaginatedQueryOptions(store.CapturesFilters{
		Period: r.URL.Query().Get("period"),
	}).WithPageSize(pageSize)), nil
}
