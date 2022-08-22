package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	ServiceName = "CHANGEME"
	Version     = "develop"
	BuildDate   = "-"
	Commit      = "-"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Get version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Version: %s \n", Version)
		fmt.Printf("Date: %s \n", BuildDate)
		fmt.Printf("Commit: %s \n", Commit)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
