package cmd

import (
	"context"
	"github.com/gorilla/mux"
	"github.com/numary/go-libs/sharedotlp/pkg/sharedotlptraces"
	"github.com/numary/reconciliation/pkg/storage"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.uber.org/fx"
	"net/http"

	sharedhealth "github.com/numary/go-libs/sharedhealth/pkg"
)

const (
	mongodbUriFlag      = "mongodb-uri"
	mongodbDatabaseFlag = "mongodb-storage"

	bindFlag                             = "bind"
	otelTracesFlag                       = "otel-traces"
	otelTracesBatchFlag                  = "otel-traces-batch"
	otelTracesExporterFlag               = "otel-traces-exporter"
	otelTracesExporterJaegerEndpointFlag = "otel-traces-exporter-jaeger-endpoint"
	otelTracesExporterJaegerUserFlag     = "otel-traces-exporter-jaeger-user"
	otelTracesExporterJaegerPasswordFlag = "otel-traces-exporter-jaeger-password"
	otelTracesExporterOTLPModeFlag       = "otel-traces-exporter-otlp-mode"
	otelTracesExporterOTLPEndpointFlag   = "otel-traces-exporter-otlp-endpoint"
	otelTracesExporterOTLPInsecureFlag   = "otel-traces-exporter-otlp-insecure"

	authBasicEnabledFlag        = "auth-basic-enabled"
	authBasicCredentialsFlag    = "auth-basic-credentials"
	authBearerEnabledFlag       = "auth-bearer-enabled"
	authBearerIntrospectUrlFlag = "auth-bearer-introspect-url"
	authBearerAudienceFlag      = "auth-bearer-audience"

	defaultBind = ":8080"

	healthCheckPath = "/_healthcheck"
)

//func apiModule2(serviceName, bind string) fx.Option {
//	return fx.Options(
//		fx.Provide(fx.Annotate(func(db *mongo.Database, tp trace.TracerProvider) (http.Handler, error) {
//			router := mux.NewRouter()
//			if viper.GetBool(otelTracesFlag) {
//				router.Use(otelmux.Middleware(serviceName, otelmux.WithTracerProvider(tp)))
//			}
//			router.Use(handlers.RecoveryHandler())
//			router.Handle(healthCheckPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//				w.WriteHeader(http.StatusOK)
//			}))
//
//			protected := router.PathPrefix("/").Subrouter()
//
//			methods := make([]sharedauth.Method, 0)
//			if viper.GetBool(authBasicEnabledFlag) {
//				credentials := sharedauth.Credentials{}
//				for _, kv := range viper.GetStringSlice(authBasicCredentialsFlag) {
//					parts := strings.SplitN(kv, ":", 2)
//					credentials[parts[0]] = sharedauth.Credential{
//						Password: parts[1],
//						Scopes:   []string{"search"},
//					}
//				}
//				methods = append(methods, sharedauth.NewHTTPBasicMethod(credentials))
//			}
//			if viper.GetBool(authBearerEnabledFlag) {
//				methods = append(methods, sharedauth.NewHttpBearerMethod(
//					sharedauth.NewIntrospectionValidator(
//						oauth2introspect.NewIntrospecter(viper.GetString(authBearerIntrospectUrlFlag)),
//						false,
//						sharedauth.AudienceIn(viper.GetString(authBearerAudienceFlag)),
//					),
//				))
//			}
//
//			if len(methods) > 0 {
//				protected.Use(sharedauth.Middleware(methods...))
//			}
//
//			router.PathPrefix("/").Handler(
//				api.ReconciliationRouter(db),
//			)
//
//			return router, nil
//		}, fx.ParamTags(``, `optional:"true"`))),
//		fx.Invoke(func(lc fx.Lifecycle, handler http.Handler) {
//			lc.Append(fx.Hook{
//				OnStart: func(ctx context.Context) error {
//					sharedlogging.GetLogger(ctx).Infof("Starting http server on %s", bind)
//					go func() {
//						err := http.ListenAndServe(bind, handler)
//						if err != nil {
//							fmt.Fprintln(os.Stderr, err)
//							os.Exit(1)
//						}
//					}()
//					return nil
//				},
//			})
//		}),
//	)
//}

func newRouter(healthController *sharedhealth.HealthController) *mux.Router {
	r := mux.NewRouter()
	// Plug middleware to handle traces
	r.Use(otelmux.Middleware(ServiceName))
	r.
		Path("/_healthcheck").
		Methods(http.MethodGet).
		HandlerFunc(healthController.Check)
	return r
}

func healthCheckModule() fx.Option {
	return fx.Options(
		// The module will expose a *sharedhealth.HealthController
		// You must mount it on your api
		sharedhealth.Module(),
		sharedhealth.ProvideHealthCheck(func() sharedhealth.NamedCheck {
			return sharedhealth.NewNamedCheck("default", sharedhealth.CheckFn(func(ctx context.Context) error {
				// TODO: Implements your own logic
				return nil
			}))
		}),
	)
}

func apiModule() fx.Option {
	return fx.Options(
		fx.Provide(newRouter),
		fx.Invoke(func(lc fx.Lifecycle, router *mux.Router) {
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					go func() {
						err := http.ListenAndServe(":8080", router)
						if err != nil {
							panic(err)
						}
					}()
					return nil
				},
			})
		}),
	)
}

var serveCmd = &cobra.Command{
	Use: "serve",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return bindFlagsToViper(cmd)
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		options := []fx.Option{
			healthCheckModule(),
			apiModule(),
			fx.NopLogger,
		}
		// This will setup the telemetry stack
		// You have to add a middleware on your router to traces http requests
		if tm := sharedotlptraces.CLITracesModule(viper.GetViper()); tm != nil {
			options = append(options, tm)
		}

		mongodbUri := viper.GetString(mongodbUriFlag)
		if mongodbUri == "" {
			return errors.New("missing mongodb uri")
		}

		mongodbDatabase := viper.GetString(mongodbDatabaseFlag)
		if mongodbDatabase == "" {
			return errors.New("missing mongodb storage name")
		}

		options = append(options,
			storage.MongoModule(mongodbUri, mongodbDatabase),
			sharedotlptraces.TracesModule(sharedotlptraces.ModuleConfig{
				Exporter: viper.GetString(otelTracesExporterFlag),
				OTLPConfig: &sharedotlptraces.OTLPConfig{
					Mode:     viper.GetString(otelTracesExporterOTLPModeFlag),
					Endpoint: viper.GetString(otelTracesExporterOTLPEndpointFlag),
					Insecure: viper.GetBool(otelTracesExporterOTLPInsecureFlag),
				},
			}))

		app := fx.New(options...)
		err := app.Start(cmd.Context())
		if err != nil {
			return err
		}
		<-app.Done()
		return app.Err()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
