package cmd

import (
	"os"

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
	cmd.Flags().String(uiURLFlag, defaultUIURL(), "Base URL where this module's business UI is served (advertised at /_info for the console shell to embed)")
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

// defaultUIURL is the standalone frontend dev URL unless overridden by env.
func defaultUIURL() string {
	if v := os.Getenv("RECONCILIATION_UI_URL"); v != "" {
		return v
	}

	return "http://localhost:3003"
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
		uiURL, _ := cmd.Flags().GetString(uiURLFlag)
		auditEnabled, _ := cmd.Flags().GetBool(audit.AuditEnabledFlag)
		options = append(options,
			fx.Supply(audit.Config{Enabled: auditEnabled}),
			api.HTTPModule(sharedapi.ServiceInfo{
				Version: version,
				Debug:   service.IsDebug(cmd),
			}, api.ModuleInfo{
				Version: version,
				Debug:   service.IsDebug(cmd),
				Name:    "reconciliation",
				Label:   "Reconciliation",
				Icon:    "scale",
				UIURL:   uiURL,
			}, listen),
			messagingfx.PublishModuleFromFlags(cmd, service.IsDebug(cmd)),
			licence.FXModuleFromFlags(cmd, ServiceName),
			scheduler.FXModuleFromFlags(cmd),
		)

		return service.New(cmd.OutOrStdout(), options...).Run(cmd)
	}
}
