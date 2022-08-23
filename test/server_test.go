package test_test

import (
	"net/http"
	"testing"

	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/pkg/server"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx/fxtest"
)

func TestServer(t *testing.T) {
	serverApp := fxtest.New(t,
		server.StartModule(
			httpClient, viper.GetString(constants.HttpBindAddressServerFlag)))

	t.Run("start", func(t *testing.T) {
		serverApp.RequireStart()
	})

	t.Run("health check", func(t *testing.T) {
		resp, err := http.Get(serverBaseURL + server.PathHealthCheck)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("stop", func(t *testing.T) {
		serverApp.RequireStop()
	})
}
