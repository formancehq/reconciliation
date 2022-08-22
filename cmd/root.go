package cmd

import (
	"fmt"
	"os"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/go-libs/sharedlogging/sharedlogginglogrus"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	debugFlag = "debug"
)

var rootCmd = &cobra.Command{
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {

		if err := bindFlagsToViper(cmd); err != nil {
			return err
		}

		logrusLogger := logrus.New()
		if viper.GetBool(debugFlag) {
			logrusLogger.SetLevel(logrus.DebugLevel)
			logrusLogger.Infof("Debug mode enabled.")
		}
		logger := sharedlogginglogrus.New(logrusLogger)
		sharedlogging.SetFactory(sharedlogging.StaticLoggerFactory(logger))

		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		sharedlogging.Errorf("cobra.Command.Execute: %s", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	rootCmd.PersistentFlags().BoolP(debugFlag, "d", false, "Debug mode")
}
