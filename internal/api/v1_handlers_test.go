package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	domain "github.com/formancehq/reconciliation/internal/reconciliation"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
)

// --- Rule handler tests -----------------------------------------------------

func TestCreateRule_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	req := &service.CreateRuleRequest{
		Name:         "buildr-trust",
		TemplateKind: models.TemplateLedgerInvariant,
		TemplateSpec: json.RawMessage(`{"terms":[],"tolerance":{}}`),
		Severity:     models.SeverityHigh,
	}
	resp := &models.Rule{
		ID:             uuid.New(),
		Name:           req.Name,
		TemplateKind:   req.TemplateKind,
		TemplateSpec:   req.TemplateSpec,
		ExplanationCEL: `abs(...) <= 0`,
		Enabled:        true,
		Severity:       req.Severity,
		PeriodType:     models.PeriodTypeMonthly,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	mockSvc.EXPECT().CreateRule(gomock.Any(), req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/rules", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), `"explanationCEL"`)
	require.NotContains(t, rec.Body.String(), `"compiledCEL"`)
	var got sharedapi.BaseResponse[ruleResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Equal(t, resp.ID.String(), got.Data.ID)
	require.Equal(t, resp.ExplanationCEL, got.Data.ExplanationCEL)
	require.Equal(t, "monthly", got.Data.PeriodType, "rule response must expose periodType")
}

func TestCreateRule_RejectsInvalidSpec(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	req := &service.CreateRuleRequest{Name: "x", TemplateKind: models.TemplateLedgerInvariant, TemplateSpec: json.RawMessage(`{}`)}
	mockSvc.EXPECT().CreateRule(gomock.Any(), req).Return(nil, templates.ErrInvalidSpec)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/rules", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var e sharedapi.ErrorResponse
	sharedapi.Decode(t, rec.Body, &e)
	require.EqualValues(t, ErrValidation, e.ErrorCode)
}

func TestGetRule_NotFound(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	mockSvc.EXPECT().GetRule(gomock.Any(), id).Return(nil, errNotFoundForTest())

	r := httptest.NewRequest(http.MethodGet, "/rules/"+id.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetRule_InvalidUUID(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	r := httptest.NewRequest(http.MethodGet, "/rules/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var e sharedapi.ErrorResponse
	sharedapi.Decode(t, rec.Body, &e)
	require.EqualValues(t, ErrInvalidID, e.ErrorCode)
}

func TestDeleteRule_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	mockSvc.EXPECT().DeleteRule(gomock.Any(), id).Return(nil)

	r := httptest.NewRequest(http.MethodDelete, "/rules/"+id.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestEvaluateRule_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	resp := &models.Evaluation{
		ID:        uuid.New(),
		RuleID:    id,
		StartedAt: time.Now().UTC(),
		EndedAt:   time.Now().UTC(),
		Result:    models.EvaluationPass,
	}
	mockSvc.EXPECT().EvaluateRule(gomock.Any(), id, gomock.Any()).Return(resp, nil)

	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[evaluationResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Equal(t, string(models.EvaluationPass), got.Data.Result)
}

func TestEvaluateRule_BusyReturnsConflict(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})
	id := uuid.New()
	mockSvc.EXPECT().EvaluateRule(gomock.Any(), id, gomock.Any()).Return(nil, domain.ErrRuleBusy)

	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusConflict, rec.Code)
	var response sharedapi.ErrorResponse
	sharedapi.Decode(t, rec.Body, &response)
	require.EqualValues(t, ErrRuleBusy, response.ErrorCode)
}

func TestEvaluateRule_ChangedReturnsConflict(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})
	id := uuid.New()
	mockSvc.EXPECT().EvaluateRule(gomock.Any(), id, gomock.Any()).Return(nil, domain.ErrRuleChanged)

	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusConflict, rec.Code)
	var response sharedapi.ErrorResponse
	sharedapi.Decode(t, rec.Body, &response)
	require.EqualValues(t, ErrRuleChanged, response.ErrorCode)
}

