package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
)

// --- Rule handler tests -----------------------------------------------------

func TestCreateRule_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
}

func TestCreateRule_RejectsInvalidSpec(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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

func TestEvaluateRule_InvalidSafetyMargin(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

	id := uuid.New()
	body := []byte(`{"safetyMargin": "-30s"}`)
	r := httptest.NewRequest(http.MethodPost, "/rules/"+id.String()+"/evaluate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	// `>=` is HTML-escaped inside the JSON body, so match on the prefix only.
	require.Contains(t, rec.Body.String(), "safetyMargin must be")
}

// --- Alert handler tests -----------------------------------------------------

func TestAckAlert_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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

// TestListAlertEvents_Nominal — new endpoint surface for the event timeline.
func TestListAlertEvents_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

	id := uuid.New()
	resolved := models.AlertResolved
	events := []models.AlertEvent{
		{ID: uuid.New(), AlertID: id, Type: models.AlertEventFail, PrevStatus: &resolved, NewStatus: models.AlertOpen, At: time.Now().UTC()},
		{ID: uuid.New(), AlertID: id, Type: models.AlertEventFail, NewStatus: models.AlertOpen, At: time.Now().UTC().Add(-time.Hour)},
	}
	mockSvc.EXPECT().ListAlertEvents(gomock.Any(), id).Return(events, nil)

	r := httptest.NewRequest(http.MethodGet, "/alerts/"+id.String()+"/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[[]alertEventResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Len(t, *got.Data, 2)
	// First event was a reopen — predicate must propagate to the response.
	require.True(t, (*got.Data)[0].IsReopen)
	require.False(t, (*got.Data)[1].IsReopen)
}

// --- Evaluation handler tests -----------------------------------------------

func TestGetEvaluation_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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

// TestListRules_InvalidPageSize the pageSize param must reject non-integer
// values; the handler short-circuits at the boundary rather than handing
// garbage to storage.
func TestListRules_InvalidPageSize(t *testing.T) {
	t.Parallel()
	b, _ := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

	r := httptest.NewRequest(http.MethodGet, "/rules?pageSize=not-a-number", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestListEvaluations_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

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
