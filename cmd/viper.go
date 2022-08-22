package cmd

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	viper.AutomaticEnv()
}

func bindFlagsToViper(cmd *cobra.Command) error {
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

	return viper.BindPFlags(cmd.Flags())
}