// A chunked request body (ContentLength == -1) must still be decoded — the
// caller's at/safetyMargin must reach the service, not be silently dropped.
func TestEvaluateRule_ChunkedBodyHonored(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	var got service.EvaluateRuleRequest
	mockSvc.EXPECT().EvaluateRule(gomock.Any(), id, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, req service.EvaluateRuleRequest) (*models.Evaluation, error) {
			got = req
			return &models.Evaluation{ID: uuid.New(), RuleID: id, Result: models.EvaluationPass}, nil
		})

	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate",
		bytes.NewReader([]byte(`{"safetyMargin":"60s"}`)))
	r.ContentLength = -1 // simulate Transfer-Encoding: chunked
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 60*time.Second, got.SafetyMargin,
		"chunked body must be decoded; safetyMargin must not fall back to the 30s default")
}

func TestEvaluateRule_InvalidSafetyMargin(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	body := []byte(`{"safetyMargin": "not-a-duration"}`)
	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestEvaluateRule_NegativeSafetyMargin a negative margin would push PIT into
// the future, which is meaningless for reconciliation (always reads history,
// never projections). Must be rejected at the boundary with 400.
func TestEvaluateRule_NegativeSafetyMargin(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	body := []byte(`{"safetyMargin": "-30s"}`)
	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	// `>=` is HTML-escaped inside the JSON body, so match on the prefix only.
	require.Contains(t, rec.Body.String(), "safetyMargin must be")
}

// Per-source PIT overrides must reach the service, keyed as sent.
func TestEvaluateRule_SourcePITsThreaded(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	poolAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	var got service.EvaluateRuleRequest
	mockSvc.EXPECT().EvaluateRule(gomock.Any(), id, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, req service.EvaluateRuleRequest) (*models.Evaluation, error) {
			got = req
			return &models.Evaluation{ID: uuid.New(), RuleID: id, Result: models.EvaluationPass}, nil
		})

	body := []byte(`{"at":"` + poolAt.Format(time.RFC3339) + `","sourcePITs":{"pool:acct#0":"` + poolAt.Format(time.RFC3339) + `"}}`)
	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, got.SourcePITs["pool:acct#0"].Equal(poolAt),
		"per-source override must reach the service unchanged, got %v", got.SourcePITs)
	require.True(t, got.PIT.Equal(poolAt), "canonical alert-period PIT must reach the service")
}

func TestEvaluateRule_SourcePITsRequireAt(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	sourceAt := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	body := []byte(`{"sourcePITs":{"pool:acct#0":"` + sourceAt + `"}}`)
	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "at is required when sourcePITs are provided")
}

// A future `at` is meaningless for reconciliation (always reads history) — 400.
func TestEvaluateRule_FutureAtRejected(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	body := []byte(`{"at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`)
	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "at must be in the past")
}

// A future per-source override is rejected with 400, naming the offending key.
func TestEvaluateRule_FutureSourcePITRejected(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	body := []byte(`{"sourcePITs":{"ledger:main#0":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}}`)
	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "ledger:main#0")
}

// --- Alert handler tests -----------------------------------------------------

func TestAckAlert_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	req := &service.AckAlertRequest{By: "ops@buildr.com", Note: "investigating"}
	resp := &models.Alert{
		ID:               id,
		RuleID:           uuid.New(),
		Fingerprint:      "asset:USD/2",
		Status:           models.AlertAcknowledged,
		Severity:         models.SeverityHigh,
		LastEvaluationID: uuid.New(),
	}
	mockSvc.EXPECT().AckAlert(gomock.Any(), id, req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/alerts/"+id.String()+"/ack", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[alertResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Equal(t, string(models.AlertAcknowledged), got.Data.Status)
}

