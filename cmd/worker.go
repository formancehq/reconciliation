package cmd

import (
	"net/http"
	"syscall"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/internal/worker"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Run reconciliation worker",
	RunE:  RunWorker,
}

func RunWorker(cmd *cobra.Command, args []string) error {
	sharedlogging.GetLogger(cmd.Context()).Debugf(
		"starting reconciliation worker module: env variables: %+v viper keys: %+v",
		syscall.Environ(), viper.AllKeys())

	app := fx.New(worker.StartModule(http.DefaultClient))

	if err := app.Start(cmd.Context()); err != nil {
		return err
	}

	<-app.Done()

	if err := app.Stop(cmd.Context()); err != nil {
		return err
	}

	return nil
}

func init() {
	rootCmd.AddCommand(workerCmd)
}
