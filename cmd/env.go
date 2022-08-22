package cmd

import (
	"github.com/numary/reconciliation/internal/env"
	"github.com/spf13/viper"
)

func init() {
	env.LoadEnv(viper.GetViper())
}