func TestResolveAlert_FixedByBooking(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	req := &service.ResolveAlertRequest{By: "ops", Note: "posted tx_abc", TransactionRefs: []string{"tx_abc"}}
	resp := &models.Alert{
		ID:               id,
		RuleID:           uuid.New(),
		Fingerprint:      "asset:USD/2",
		Status:           models.AlertResolved,
		Severity:         models.SeverityHigh,
		LastEvaluationID: uuid.New(),
		Resolution: &models.Resolution{
			Kind: models.ResolutionFixedByBooking,
			By:   req.By,
			At:   time.Now().UTC(),
		},
	}
	mockSvc.EXPECT().ResolveAlert(gomock.Any(), id, req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/alerts/"+id.String()+"/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAcceptAlert_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	req := &service.AcceptAlertRequest{By: "treasurer", Note: "settlement lag"}
	resp := &models.Alert{
		ID:               id,
		RuleID:           uuid.New(),
		Fingerprint:      "asset:USD/2",
		Status:           models.AlertResolved,
		Severity:         models.SeverityHigh,
		LastEvaluationID: uuid.New(),
		Resolution: &models.Resolution{
			Kind: models.ResolutionAcceptedByBusiness,
			By:   req.By,
			Note: req.Note,
			At:   time.Now().UTC(),
		},
	}
	mockSvc.EXPECT().AcceptAlert(gomock.Any(), id, req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/alerts/"+id.String()+"/accept", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSnoozeAlert_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	until := time.Now().UTC().Add(time.Hour).Round(time.Millisecond)
	req := &service.SnoozeAlertRequest{By: "alice", Until: until, Note: "migration"}
	resp := &models.Alert{
		ID:               id,
		RuleID:           uuid.New(),
		Fingerprint:      "asset:USD/2",
		Status:           models.AlertOpen,
		Severity:         models.SeverityHigh,
		LastEvaluationID: uuid.New(),
		Snooze:           &models.Snooze{Until: until, By: req.By, At: time.Now().UTC(), Note: req.Note},
	}
	mockSvc.EXPECT().SnoozeAlert(gomock.Any(), id, req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/alerts/"+id.String()+"/snooze", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestUnsnoozeAlert_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	req := &service.UnsnoozeAlertRequest{By: "bob"}
	resp := &models.Alert{
		ID:               id,
		RuleID:           uuid.New(),
		Fingerprint:      "asset:USD/2",
		Status:           models.AlertOpen,
		Severity:         models.SeverityHigh,
		LastEvaluationID: uuid.New(),
	}
	mockSvc.EXPECT().UnsnoozeAlert(gomock.Any(), id, req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/alerts/"+id.String()+"/unsnooze", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestListAlertEvents_Nominal — new endpoint surface for the event timeline.
func TestListAlertEvents_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	resolved := models.AlertResolved
	cursor := &bunpaginate.Cursor[models.AlertEvent]{
		Data: []models.AlertEvent{
			{ID: uuid.New(), AlertID: id, Type: models.AlertEventFail, PrevStatus: &resolved, NewStatus: models.AlertOpen, At: time.Now().UTC()},
			{ID: uuid.New(), AlertID: id, Type: models.AlertEventFail, NewStatus: models.AlertOpen, At: time.Now().UTC().Add(-time.Hour)},
		},
	}
	mockSvc.EXPECT().ListAlertEvents(gomock.Any(), id, gomock.Any()).Return(cursor, nil)

	r := httptest.NewRequest(http.MethodGet, "/alerts/"+id.String()+"/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[alertEventResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Len(t, got.Cursor.Data, 2)
	// First event was a reopen — predicate must propagate to the response.
	require.True(t, got.Cursor.Data[0].IsReopen)
	require.False(t, got.Cursor.Data[1].IsReopen)
}

func TestListAlertEvents_MissingAlertReturnsNotFound(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	mockSvc.EXPECT().ListAlertEvents(gomock.Any(), id, gomock.Any()).Return(nil, storage.ErrNotFound)

	r := httptest.NewRequest(http.MethodGet, "/alerts/"+id.String()+"/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Evaluation handler tests -----------------------------------------------

func TestGetEvaluation_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	resp := &models.Evaluation{ID: id, RuleID: uuid.New(), Result: models.EvaluationFail, StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	mockSvc.EXPECT().GetEvaluation(gomock.Any(), id).Return(resp, nil)

	r := httptest.NewRequest(http.MethodGet, "/evaluations/"+id.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[evaluationResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Equal(t, string(models.EvaluationFail), got.Data.Result)
}

// --- List handlers ----------------------------------------------------------

func TestListRules_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	cursor := &bunpaginate.Cursor[models.Rule]{
		PageSize: 15,
		Data: []models.Rule{
			{ID: uuid.New(), Name: "a", TemplateKind: models.TemplateLedgerInvariant, PeriodType: models.PeriodTypeMonthly},
			{ID: uuid.New(), Name: "b", TemplateKind: models.TemplateAccountThreshold, PeriodType: models.PeriodTypeDaily},
		},
	}
	mockSvc.EXPECT().ListRules(gomock.Any(), gomock.Any()).Return(cursor, nil)

	r := httptest.NewRequest(http.MethodGet, "/rules", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	// Captured before Decode, which consumes the buffer.
	body := rec.Body.String()
	var got sharedapi.BaseResponse[ruleResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Len(t, got.Cursor.Data, 2)

	// List items must go through renderRule, not serialise models.Rule
	// directly: the OpenAPI Rule schema marks both `periodType` and the
	// deprecated `cadence` required, and a pre-2.4.2 client reads the latter.
	// Serialising the model emitted `periodType` alone, breaking list responses
	// while create/get/patch kept working.
	require.Equal(t, "monthly", got.Cursor.Data[0].PeriodType)
	require.Equal(t, "daily", got.Cursor.Data[1].PeriodType)
	for i, item := range got.Cursor.Data {
		require.Equal(t, item.PeriodType, item.Cadence,
			"item %d: the deprecated alias must mirror periodType", i)
	}
	require.Contains(t, body, `"cadence"`,
		"list responses must still emit the deprecated alias")
}

// TestListRules_InvalidPageSize the pageSize param must reject non-integer
// values; the handler short-circuits at the boundary rather than handing
// garbage to storage.
func TestListRules_InvalidPageSize(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	r := httptest.NewRequest(http.MethodGet, "/rules?pageSize=not-a-number", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestListEvaluations_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	cursor := &bunpaginate.Cursor[models.Evaluation]{
		PageSize: 15,
		Data: []models.Evaluation{
			{ID: uuid.New(), RuleID: uuid.New(), Result: models.EvaluationFail, StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()},
		},
	}
	mockSvc.EXPECT().ListEvaluations(gomock.Any(), gomock.Any()).Return(cursor, nil)

	r := httptest.NewRequest(http.MethodGet, "/evaluations", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestListAlerts_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	cursor := &bunpaginate.Cursor[models.Alert]{
		PageSize: 15,
		Data: []models.Alert{
			{
				ID: uuid.New(), RuleID: uuid.New(),
				Fingerprint: "asset:USD/2",
				Status:      models.AlertOpen,
				Severity:    models.SeverityHigh,
			},
		},
	}
	mockSvc.EXPECT().ListAlerts(gomock.Any(), gomock.Any()).Return(cursor, nil)

	r := httptest.NewRequest(http.MethodGet, "/alerts", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[models.Alert]
	sharedapi.Decode(t, rec.Body, &got)
	require.Len(t, got.Cursor.Data, 1)
	require.Equal(t, "asset:USD/2", got.Cursor.Data[0].Fingerprint)
}

func TestGetAlert_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	alert := &models.Alert{
		ID:          id,
		RuleID:      uuid.New(),
		Fingerprint: "asset:USD/2",
		Status:      models.AlertResolved,
		Severity:    models.SeverityHigh,
	}
	mockSvc.EXPECT().GetAlert(gomock.Any(), id).Return(alert, nil)

	r := httptest.NewRequest(http.MethodGet, "/alerts/"+id.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[alertResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Equal(t, id.String(), got.Data.ID)
	require.Equal(t, string(models.AlertResolved), got.Data.Status)
}

// --- PATCH /rules handler ---------------------------------------------------

func TestPatchRule_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	id := uuid.New()
	// PATCH handler does PatchRule + GetRule (re-fetch after patch). Both must
	// be mocked, in order.
	mockSvc.EXPECT().PatchRule(gomock.Any(), id, gomock.Any()).Return(nil)
	mockSvc.EXPECT().GetRule(gomock.Any(), id).Return(&models.Rule{
		ID: id, Name: "patched", TemplateKind: models.TemplateLedgerInvariant, Enabled: false,
	}, nil)

	body := []byte(`{"name":"patched","enabled":false}`)
	r := httptest.NewRequest(http.MethodPatch, "/rules/"+id.String(), bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[ruleResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Equal(t, "patched", got.Data.Name)
	require.False(t, got.Data.Enabled)
}

func TestPatchRule_InvalidBody(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	r := httptest.NewRequest(http.MethodPatch, "/rules/"+uuid.New().String(), bytes.NewReader([]byte(`not-json`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- helpers ---------------------------------------------------------------

// errNotFoundForTest returns the sentinel storage.ErrNotFound without forcing
// the test to import storage just for one line.
func errNotFoundForTest() error {
	return notFoundErr{}
}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "not found" }
func (notFoundErr) Is(target error) bool {
	return target.Error() == "not found"
}

// TestCreateRule_PeriodTypeWireContract pins the cadence -> periodType rename at
// the HTTP boundary, which is the part of this rename that is actually breaking.
// Both directions are asserted, because a partial rename (say, request renamed
// but response not) would still pass a round-trip test that only checked one.
func TestCreateRule_PeriodTypeWireContract(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	// The request body speaks periodType; the service must receive it decoded.
	want := &service.CreateRuleRequest{
		Name:         "period-type-wire",
		TemplateKind: models.TemplateLedgerInvariant,
		TemplateSpec: json.RawMessage(`{"terms":[],"tolerance":{}}`),
		PeriodType:   models.PeriodTypeWeekly,
		// Set because the body carries the key explicitly; UnmarshalJSON
		// records presence so an explicit "" is distinguishable from absent.
		PeriodTypeWasProvided: true,
	}
	resp := &models.Rule{
		ID:           uuid.New(),
		Name:         want.Name,
		TemplateKind: want.TemplateKind,
		TemplateSpec: want.TemplateSpec,
		Enabled:      true,
		Severity:     models.SeverityHigh,
		PeriodType:   models.PeriodTypeWeekly,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	mockSvc.EXPECT().CreateRule(gomock.Any(), want).Return(resp, nil)

	r := httptest.NewRequest(http.MethodPost, "/rules", bytes.NewReader([]byte(
		`{"name":"period-type-wire","templateKind":"ledger_invariant",`+
			`"templateSpec":{"terms":[],"tolerance":{}},"periodType":"weekly"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), `"periodType":"weekly"`)
	// `cadence` is still emitted, deprecated, mirroring periodType — dropping it
	// would break clients generated against the pre-2.4.2 V1 contract, where it
	// is a required response field.
	require.Contains(t, rec.Body.String(), `"cadence":"weekly"`,
		"the deprecated mirror must keep V1 responses deserializable")
}

// TestCreateRule_LegacyCadenceKeyReachesService proves the deprecated `cadence`
// key survives decoding and is handed to the service, which is what makes
// dual-acceptance possible. The gomock argument match is the assertion: if the
// handler dropped the key, the expected request would not match.
//
// The resolution itself (cadence -> periodType, and rejection when the two
// disagree) is covered against the real service in
// internal/api/service/rule_period_type_compat_test.go — deliberately not here,
// where a mocked service could only ever confirm a handcrafted answer.
func TestCreateRule_LegacyCadenceKeyReachesService(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	want := &service.CreateRuleRequest{
		Name:         "legacy-cadence",
		TemplateKind: models.TemplateLedgerInvariant,
		TemplateSpec: json.RawMessage(`{"terms":[],"tolerance":{}}`),
		Cadence:      models.PeriodTypeMonthly,
	}
	resp := &models.Rule{
		ID: uuid.New(), Name: want.Name, TemplateKind: want.TemplateKind,
		TemplateSpec: want.TemplateSpec, Enabled: true,
		Severity: models.SeverityHigh, PeriodType: models.PeriodTypeMonthly,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	mockSvc.EXPECT().CreateRule(gomock.Any(), want).Return(resp, nil)

	r := httptest.NewRequest(http.MethodPost, "/rules", bytes.NewReader([]byte(
		`{"name":"legacy-cadence","templateKind":"ledger_invariant",`+
			`"templateSpec":{"terms":[],"tolerance":{}},"cadence":"monthly"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusCreated, rec.Code)
	// Both keys come back, so a pre-2.4.2 client reading `cadence` and a current
	// client reading `periodType` each see monthly.
	require.Contains(t, rec.Body.String(), `"periodType":"monthly"`)
	require.Contains(t, rec.Body.String(), `"cadence":"monthly"`)
}

// TestCreateRule_PeriodTypeKeyEdgeCases pins the presence semantics at the HTTP
// boundary, where the JSON actually gets decoded — a service-level test cannot
// tell an absent key from an explicit empty one, which is the whole point here.
func TestCreateRule_PeriodTypeKeyEdgeCases(t *testing.T) {
	t.Parallel()

	spec := `"templateSpec":{"terms":[],"tolerance":{}}`
	base := `"name":"edge","templateKind":"ledger_invariant",` + spec

	cases := []struct {
		name string
		body string
		// want is the PeriodType the service should receive; "" means the
		// request must be rejected before reaching the service.
		want     models.PeriodType
		wantCode int
	}{
		{
			name:     "explicit empty periodType is rejected, not defaulted",
			body:     `{` + base + `,"periodType":""}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "null periodType counts as absent and defaults",
			body:     `{` + base + `,"periodType":null}`,
			want:     "",
			wantCode: http.StatusCreated,
		},
		{
			name:     "empty cadence beside a real periodType is not a conflict",
			body:     `{` + base + `,"periodType":"monthly","cadence":""}`,
			want:     models.PeriodTypeMonthly,
			wantCode: http.StatusCreated,
		},
		{
			name:     "null cadence beside a real periodType is not a conflict",
			body:     `{` + base + `,"periodType":"monthly","cadence":null}`,
			want:     models.PeriodTypeMonthly,
			wantCode: http.StatusCreated,
		},
		{
			name:     "empty cadence alone is treated as unset",
			body:     `{` + base + `,"cadence":""}`,
			want:     "",
			wantCode: http.StatusCreated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, mockSvc := newTestingBackend(t)
			router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

			if tc.wantCode == http.StatusCreated {
				mockSvc.EXPECT().
					CreateRule(gomock.Any(), gomock.Cond(func(req *service.CreateRuleRequest) bool {
						// Validate runs inside the real service, so assert on
						// the decoded request the handler forwards.
						return req.PeriodType == tc.want
					})).
					Return(&models.Rule{
						ID: uuid.New(), Name: "edge", TemplateKind: models.TemplateLedgerInvariant,
						PeriodType: models.PeriodTypeContinuous,
						CreatedAt:  time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					}, nil)
			} else {
				mockSvc.EXPECT().
					CreateRule(gomock.Any(), gomock.Any()).
					Return(nil, templates.ErrInvalidSpec).
					AnyTimes()
			}

			r := httptest.NewRequest(http.MethodPost, "/rules", bytes.NewReader([]byte(tc.body)))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, r)

			require.Equal(t, tc.wantCode, rec.Code, "body: %s", rec.Body.String())
		})
	}
}
