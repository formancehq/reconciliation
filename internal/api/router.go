package api

import (
	"net/http"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/go-chi/chi/v5"

	"github.com/formancehq/go-libs/service"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/audit/httpaudit"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/health"
	"github.com/formancehq/reconciliation/internal/api/backend"
)

func newRouter(
	b backend.Backend,
	serviceInfo api.ServiceInfo,
	authenticator auth.Authenticator,
	healthController *health.HealthController,
	publisher message.Publisher,
	auditConfig audit.Config,
) *chi.Mux {
	r := chi.NewRouter()
	r.Use(httpaudit.Middleware(publisher, "audit-events", "reconciliation", nil, httpaudit.WithConfig(auditConfig)))
	r.Use(func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			handler.ServeHTTP(w, r)
		})
	})
	r.Get("/_healthcheck", healthController.Check)
	r.Get("/_info", api.InfoHandler(serviceInfo))

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(authenticator))
		r.Use(service.OTLPMiddleware("reconciliation", serviceInfo.Debug))

		r.Get("/reconciliations/{reconciliationID}", getReconciliationHandler(b))
		r.Get("/reconciliations", listReconciliationsHandler(b))

		r.Post("/policies", createPolicyHandler(b))
		r.Get("/policies", listPoliciesHandler(b))
		r.Delete("/policies/{policyID}", deletePolicyHandler(b))
		r.Get("/policies/{policyID}", getPolicyHandler(b))
		r.Post("/policies/{policyID}/reconciliation", reconciliationHandler(b))

		// V1 — Rule / Evaluation / Alert
		r.Post("/rules", createRuleHandler(b))
		r.Get("/rules", listRulesHandler(b))
		r.Get("/rules/{ruleID}", getRuleHandler(b))
		r.Patch("/rules/{ruleID}", patchRuleHandler(b))
		r.Delete("/rules/{ruleID}", deleteRuleHandler(b))
		r.Post("/rules/{ruleID}/evaluate", evaluateRuleHandler(b))

		r.Get("/evaluations", listEvaluationsHandler(b))
		r.Get("/evaluations/{evaluationID}", getEvaluationHandler(b))

		r.Get("/alerts", listAlertsHandler(b))
		r.Get("/alerts/{alertID}", getAlertHandler(b))
		r.Get("/alerts/{alertID}/events", listAlertEventsHandler(b))
		r.Post("/alerts/{alertID}/ack", ackAlertHandler(b))
		r.Post("/alerts/{alertID}/resolve", resolveAlertHandler(b))
		r.Post("/alerts/{alertID}/accept", acceptAlertHandler(b))
		r.Post("/alerts/{alertID}/snooze", snoozeAlertHandler(b))
		r.Post("/alerts/{alertID}/unsnooze", unsnoozeAlertHandler(b))
	})

	return r
}
