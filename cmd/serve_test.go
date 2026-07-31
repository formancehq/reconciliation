package cmd

import (
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/formancehq/go-libs/logging"
	"github.com/formancehq/go-libs/v5/pkg/audit"
	"github.com/formancehq/go-libs/v5/pkg/messaging/publish"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

func TestServerOptionsAreValid(t *testing.T) {
	cmd := newServeCommand("test")
	require.NoError(t, cmd.Flags().Set("postgres-uri", "postgres://user:pass@localhost:5432/reconciliation?sslmode=disable"))

	options, err := serverOptions(cmd, "test")
	require.NoError(t, err)

	options = append([]fx.Option{
		fx.NopLogger,
		fx.Supply(fx.Annotate(logging.Testing(), fx.As(new(logging.Logger)))),
		fx.Invoke(func(message.Publisher) {}),
	}, options...)
	require.NoError(t, fx.ValidateApp(options...))
}

func TestWorkerOptionsAreValid(t *testing.T) {
	cmd := newWorkerCommand("test")
	require.NoError(t, cmd.Flags().Set("postgres-uri", "postgres://user:pass@localhost:5432/reconciliation?sslmode=disable"))

	options, err := workerOptions(cmd, "test")
	require.NoError(t, err)
	options = append([]fx.Option{
		fx.NopLogger,
		fx.Supply(fx.Annotate(logging.Testing(), fx.As(new(logging.Logger)))),
		fx.Invoke(func(message.Publisher) {}),
	}, options...)
	require.NoError(t, fx.ValidateApp(options...))
}

func TestPublisherCircuitBreakerIsOptInOnAPIAndWorker(t *testing.T) {
	for _, command := range []*cobra.Command{newServeCommand("test"), newWorkerCommand("test")} {
		enabled, err := command.Flags().GetBool(publish.PublisherCircuitBreakerEnabledFlag)
		require.NoError(t, err)
		require.False(t, enabled)

		require.NoError(t, command.Flags().Set(publish.PublisherCircuitBreakerEnabledFlag, "true"))
		enabled, err = command.Flags().GetBool(publish.PublisherCircuitBreakerEnabledFlag)
		require.NoError(t, err)
		require.True(t, enabled)
	}
}

func TestServeDoesNotExposeEmbeddedSchedulerFlags(t *testing.T) {
	flags := newServeCommand("test").Flags()
	require.Nil(t, flags.Lookup("scheduler-enabled"))
	require.Nil(t, flags.Lookup("scheduler-interval"))
}

func TestAuditDefaultsToDisabled(t *testing.T) {
	cmd := newServeCommand("test")

	enabled, err := cmd.Flags().GetBool(audit.AuditEnabledFlag)
	require.NoError(t, err)
	require.False(t, enabled, "HTTP audit must stay opt-in")
}
