package api

import (
	"io"
	"net/http"

	"github.com/formancehq/go-libs/pointer"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/storage"
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

func getPaginatedQueryOptionsRules(r *http.Request) (*storage.PaginatedQueryOptions[storage.RulesFilters], error) {
	qb, err := getQueryBuilder(r)
	if err != nil {
		return nil, err
	}
	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}
	return pointer.For(storage.NewPaginatedQueryOptions(storage.RulesFilters{}).
		WithQueryBuilder(qb).
		WithPageSize(pageSize)), nil
}

func getPaginatedQueryOptionsAlerts(r *http.Request) (*storage.PaginatedQueryOptions[storage.AlertsFilters], error) {
	qb, err := getQueryBuilder(r)
	if err != nil {
		return nil, err
	}
	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}
	return pointer.For(storage.NewPaginatedQueryOptions(storage.AlertsFilters{}).
		WithQueryBuilder(qb).
		WithPageSize(pageSize)), nil
}

func getPaginatedQueryOptionsAlertEvents(r *http.Request) (*storage.PaginatedQueryOptions[storage.AlertEventsFilters], error) {
	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}
	// The alert id comes from the path, not a query builder — events have no
	// user-facing filter surface, so only page size is read here.
	return pointer.For(storage.NewPaginatedQueryOptions(storage.AlertEventsFilters{}).
		WithPageSize(pageSize)), nil
}
