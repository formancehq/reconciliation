package cmd

import (
	"net/http"
	"syscall"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/pkg/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run reconciliation server",
	RunE:  RunServer,
}

func init() {
	rootCmd.AddCommand(serverCmd)
}

func RunServer(cmd *cobra.Command, args []string) error {
	sharedlogging.GetLogger(cmd.Context()).Debugf(
		"starting reconciliation server module: env variables: %+v viper keys: %+v",
		syscall.Environ(), viper.AllKeys())

	app := fx.New(
		server.StartModule(
			http.DefaultClient, viper.GetString(constants.HttpBindAddressServerFlag)))

	if err := app.Start(cmd.Context()); err != nil {
		return err
	}

	<-app.Done()

	if err := app.Stop(cmd.Context()); err != nil {
		return err
	}

	return nil
}
