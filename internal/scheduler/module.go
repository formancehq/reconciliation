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
		stopped := make(chan struct{})
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				go func() {
					defer close(stopped)
					s.Run(ctx)
				}()
				return nil
			},
			// Cancelling only tells the loop to stop starting work; Run then
			// drains the evaluations already in flight before returning. Waiting
			// here is what makes that drain mean anything — returning straight
			// after cancel would let the process exit mid-evaluation, which is
			// the very thing Run's split contexts exist to prevent.
			//
			// The wait is bounded by fx's own stop context so a stuck drain
			// cannot hold the process open past its shutdown budget, and it
			// never fails shutdown: by this point the loop is stopped either
			// way, and returning an error would only mask the real cause.
			OnStop: func(stopCtx context.Context) error {
				cancel()
				select {
				case <-stopped:
				case <-stopCtx.Done():
					logger.Errorf("reconciliation scheduler did not drain within the shutdown budget: %s", stopCtx.Err())
				}
				return nil
			},
		})
	})
}
