package cmd

import (
	"fmt"
	"os"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/pkg/env"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use: "reconciliation",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		sharedlogging.Errorf("cobra.Command.Execute: %s", err)
		os.Exit(1)
	}
}

func init() {
	cobra.CheckErr(env.Init(rootCmd.PersistentFlags()))
}
