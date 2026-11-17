package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/stretchr/testify/require"
)

func TestSEC100_HTTPRejectsUnsafeLedgerBeforePersistence(t *testing.T) {
	// Use the real service and template validation. A nil store/client fails if
	// validation ever lets an unsafe identifier reach persistence or the SDK.
	svc := service.NewService(nil, nil, nil, templates.DefaultRegistry(), engine.Resolvers{})
	router := newRouter(backend.NewDefaultBackend(svc), sharedapi.ServiceInfo{}, auth.NewNoAuth(), nil, publish.InMemory(), audit.Config{})
	for _, name := range []string{"main/../../payments", "main%2F..%2Fauth", "main?next=1", "main#fragment", ".", ".."} {
		for _, endpoint := range []string{"/rules", "/policies"} {
			t.Run(endpoint+"/"+name, func(t *testing.T) {
				var payload any = map[string]any{
					"name": "local test", "templateKind": "ledger_invariant",
					"templateSpec": map[string]any{
						"terms":     []map[string]any{{"ledger": name, "query": map[string]any{}, "sign": 1}},
						"tolerance": map[string]int{"USD/2": 0},
					},
				}
				if endpoint == "/policies" {
					payload = service.CreatePolicyRequest{Name: "local test", LedgerName: name, PaymentsPoolID: "3b61a0f0-0000-4000-8000-000000000000"}
				}
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				var apiError sharedapi.ErrorResponse
				sharedapi.Decode(t, response.Body, &apiError)
				require.EqualValues(t, ErrValidation, apiError.ErrorCode)
			})
		}
	}
}
