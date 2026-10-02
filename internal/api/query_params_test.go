package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
)

func TestValidateQueryParams(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		allowed []string
		query   string
		message string
	}{
		{name: "no parameters", allowed: []string{QueryKeyPageSize}, query: ""},
		{name: "allowed parameters", allowed: []string{QueryKeyPageSize, QueryKeyCursor}, query: "pageSize=10&cursor=abc"},
		{name: "empty allowed value", allowed: []string{QueryKeyPageSize}, query: "pageSize="},
		{
			name: "one unknown parameter", allowed: []string{QueryKeyQuery, QueryKeyPageSize}, query: "status=OPEN",
			message: `unknown query parameter "status": this endpoint reads only pageSize and query`,
		},
		{
			name: "unknown beside allowed", allowed: []string{QueryKeyCursor, QueryKeyPageSize, QueryKeyQuery}, query: "pageSize=10&since=2026-06-01",
			message: `unknown query parameter "since": this endpoint reads only cursor, pageSize, and query`,
		},
		{
			name: "several unknown parameters, sorted", allowed: []string{QueryKeyPageSize}, query: "status=OPEN&ruleId=x&severity=high",
			message: `unknown query parameters "ruleId", "severity", "status": this endpoint reads only pageSize`,
		},
		{
			name: "endpoint without parameters", allowed: nil, query: "bogus=1",
			message: `unknown query parameter "bogus": this endpoint reads no query parameters`,
		},
		{
			name: "keys are case-sensitive", allowed: []string{QueryKeyPageSize}, query: "pagesize=10",
			message: `unknown query parameter "pagesize": this endpoint reads only pageSize`,
		},
		// url.URL.Query drops a malformed pair without an error, so these
		// would otherwise reach the handler as an empty, unfiltered query.
		{
			name: "invalid escape", allowed: []string{QueryKeyPageSize}, query: "status=%ZZ",
			message: `invalid query string: invalid URL escape "%ZZ"`,
		},
		{
			name: "semicolon separator", allowed: []string{QueryKeyPageSize}, query: "pageSize=10;status=OPEN",
			message: `invalid query string: invalid semicolon separator in query`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateQueryParams(tc.query, tc.allowed...)
			if tc.message == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.message)
		})
	}
}

// Every list endpoint rejects a parameter it does not read, before any service
// or ledger call runs. The strict mock service fails the test on any call, and
// the nil ledger client would panic if an introspection handler ran. The exact
// message pins each endpoint's allowlist, so a parameter added to or removed
// from it fails here.
func TestListEndpointsRejectUnknownQueryParameters(t *testing.T) {
	t.Parallel()

	id := uuid.NewString()
	for _, tc := range []struct {
		path  string
		reads string
	}{
		{path: "/rules", reads: "reads only cursor, pageSize, and query"},
		{path: "/alerts", reads: "reads only cursor, pageSize, and query"},
		{path: "/rules/" + id + "/captures", reads: "reads only cursor, pageSize, and period"},
		{path: "/rules/" + id + "/timeline", reads: "reads only cursor and pageSize"},
		{path: "/alerts/" + id + "/events", reads: "reads only cursor and pageSize"},
		{path: "/audit/entries", reads: "reads only limit and scope"},
		{path: "/audit/signing-keys", reads: "reads no query parameters"},
		{path: "/ledgers", reads: "reads no query parameters"},
		{path: "/ledgers/main/meta-fields", reads: "reads no query parameters"},
		{path: "/ledgers/main/accounts", reads: "reads only filter, limit, and prefix"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			b, _ := newTestingBackend(t)
			router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, ControlLedger(""), auth.NewNoAuth(), AuthConfig{}, nil, publish.InMemory(), audit.Config{})

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path+"?bogus=1", nil))

			require.Equal(t, http.StatusBadRequest, rec.Code)
			var got sharedapi.ErrorResponse
			sharedapi.Decode(t, rec.Body, &got)
			require.Equal(t, ErrValidation, got.ErrorCode)
			require.Equal(t, `unknown query parameter "bogus": this endpoint `+tc.reads, got.ErrorMessage)
		})
	}
}

