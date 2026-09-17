package ledger

import (
	"context"
	"net"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// dialBufconn serves impl over an in-process listener and returns a Client
// built through NewClient — the real constructor, so whatever these tests
// observe on the server side is what production puts on the wire, dial options
// included.
func dialBufconn(t *testing.T, impl servicepb.BucketServiceServer) *Client {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	servicepb.RegisterBucketServiceServer(server, impl)

	go func() { _ = server.Serve(listener) }()

	t.Cleanup(server.Stop)

	client, err := NewClient("passthrough:///bufnet", insecure.NewCredentials(),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return client
}
