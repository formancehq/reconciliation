package cmd

import (
	"github.com/numary/reconciliation/pkg/worker"
	"github.com/spf13/cobra"
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Start worker",
	Run:   worker.Start,
}

func init() {
	rootCmd.AddCommand(workerCmd)
}
