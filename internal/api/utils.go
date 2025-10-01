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

func getPaginatedQueryOptionsReconciliations(r *http.Request) (*storage.PaginatedQueryOptions[storage.ReconciliationsFilters], error) {
	qb, err := getQueryBuilder(r)
	if err != nil {
		return nil, err
	}

	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}

	filters := storage.ReconciliationsFilters{}
	return pointer.For(storage.NewPaginatedQueryOptions(filters).
		WithQueryBuilder(qb).
		WithPageSize(pageSize)), nil
}

func getPaginatedQueryOptionsPolicies(r *http.Request) (*storage.PaginatedQueryOptions[storage.PoliciesFilters], error) {
	qb, err := getQueryBuilder(r)
	if err != nil {
		return nil, err
	}

	pageSize, err := getPageSize(r)
	if err != nil {
		return nil, err
	}

	filters := storage.PoliciesFilters{}
	return pointer.For(storage.NewPaginatedQueryOptions(filters).
		WithQueryBuilder(qb).
		WithPageSize(pageSize)), nil
}
