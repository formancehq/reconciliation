package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/contractversion"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestV1RenderersDoNotExposeV2AuditFields(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ruleJSON, err := json.Marshal(renderRule(&models.Rule{
		ID: uuid.New(), ContractVersion: models.ContractVersionV1, Revision: "sha256:revision",
		Name: "v1", TemplateKind: models.TemplateSourceParity, TemplateSpec: json.RawMessage(`{}`),
		Enabled: true, Severity: models.SeverityMedium, PeriodType: models.PeriodTypeContinuous,
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, err)
	var rule map[string]any
	require.NoError(t, json.Unmarshal(ruleJSON, &rule))
	require.NotContains(t, rule, "contractVersion")
	require.NotContains(t, rule, "revision")

	captureJSON, err := json.Marshal(renderCapture(&models.Capture{
		TransactionID: 1, ContractVersion: models.ContractVersionV1, RuleID: uuid.New(),
		EvaluationID: uuid.New(), PeriodID: "continuous", TemplateKind: string(models.TemplateSourceParity),
		Verdict: "pass", Trigger: "manual", CapturedAt: now, RuleRevision: "sha256:revision",
		PIT: now, StartedAt: now, Result: models.EvaluationPass,
	}))
	require.NoError(t, err)
	var capture map[string]any
	require.NoError(t, json.Unmarshal(captureJSON, &capture))
	for _, field := range []string{"contractVersion", "ruleRevision", "pit", "startedAt", "result", "error"} {
		require.NotContains(t, capture, field)
	}
}

func TestV2DoesNotExposeDeferredAlertEventsRoute(t *testing.T) {
	t.Parallel()

	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, ControlLedger(""), auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})
	req := httptest.NewRequest(http.MethodGet, "/v2/alerts/"+uuid.NewString()+"/events", nil)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	require.Equal(t, http.StatusNotFound, res.Code)
}

func TestV2CreateRouteScopesRequestAndRendersVersion(t *testing.T) {
	t.Parallel()

	b, svc := newTestingBackend(t)
	svc.EXPECT().CreateRule(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *service.CreateRuleRequest) (*models.Rule, error) {
		version, ok := contractversion.FromContext(ctx)
		require.True(t, ok)
		require.Equal(t, models.ContractVersionV2, version)
		return &models.Rule{
			ID:              uuid.New(),
			ContractVersion: models.ContractVersionV2,
			Name:            req.Name,
			TemplateKind:    req.TemplateKind,
			TemplateSpec:    req.TemplateSpec,
			CompiledCEL:     "balanceEquation(...) ",
		}, nil
	})

	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, ControlLedger(""), auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})
	req := httptest.NewRequest(http.MethodPost, "/v2/rules", strings.NewReader(`{"name":"eq","templateKind":"balance_equation","templateSpec":{}}`))
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	require.Equal(t, http.StatusCreated, res.Code)
	var payload struct {
		Data struct {
			ContractVersion int    `json:"contractVersion"`
			CompiledCEL     string `json:"compiledCEL"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &payload))
	require.Equal(t, 2, payload.Data.ContractVersion)
	require.Equal(t, "balanceEquation(...) ", payload.Data.CompiledCEL)
}
