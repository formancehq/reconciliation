package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

func TestListReconciliationsPageSize(t *testing.T) {
	t.Parallel()

	forgedCursor := func(pageSize uint64) string {
		q := storage.GetReconciliationsQuery{
			PageSize: pageSize,
			Options:  storage.NewPaginatedQueryOptions(storage.ReconciliationsFilters{}).WithPageSize(pageSize),
		}
		return bunpaginate.EncodeCursor(q)
	}

	type testCase struct {
		name               string
		queryParams        string
		expectedStatusCode int
		expectedPageSize   uint64
	}

	testCases := []testCase{
		{
			name:             "default page size",
			queryParams:      "",
			expectedPageSize: bunpaginate.QueryDefaultPageSize,
		},
		{
			name:             "valid page size",
			queryParams:      "?pageSize=10",
			expectedPageSize: 10,
		},
		{
			name:             "page size above max is capped",
			queryParams:      "?pageSize=1000",
			expectedPageSize: MaxPageSize,
		},
		{
			name:               "page size zero is rejected",
			queryParams:        "?pageSize=0",
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:               "non numeric page size is rejected",
			queryParams:        "?pageSize=abc",
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:             "valid cursor",
			queryParams:      "?cursor=" + forgedCursor(10),
			expectedPageSize: 10,
		},
		{
			name:               "forged cursor with page size zero is rejected",
			queryParams:        "?cursor=" + forgedCursor(0),
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:               "forged cursor with page size above max is rejected",
			queryParams:        "?cursor=" + forgedCursor(1000000),
			expectedStatusCode: http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		testCase := tc
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if testCase.expectedStatusCode == 0 {
				testCase.expectedStatusCode = http.StatusOK
			}

			backend, mockService := newTestingBackend(t)
			if testCase.expectedStatusCode == http.StatusOK {
				mockService.EXPECT().
					ListReconciliations(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ any, q storage.GetReconciliationsQuery) (*bunpaginate.Cursor[models.Reconciliation], error) {
						require.Equal(t, testCase.expectedPageSize, q.PageSize)
						return &bunpaginate.Cursor[models.Reconciliation]{
							PageSize: int(q.PageSize),
							Data:     []models.Reconciliation{},
						}, nil
					})
			}

			router := newRouter(backend, sharedapi.ServiceInfo{
				Debug: testing.Verbose(),
			}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{Enabled: true})

			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/reconciliations%s", testCase.queryParams), nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			require.Equal(t, testCase.expectedStatusCode, rec.Code)
		})
	}
}

func TestListPoliciesPageSize(t *testing.T) {
	t.Parallel()

	forgedCursor := func(pageSize uint64) string {
		q := storage.GetPoliciesQuery{
			PageSize: pageSize,
			Options:  storage.NewPaginatedQueryOptions(storage.PoliciesFilters{}).WithPageSize(pageSize),
		}
		return bunpaginate.EncodeCursor(q)
	}

	type testCase struct {
		name               string
		queryParams        string
		expectedStatusCode int
		expectedPageSize   uint64
	}

	testCases := []testCase{
		{
			name:               "page size zero is rejected",
			queryParams:        "?pageSize=0",
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:               "forged cursor with page size zero is rejected",
			queryParams:        "?cursor=" + forgedCursor(0),
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:               "forged cursor with page size above max is rejected",
			queryParams:        "?cursor=" + forgedCursor(1000000),
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:             "valid cursor",
			queryParams:      "?cursor=" + forgedCursor(10),
			expectedPageSize: 10,
		},
	}

	for _, tc := range testCases {
		testCase := tc
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if testCase.expectedStatusCode == 0 {
				testCase.expectedStatusCode = http.StatusOK
			}

			backend, mockService := newTestingBackend(t)
			if testCase.expectedStatusCode == http.StatusOK {
				mockService.EXPECT().
					ListPolicies(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ any, q storage.GetPoliciesQuery) (*bunpaginate.Cursor[models.Policy], error) {
						require.Equal(t, testCase.expectedPageSize, q.PageSize)
						return &bunpaginate.Cursor[models.Policy]{
							PageSize: int(q.PageSize),
							Data:     []models.Policy{},
						}, nil
					})
			}

			router := newRouter(backend, sharedapi.ServiceInfo{
				Debug: testing.Verbose(),
			}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{Enabled: true})

			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/policies%s", testCase.queryParams), nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			require.Equal(t, testCase.expectedStatusCode, rec.Code)
		})
	}
}
