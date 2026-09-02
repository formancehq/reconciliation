package api

import (
	"net/http"
	"os"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/go-chi/chi/v5"

	"github.com/formancehq/go-libs/service"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/audit/httpaudit"
	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/health"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/contractversion"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/models"
)

func contractVersionMiddleware(version models.ContractVersion) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(contractversion.WithContext(r.Context(), version)))
		})
	}
}

func mountRuleAndAlertRoutes(r chi.Router, b backend.Backend, includeDeferredAlertEvents bool) {
	r.Post("/rules", createRuleHandler(b))
	r.Get("/rules", listRulesHandler(b))
	r.Get("/rules/{ruleID}", getRuleHandler(b))
	r.Patch("/rules/{ruleID}", patchRuleHandler(b))
	r.Delete("/rules/{ruleID}", deleteRuleHandler(b))
	r.Post("/rules/{ruleID}/evaluate", evaluateRuleHandler(b))
	r.Get("/rules/{ruleID}/captures", listRuleCapturesHandler(b))
	r.Get("/rules/{ruleID}/timeline", listRuleActivitiesHandler(b))

	r.Get("/alerts", listAlertsHandler(b))
	r.Get("/alerts/{alertID}", getAlertHandler(b))
	if includeDeferredAlertEvents {
		r.Get("/alerts/{alertID}/events", listAlertEventsHandler(b))
	}
	r.Post("/alerts/{alertID}/ack", ackAlertHandler(b))
	r.Post("/alerts/{alertID}/resolve", resolveAlertHandler(b))
	r.Post("/alerts/{alertID}/accept", acceptAlertHandler(b))
	r.Post("/alerts/{alertID}/snooze", snoozeAlertHandler(b))
	r.Post("/alerts/{alertID}/unsnooze", unsnoozeAlertHandler(b))
}

func newRouter(
	b backend.Backend,
	serviceInfo api.ServiceInfo,
	moduleInfo ModuleInfo,
	ledgerClient *ledger.Client,
	controlLedger ControlLedger,
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
	// Propagate a request-scoped logger so handlers can log via
	// log.FromContext — service.OTLPMiddleware only adds tracing, not a context
	// logger. Honors --debug (the introspection handlers debug-log the ledger
	// read errors they otherwise swallow into a best-effort empty response).
	reqLogger := v5log.NewDefaultLogger(os.Stdout, serviceInfo.Debug, false, false)
	r.Use(func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler.ServeHTTP(w, r.WithContext(v5log.ContextWithLogger(r.Context(), reqLogger)))
		})
	})
	r.Get("/_healthcheck", healthController.Check)
	// Custom /_info: extends the standard go-libs ServiceInfo with the
	// UI-federation fields ({name,label,icon,uiUrl}) the console shell reads to
	// discover and embed this module's standalone business UI.
	r.Get("/_info", infoHandler(moduleInfo))

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(authenticator))
		// After auth has verified the token, bind its subject onto the context so
		// lifecycle writes can attribute the action to the authenticated human
		// (EN-1930, P1.2). Must follow auth.Middleware — see subjectMiddleware.
		r.Use(subjectMiddleware)
		r.Use(service.OTLPMiddleware("reconciliation", serviceInfo.Debug))

		// V1 (default, unprefixed) and V2 (/v2) rule/alert surfaces, each scoped
		// by its contract version so the shared handlers serialize the right shape.
		r.Group(func(r chi.Router) {
			r.Use(contractVersionMiddleware(models.ContractVersionV1))
			mountRuleAndAlertRoutes(r, b, true)
		})
		r.Route("/v2", func(r chi.Router) {
			r.Use(contractVersionMiddleware(models.ContractVersionV2))
			mountRuleAndAlertRoutes(r, b, false)
		})

		// Ledger introspection — read-only helpers that let the standalone UI's
		// rule builder offer live ledger-name / metadata-key / account
		// autosuggest, sourced through this module's ledger gRPC connection (UI
		// federation). Version-agnostic, so mounted outside the V1/V2 groups.
		r.Get("/ledgers", listLedgersHandler(ledgerClient))
		r.Get("/ledgers/{ledger}/meta-fields", listLedgerMetaFieldsHandler(ledgerClient))
		r.Get("/ledgers/{ledger}/accounts", listLedgerAccountsHandler(ledgerClient))

		// Audit: the public signing keys the control-ledger writes are signed
		// with, plus the signed entries themselves — so the audit tab (and an
		// external auditor) can verify each write from the public key alone, with
		// no ledger access (EN-1930, P1.3).
		r.Get("/audit/signing-keys", listSigningKeysHandler(ledgerClient))
		r.Get("/audit/entries", listAuditEntriesHandler(ledgerClient, controlLedger))
	})

	return r
}
