package cmd

import (
	"github.com/formancehq/go-libs/aws/iam"

	"github.com/formancehq/go-libs/licence"

	sharedapi "github.com/formancehq/go-libs/api"
	"github.com/formancehq/go-libs/auth"
	"github.com/formancehq/go-libs/otlp/otlpmetrics"
	"github.com/formancehq/go-libs/otlp/otlptraces"
	"github.com/formancehq/go-libs/service"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/fx/messagingfx"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/api"
	"github.com/formancehq/reconciliation/internal/scheduler"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

func newServeCommand(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "serve",
		RunE: runServer(version),
	}
	cmd.Flags().String(listenFlag, ":8080", "Listening address")
	cmd.Flags().Bool(audit.AuditEnabledFlag, true, "Enable HTTP audit")

	otlpmetrics.AddFlags(cmd.Flags())
	otlptraces.AddFlags(cmd.Flags())
	auth.AddFlags(cmd.Flags())
	addLedgerFlags(cmd.Flags())
	iam.AddFlags(cmd.Flags())
	service.AddFlags(cmd.Flags())
	licence.AddFlags(cmd.Flags())
	publish.AddFlags(ServiceName, cmd.Flags())
	scheduler.AddFlags(cmd.Flags())

	return cmd
}

func runServer(version string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		options := make([]fx.Option, 0)
		options = append(options,
			ledgerClientModule(cmd),
			otlptraces.FXModuleFromFlags(cmd),
			otlpmetrics.FXModuleFromFlags(cmd),
			auth.FXModuleFromFlags(cmd),
		)

		listen, _ := cmd.Flags().GetString(listenFlag)
		auditEnabled, _ := cmd.Flags().GetBool(audit.AuditEnabledFlag)
		options = append(options,
			fx.Supply(audit.Config{Enabled: auditEnabled}),
			api.HTTPModule(sharedapi.ServiceInfo{
				Version: version,
				Debug:   service.IsDebug(cmd),
			}, listen),
			messagingfx.PublishModuleFromFlags(cmd, service.IsDebug(cmd)),
			licence.FXModuleFromFlags(cmd, ServiceName),
			scheduler.FXModuleFromFlags(cmd),
		)

		return service.New(cmd.OutOrStdout(), options...).Run(cmd)
	}
}
