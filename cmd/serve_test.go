package cmd

import (
	"testing"

	"github.com/formancehq/go-libs/logging"
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
	}, options...)
	require.NoError(t, fx.ValidateApp(options...))
}
