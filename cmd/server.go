package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/numary/go-libs/oauth2/oauth2introspect"
	"github.com/numary/go-libs/sharedauth"
	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/go-libs/sharedotlp/sharedotlptraces"
	"github.com/numary/reconciliation/pkg/api"
	"github.com/numary/reconciliation/pkg/storage"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/mongo"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"
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

func NewServer() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "server",
		Short:        "Launch the search server",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			debug := viper.GetBool(debugFlag)

			logger := logrus.New()
			logger.SetFormatter(&logrus.JSONFormatter{})
			if debug {
				logger.SetLevel(logrus.DebugLevel)
				logger.Debugln("Debug mode enabled")
			}
			logger.Debugf("Starting with config: %s", viper.AllSettings())

			bind := viper.GetString(bindFlag)
			if bind == "" {
				bind = defaultBind
			}

			options := make([]fx.Option, 0)

			if viper.GetBool(otelTracesFlag) {
				options = append(options, telemetryModule())
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

			options = append(options, apiModule("reconciliation", bind))

			app := fx.New(options...)

			err := app.Start(cmd.Context())
			if err != nil {
				return err
			}

			<-app.Done()

			return nil
		},
	}

	cmd.Flags().String(bindFlag, defaultBind, "http server address")
	cmd.Flags().Bool(otelTracesFlag, false, "Enable OpenTelemetry traces support")
	cmd.Flags().Bool(otelTracesBatchFlag, false, "Use OpenTelemetry batching")
	cmd.Flags().String(otelTracesExporterFlag, "stdout", "OpenTelemetry traces exporter")
	cmd.Flags().String(otelTracesExporterJaegerEndpointFlag, "", "OpenTelemetry traces Jaeger exporter endpoint")
	cmd.Flags().String(otelTracesExporterJaegerUserFlag, "", "OpenTelemetry traces Jaeger exporter user")
	cmd.Flags().String(otelTracesExporterJaegerPasswordFlag, "", "OpenTelemetry traces Jaeger exporter password")
	cmd.Flags().String(otelTracesExporterOTLPModeFlag, "grpc", "OpenTelemetry traces OTLP exporter mode (grpc|http)")
	cmd.Flags().String(otelTracesExporterOTLPEndpointFlag, "", "OpenTelemetry traces grpc endpoint")
	cmd.Flags().Bool(otelTracesExporterOTLPInsecureFlag, false, "OpenTelemetry traces grpc insecure")
	cmd.Flags().Bool(authBasicEnabledFlag, false, "Enable basic auth")
	cmd.Flags().StringSlice(authBasicCredentialsFlag, []string{}, "HTTP basic auth credentials (<username>:<password>)")
	cmd.Flags().Bool(authBearerEnabledFlag, false, "Enable bearer auth")
	cmd.Flags().String(authBearerIntrospectUrlFlag, "", "OAuth2 introspect URL")
	cmd.Flags().String(authBearerAudienceFlag, "", "OAuth2 audience template")

	return cmd
}

func apiModule(serviceName, bind string) fx.Option {
	return fx.Options(
		fx.Provide(fx.Annotate(func(db *mongo.Database, tp trace.TracerProvider) (http.Handler, error) {
			router := mux.NewRouter()
			if viper.GetBool(otelTracesFlag) {
				router.Use(otelmux.Middleware(serviceName, otelmux.WithTracerProvider(tp)))
			}
			router.Use(handlers.RecoveryHandler())
			router.Handle(healthCheckPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			protected := router.PathPrefix("/").Subrouter()

			methods := make([]sharedauth.Method, 0)
			if viper.GetBool(authBasicEnabledFlag) {
				credentials := sharedauth.Credentials{}
				for _, kv := range viper.GetStringSlice(authBasicCredentialsFlag) {
					parts := strings.SplitN(kv, ":", 2)
					credentials[parts[0]] = sharedauth.Credential{
						Password: parts[1],
						Scopes:   []string{"search"},
					}
				}
				methods = append(methods, sharedauth.NewHTTPBasicMethod(credentials))
			}
			if viper.GetBool(authBearerEnabledFlag) {
				methods = append(methods, sharedauth.NewHttpBearerMethod(
					sharedauth.NewIntrospectionValidator(
						oauth2introspect.NewIntrospecter(viper.GetString(authBearerIntrospectUrlFlag)),
						false,
						sharedauth.AudienceIn(viper.GetString(authBearerAudienceFlag)),
					),
				))
			}

			if len(methods) > 0 {
				protected.Use(sharedauth.Middleware(methods...))
			}

			router.PathPrefix("/").Handler(
				api.ReconciliationRouter(db),
			)

			return router, nil
		}, fx.ParamTags(``, `optional:"true"`))),
		fx.Invoke(func(lc fx.Lifecycle, handler http.Handler) {
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					sharedlogging.GetLogger(ctx).Infof("Starting http server on %s", bind)
					go func() {
						err := http.ListenAndServe(bind, handler)
						if err != nil {
							fmt.Fprintln(os.Stderr, err)
							os.Exit(1)
						}
					}()
					return nil
				},
			})
		}),
	)
}

func telemetryModule() fx.Option {
	return sharedotlptraces.TracesModule(sharedotlptraces.ModuleConfig{
		Batch:    viper.GetBool(otelTracesBatchFlag),
		Exporter: viper.GetString(otelTracesExporterFlag),
		JaegerConfig: func() *sharedotlptraces.JaegerConfig {
			if viper.GetString(otelTracesExporterFlag) != sharedotlptraces.JaegerExporter {
				return nil
			}
			return &sharedotlptraces.JaegerConfig{
				Endpoint: viper.GetString(otelTracesExporterJaegerEndpointFlag),
				User:     viper.GetString(otelTracesExporterJaegerUserFlag),
				Password: viper.GetString(otelTracesExporterJaegerPasswordFlag),
			}
		}(),
		OTLPConfig: func() *sharedotlptraces.OTLPConfig {
			if viper.GetString(otelTracesExporterFlag) != sharedotlptraces.OTLPExporter {
				return nil
			}
			return &sharedotlptraces.OTLPConfig{
				Mode:     viper.GetString(otelTracesExporterOTLPModeFlag),
				Endpoint: viper.GetString(otelTracesExporterOTLPEndpointFlag),
				Insecure: viper.GetBool(otelTracesExporterOTLPInsecureFlag),
			}
		}(),
	})
}
