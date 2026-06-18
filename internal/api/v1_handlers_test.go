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

// --- Incident handler tests -------------------------------------------------

func TestAckIncident_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

	id := uuid.New()
	req := &service.AckIncidentRequest{By: "ops@buildr.com", Note: "investigating"}
	resp := &models.Incident{
		ID:                id,
		RuleID:            uuid.New(),
		Fingerprint:       "asset:USD/2",
		Status:            models.IncidentAcknowledged,
		Severity:          models.SeverityHigh,
		FirstEvaluationID: uuid.New(),
		LastEvaluationID:  uuid.New(),
	}
	mockSvc.EXPECT().AckIncident(gomock.Any(), id, req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/incidents/"+id.String()+"/ack", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	var got sharedapi.BaseResponse[incidentResponse]
	sharedapi.Decode(t, rec.Body, &got)
	require.Equal(t, string(models.IncidentAcknowledged), got.Data.Status)
}

func TestResolveIncident_FixedByBooking(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

	id := uuid.New()
	req := &service.ResolveIncidentRequest{By: "ops", Note: "posted tx_abc", TransactionRefs: []string{"tx_abc"}}
	resp := &models.Incident{
		ID:                id,
		RuleID:            uuid.New(),
		Fingerprint:       "asset:USD/2",
		Status:            models.IncidentResolved,
		Severity:          models.SeverityHigh,
		FirstEvaluationID: uuid.New(),
		LastEvaluationID:  uuid.New(),
		Resolution: &models.Resolution{
			Kind: models.ResolutionFixedByBooking,
			By:   req.By,
			At:   time.Now().UTC(),
		},
	}
	mockSvc.EXPECT().ResolveIncident(gomock.Any(), id, req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/incidents/"+id.String()+"/resolve", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAcceptIncident_Nominal(t *testing.T) {
	t.Parallel()
	b, mockSvc := newTestingBackend(t)
	router := newRouter(b, sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory())

	id := uuid.New()
	req := &service.AcceptIncidentRequest{By: "treasurer", Note: "settlement lag"}
	resp := &models.Incident{
		ID:                id,
		RuleID:            uuid.New(),
		Fingerprint:       "asset:USD/2",
		Status:            models.IncidentResolved,
		Severity:          models.SeverityHigh,
		FirstEvaluationID: uuid.New(),
		LastEvaluationID:  uuid.New(),
		Resolution: &models.Resolution{
			Kind: models.ResolutionAcceptedByBusiness,
			By:   req.By,
			Note: req.Note,
			At:   time.Now().UTC(),
		},
	}
	mockSvc.EXPECT().AcceptIncident(gomock.Any(), id, req).Return(resp, nil)

	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/incidents/"+id.String()+"/accept", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
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

// --- helpers ---------------------------------------------------------------

// errNotFoundForTest returns the sentinel storage.ErrNotFound without forcing
// the test to import storage just for one line.
func errNotFoundForTest() error {
	type errType interface{ Error() string }
	return notFoundErr{}
}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "not found" }
func (notFoundErr) Is(target error) bool {
	return target.Error() == "not found"
}
