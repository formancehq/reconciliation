package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/go-chi/chi/v5"

	"github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/health"
	"github.com/formancehq/go-libs/httpserver"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/formancehq/reconciliation/internal/api/service"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/events"
	domain "github.com/formancehq/reconciliation/internal/reconciliation"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"go.uber.org/fx"
)

const (
	ErrInvalidID            = "INVALID_ID"
	ErrMissingOrInvalidBody = "MISSING_OR_INVALID_BODY"
	ErrValidation           = "VALIDATION"
	ErrRuleBusy             = "RULE_BUSY"
	ErrRuleChanged          = "RULE_CHANGED"
	// ErrPeriodSealed covers both directions of the closing barrier: writing into
	// a closed period, and closing one twice.
	ErrPeriodSealed = "PERIOD_SEALED"
)

func healthCheckModule() fx.Option {
	return fx.Options(
		health.Module(),
		health.ProvideHealthCheck(func() health.NamedCheck {
			// Intentionally a process-only liveness check: it must NOT ping the
			// database or any dependency. The operator wires /_healthcheck to
			// BOTH the liveness and readiness probes, and the liveness probe
			// restarts the pod after ~40s of failures. Checking the DB here
			// would turn a transient DB outage into a restart storm across all
			// replicas. DB reachability and migration state are verified once at
			// startup (storage OnStart hook), which fails fast instead.
			return health.NewNamedCheck("default", health.CheckFn(func(ctx context.Context) error {
				return nil
			}))
		}),
	)
}

func HTTPModule(serviceInfo api.ServiceInfo, bind string) fx.Option {
	return fx.Options(
		healthCheckModule(),
		fx.Invoke(func(m *chi.Mux, lc fx.Lifecycle) {
			lc.Append(httpserver.NewHook(m, httpserver.WithAddress(bind)))
		}),
		fx.Provide(func(store *storage.Storage) service.Store {
			return store
		}),

		// Alert lifecycle → webhook events. The publisher is bound to the
		// storage seam so every alert_event row produces one outbound message
		// (see internal/events). The message bus publisher is optional: with
		// no broker configured, NewPublisher yields a silent no-op.
		fx.Provide(provideAlertEventPublisher),
		fx.Supply(serviceInfo),
		fx.Provide(fx.Annotate(service.NewSDKFormance, fx.As(new(service.SDKFormance)))),

		// V1 engine + templates wiring. The SDKFormance interface is a superset
		// of engine.SDKClient (it adds the legacy GetPoolBalances for the
		// /policies path), so it satisfies the engine's narrower contract via
		// Go's structural subtyping at this assignment.
		fx.Provide(provideResolvers),
		fx.Provide(provideEngine),
		fx.Provide(templates.DefaultRegistry),

		// NB: the v5 observe/log.Logger the messaging modules consume
		// is supplied at the serve level (cmd.messagingLoggingModule), not here —
		// providing it in both places makes fx reject a duplicate provider.

		fx.Provide(fx.Annotate(service.NewService, fx.As(new(backend.Service)))),
		fx.Provide(backend.NewDefaultBackend),
		fx.Provide(newRouter),
	)
}

// alertPublisherParams optionally receives the message-bus publisher
// messagingfx wires from --publisher-* flags. fx provides no message.Publisher
// when no broker is enabled, so the field is optional and resolves to nil;
// events.NewPublisher then returns a no-op publisher.
type alertPublisherParams struct {
	fx.In
	Publisher message.Publisher `optional:"true"`
}

func provideAlertEventPublisher(p alertPublisherParams) storage.AlertEventPublisher {
	return events.NewPublisher(p.Publisher)
}

// provideResolvers builds the engine.Resolvers fan-out from a single SDK
// client. Kept here (and not in the engine package) because the SDK type
// conversion is an api-layer wiring concern.
func provideResolvers(client service.SDKFormance) engine.Resolvers {
	var sdkClient engine.SDKClient = client
	return engine.Resolvers{
		Ledger:   engine.NewSDKLedgerResolver(sdkClient),
		Payments: engine.NewSDKPaymentsResolver(sdkClient),
	}
}

func provideEngine(resolvers engine.Resolvers) (*engine.Engine, error) {
	return engine.New(resolvers, engine.DefaultLimits)
}

func handleServiceErrors(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrRuleBusy):
		api.WriteErrorResponse(w, http.StatusConflict, ErrRuleBusy, err)
	case errors.Is(err, domain.ErrRuleChanged):
		api.WriteErrorResponse(w, http.StatusConflict, ErrRuleChanged, err)
	case errors.Is(err, service.ErrValidation):
		api.BadRequest(w, ErrValidation, err)
	case errors.Is(err, service.ErrInvalidID):
		api.BadRequest(w, ErrInvalidID, err)
	case errors.Is(err, storage.ErrInvalidQuery):
		api.BadRequest(w, ErrValidation, err)
	case errors.Is(err, storage.ErrNotFound):
		api.NotFound(w, err)
	// A write into closed books is a conflict rather than a bad request: the
	// request was well-formed, the books moved on. The double-seal and
	// not-sealable conflicts that used to sit here are gone — closing takes no
	// period id, so neither can be expressed.
	case errors.Is(err, storage.ErrPeriodSealed):
		api.WriteErrorResponse(w, http.StatusConflict, ErrPeriodSealed, err)
	case errors.Is(err, storage.ErrNoOpenClosure):
		api.InternalServerError(w, r, err)
	case errors.Is(err, storage.ErrAuditChainNotConfigured):
		api.InternalServerError(w, r, err)
	// V1 error classes
	case errors.Is(err, templates.ErrInvalidSpec):
		api.BadRequest(w, ErrValidation, err)
	case errors.Is(err, templates.ErrUnknownTemplate):
		api.BadRequest(w, ErrValidation, err)
	case errors.Is(err, engine.ErrCompile):
		api.BadRequest(w, ErrValidation, err)
	default:
		api.InternalServerError(w, r, err)
	}
}
