package cmd

import (
	"github.com/numary/reconciliation/pkg/env"
	"github.com/spf13/viper"
)

func init() {
	env.LoadEnv(viper.GetViper())
}
