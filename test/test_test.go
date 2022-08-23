package test_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/pkg/env"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	httpClient    *http.Client
	serverBaseURL string
	workerBaseURL string
	//mongoClient   *mongo.Client
)

func TestMain(m *testing.M) {
	flagSet := pflag.NewFlagSet("test", pflag.ContinueOnError)
	if err := env.Init(flagSet); err != nil {
		panic(err)
	}

	httpClient = http.DefaultClient
	serverBaseURL = fmt.Sprintf("http://localhost%s",
		viper.GetString(constants.HttpBindAddressServerFlag))
	workerBaseURL = fmt.Sprintf("http://localhost%s",
		viper.GetString(constants.HttpBindAddressWorkerFlag))

	/*
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var err error
		mongoDBUri := viper.GetString(constants.StorageMongoConnStringFlag)
		if mongoClient, err = mongo.Connect(ctx, options.Client().ApplyURI(mongoDBUri)); err != nil {
			panic(err)
		}
	*/

	os.Exit(m.Run())
}

/*
func buffer(t *testing.T, v any) *bytes.Buffer {
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewBuffer(data)
}

func decodeCursorResponse[T any](t *testing.T, reader io.Reader) *sharedapi.Cursor[T] {
	res := sharedapi.BaseResponse[T]{}
	err := json.NewDecoder(reader).Decode(&res)
	require.NoError(t, err)
	return res.Cursor
}
*/