// The plain filter parameters the docs once described are now rejected, and so
// is a malformed pair, which url.URL.Query would otherwise drop in silence.
func TestListAlerts_RejectsPlainAndMalformedParameters(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		target  string
		message string
	}{
		{target: "/alerts?status=OPEN", message: `unknown query parameter "status": this endpoint reads only cursor, pageSize, and query`},
		{target: "/alerts?status=%ZZ", message: `invalid query string: invalid URL escape "%ZZ"`},
		{target: "/alerts?pageSize=10;status=OPEN", message: `invalid query string: invalid semicolon separator in query`},
	} {
		t.Run(tc.target, func(t *testing.T) {
			t.Parallel()

			b, _ := newTestingBackend(t)
			router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, ControlLedger(""), auth.NewNoAuth(), AuthConfig{}, nil, publish.InMemory(), audit.Config{})

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))

			require.Equal(t, http.StatusBadRequest, rec.Code)
			var got sharedapi.ErrorResponse
			sharedapi.Decode(t, rec.Body, &got)
			require.Equal(t, ErrValidation, got.ErrorCode)
			require.Equal(t, tc.message, got.ErrorMessage)
		})
	}
}

// Each contract list still accepts every parameter its handler reads, the cursor
// included, so the allowlists cannot drift from the handlers unnoticed.
func TestListEndpointsAcceptTheirParameters(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	filter := url.QueryEscape(`{"$match":{"status":"OPEN"}}`)

	for _, tc := range []struct {
		name   string
		target string
		expect func(svc *backend.MockService)
	}{
		{
			name:   "rules",
			target: "/rules?pageSize=10&query=" + filter + "&cursor=",
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListRules(gomock.Any(), gomock.Any()).Return(&bunpaginate.Cursor[models.Rule]{}, nil)
				svc.EXPECT().AlertCountsByRule(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
		},
		{
			name:   "rules page two",
			target: "/rules?cursor=" + url.QueryEscape(bunpaginate.EncodeCursor(store.GetRulesQuery{PageSize: 10})),
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListRules(gomock.Any(), gomock.Any()).Return(&bunpaginate.Cursor[models.Rule]{}, nil)
				svc.EXPECT().AlertCountsByRule(gomock.Any(), gomock.Any()).Return(nil, nil)
			},
		},
		{
			name:   "alerts",
			target: "/alerts?pageSize=10&query=" + filter,
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListAlerts(gomock.Any(), gomock.Any()).Return(&bunpaginate.Cursor[models.Alert]{}, nil)
			},
		},
		{
			name:   "alerts page two",
			target: "/alerts?cursor=" + url.QueryEscape(bunpaginate.EncodeCursor(store.GetAlertsQuery{PageSize: 10})),
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListAlerts(gomock.Any(), gomock.Any()).Return(&bunpaginate.Cursor[models.Alert]{}, nil)
			},
		},
		{
			name:   "captures",
			target: "/rules/" + id.String() + "/captures?pageSize=10&period=2026-03",
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListCaptures(gomock.Any(), id, gomock.Any()).Return(&bunpaginate.Cursor[models.Capture]{}, nil)
			},
		},
		{
			name:   "captures page two",
			target: "/rules/" + id.String() + "/captures?cursor=" + url.QueryEscape(bunpaginate.EncodeCursor(store.GetCapturesQuery{PageSize: 10})),
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListCaptures(gomock.Any(), id, gomock.Any()).Return(&bunpaginate.Cursor[models.Capture]{}, nil)
			},
		},
		{
			name:   "rule timeline",
			target: "/rules/" + id.String() + "/timeline?pageSize=10",
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListRuleActivities(gomock.Any(), id, gomock.Any()).Return(&bunpaginate.Cursor[models.RuleActivity]{}, nil)
			},
		},
		{
			name:   "rule timeline page two",
			target: "/rules/" + id.String() + "/timeline?cursor=" + url.QueryEscape(bunpaginate.EncodeCursor(store.GetRuleActivitiesQuery{PageSize: 10})),
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListRuleActivities(gomock.Any(), id, gomock.Any()).Return(&bunpaginate.Cursor[models.RuleActivity]{}, nil)
			},
		},
		{
			name:   "alert events",
			target: "/alerts/" + id.String() + "/events?pageSize=10",
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListAlertEvents(gomock.Any(), id, gomock.Any()).Return(&bunpaginate.Cursor[models.AlertEvent]{}, nil)
			},
		},
		{
			name:   "alert events page two",
			target: "/alerts/" + id.String() + "/events?cursor=" + url.QueryEscape(bunpaginate.EncodeCursor(store.GetAlertEventsQuery{PageSize: 10})),
			expect: func(svc *backend.MockService) {
				svc.EXPECT().ListAlertEvents(gomock.Any(), id, gomock.Any()).Return(&bunpaginate.Cursor[models.AlertEvent]{}, nil)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b, svc := newTestingBackend(t)
			tc.expect(svc)
			router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, ControlLedger(""), auth.NewNoAuth(), AuthConfig{}, nil, publish.InMemory(), audit.Config{})

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}
