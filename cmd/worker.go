package cmd

import (
	"time"

	"github.com/formancehq/go-libs/aws/iam"
	"github.com/formancehq/go-libs/bun/bunconnect"
	"github.com/formancehq/go-libs/otlp/otlpmetrics"
	"github.com/formancehq/go-libs/otlp/otlptraces"
	"github.com/formancehq/go-libs/service"
	"github.com/formancehq/go-libs/v5/pkg/fx/messagingfx"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/formancehq/reconciliation/internal/worker"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

const (
	workerConcurrencyFlag     = "worker-concurrency"
	workerPollingFlag         = "worker-polling-interval"
	workerLeaseFlag           = "worker-lease-duration"
	workerHeartbeatFlag       = "worker-heartbeat-interval"
	workerRetentionFlag       = "worker-job-retention"
	workerPlannerIntervalFlag = "worker-planner-interval"
)

func newWorkerCommand(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "worker",
		Aliases:      []string{"run-worker"},
		Short:        "Run the durable reconciliation scheduler",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			options, err := workerOptions(cmd, version)
			if err != nil {
				return err
			}
			return service.New(cmd.OutOrStdout(), options...).Run(cmd)
		},
	}
	cmd.Flags().String(listenFlag, ":8080", "Health server listening address")
	cmd.Flags().String(stackURLFlag, "", "Stack url")
	cmd.Flags().String(stackClientIDFlag, "", "Stack client ID")
	cmd.Flags().String(stackClientSecretFlag, "", "Stack client secret")
	cmd.Flags().Int(workerConcurrencyFlag, 4, "Maximum concurrent evaluation jobs")
	cmd.Flags().Duration(workerPollingFlag, time.Second, "Job polling interval")
	cmd.Flags().Duration(workerLeaseFlag, 2*time.Minute, "Job claim lease duration")
	cmd.Flags().Duration(workerHeartbeatFlag, 30*time.Second, "Job claim heartbeat interval")
	cmd.Flags().Duration(workerRetentionFlag, 30*24*time.Hour, "Terminal job retention")
	cmd.Flags().Duration(workerPlannerIntervalFlag, time.Minute, "Cron planner interval")

	otlpmetrics.AddFlags(cmd.Flags())
	otlptraces.AddFlags(cmd.Flags())
	bunconnect.AddFlags(cmd.Flags())
	iam.AddFlags(cmd.Flags())
	service.AddFlags(cmd.Flags())
	publish.AddFlags(ServiceName, cmd.Flags())
	addAuditChainFlags(cmd)
	return cmd
}

func workerOptions(cmd *cobra.Command, version string) ([]fx.Option, error) {
	database, err := prepareDatabaseOptions(cmd)
	if err != nil {
		return nil, err
	}
	listen, _ := cmd.Flags().GetString(listenFlag)
	concurrency, _ := cmd.Flags().GetInt(workerConcurrencyFlag)
	polling, _ := cmd.Flags().GetDuration(workerPollingFlag)
	lease, _ := cmd.Flags().GetDuration(workerLeaseFlag)
	heartbeat, _ := cmd.Flags().GetDuration(workerHeartbeatFlag)
	retention, _ := cmd.Flags().GetDuration(workerRetentionFlag)
	plannerInterval, _ := cmd.Flags().GetDuration(workerPlannerIntervalFlag)

	return []fx.Option{
		database,
		otlptraces.FXModuleFromFlags(cmd),
		otlpmetrics.FXModuleFromFlags(cmd),
		messagingLoggingModule(cmd),
		messagingfx.PublishModuleFromFlags(cmd, service.IsDebug(cmd)),
		stackClientModule(cmd),
		worker.Module(worker.Config{
			Listen: listen, Version: version, Debug: service.IsDebug(cmd),
			Concurrency: concurrency, PollingInterval: polling, LeaseDuration: lease,
			HeartbeatInterval: heartbeat, Retention: retention, PlannerInterval: plannerInterval,
		}),
	}, nil
}
