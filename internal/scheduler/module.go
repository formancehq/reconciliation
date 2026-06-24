package scheduler

import (
	"context"
	"time"

	v5log "github.com/formancehq/go-libs/v5/pkg/observe/log"
	"github.com/formancehq/reconciliation/internal/api/backend"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.uber.org/fx"
)

const (
	enabledFlag  = "scheduler-enabled"
	intervalFlag = "scheduler-interval"
)

// AddFlags registers the scheduler's flags on the serve command.
func AddFlags(flags *pflag.FlagSet) {
	flags.Bool(enabledFlag, false, "Run the in-process cron scheduler. Single-instance only — see internal/scheduler.")
	flags.Duration(intervalFlag, time.Minute, "Scheduler tick interval (cron is minute-granular)")
}

// FXModuleFromFlags wires the scheduler into the fx graph when --scheduler-enabled
// is set; otherwise it is a no-op. Disabled by default so existing deployments
// and the migrate path are unaffected.
func FXModuleFromFlags(cmd *cobra.Command) fx.Option {
	enabled, _ := cmd.Flags().GetBool(enabledFlag)
	if !enabled {
		return fx.Options()
	}
	interval, _ := cmd.Flags().GetDuration(intervalFlag)
	return fx.Invoke(func(lc fx.Lifecycle, svc backend.Service, logger v5log.Logger) {
		// backend.Service is a superset of RuleService.
		s := New(svc, interval, logger)
		// Detached context: the loop lives for the app's lifetime, not the
		// (short-lived) OnStart context. OnStop cancels it.
		ctx, cancel := context.WithCancel(context.Background())
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error { go s.Run(ctx); return nil },
			OnStop:  func(context.Context) error { cancel(); return nil },
		})
	})
}
