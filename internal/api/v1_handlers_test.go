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
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
)

// --- Rule handler tests -----------------------------------------------------

func TestCreateRule_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	req := &service.CreateRuleRequest{
		Name:         "buildr-trust",
		TemplateKind: models.TemplateLedgerInvariant,
		TemplateSpec: json.RawMessage(`{"terms":[],"tolerance":{}}`),
		Severity:     models.SeverityHigh,
	}
	resp := &models.Rule{
		ID:           uuid.New(),
		Name:         req.Name,
		TemplateKind: req.TemplateKind,
		TemplateSpec: req.TemplateSpec,
		CompiledCEL:  `abs(...) <= 0`,
		Enabled:      true,
		Severity:     req.Severity,
		PeriodType:   models.PeriodTypeMonthly,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	mockSvc.EXPECT().CreateRule(gomock.Any(), req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/rules", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusCreated, rec.Code)
	var got sharedapi.BaseResponse[ruleResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Equal(t, resp.ID.String(), got.Data.ID)
	require.Equal(t, resp.CompiledCEL, got.Data.CompiledCEL)
	require.Equal(t, "monthly", got.Data.PeriodType, "rule response must expose periodType")
}

func TestCreateRule_RejectsInvalidSpec(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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

// --- Alert handler tests -----------------------------------------------------

func TestAckAlert_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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

// --- List handlers ----------------------------------------------------------

func TestListRules_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	cursor := &bunpaginate.Cursor[models.Rule]{
		PageSize: 15,
		Data: []models.Rule{
			{ID: uuid.New(), Name: "a", TemplateKind: models.TemplateLedgerInvariant},
			{ID: uuid.New(), Name: "b", TemplateKind: models.TemplateAccountThreshold},
		},
	}
	mockSvc.EXPECT().ListRules(gomock.Any(), gomock.Any()).Return(cursor, nil)

	r := httptest.NewRequest(http.MethodGet, "/rules", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[models.Rule]
	sharedapi.Decode(t, rec.Body, &got)
	require.Len(t, got.Cursor.Data, 2)
}

// TestListRuleCaptures_Nominal — the capture-history endpoint: DTO rendering plus
// the optional ?period= filter threaded into the query.
func TestListRuleCaptures_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	ruleID := uuid.New()
	cursor := &bunpaginate.Cursor[models.Capture]{
		Data: []models.Capture{
			{TransactionID: 3, RuleID: ruleID, PeriodID: "2026-03", EvaluationID: uuid.New(), Verdict: "fail", Trigger: "manual", CapturedAt: time.Now().UTC()},
		},
	}

	mockSvc.EXPECT().
		ListCaptures(gomock.Any(), ruleID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, q store.GetCapturesQuery) (*bunpaginate.Cursor[models.Capture], error) {
			require.Equal(t, "2026-03", q.Options.Options.Period, "period query param is threaded into the filter")
			return cursor, nil
		})

	r := httptest.NewRequest(http.MethodGet, "/rules/"+ruleID.String()+"/captures?period=2026-03", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[captureResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Len(t, got.Cursor.Data, 1)
	require.Equal(t, uint64(3), got.Cursor.Data[0].TransactionID)
	require.Equal(t, "fail", got.Cursor.Data[0].Verdict)
	require.Equal(t, ruleID.String(), got.Cursor.Data[0].RuleID)
}

// TestListRules_InvalidPageSize the pageSize param must reject non-integer
// values; the handler short-circuits at the boundary rather than handing
// garbage to storage.
func TestListRules_InvalidPageSize(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

	r := httptest.NewRequest(http.MethodGet, "/rules?pageSize=not-a-number", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestListAlerts_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
	router := newRouter(b, sharedapi.ServiceInfo{}, ModuleInfo{}, nil, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})

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
