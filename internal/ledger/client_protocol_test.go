package ledger

import (
	"context"
	"net"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/grpcprotocol"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// protocolCapturingServer records the metadata of the last RPC it served, so a
// test can assert on what actually crossed the wire rather than on the dial
// options we believe we set.
type protocolCapturingServer struct {
	servicepb.UnimplementedBucketServiceServer
	unary  metadata.MD
	stream metadata.MD
}

func (s *protocolCapturingServer) GetLedger(ctx context.Context, _ *servicepb.GetLedgerRequest) (*commonpb.LedgerInfo, error) {
	s.unary, _ = metadata.FromIncomingContext(ctx)

	return &commonpb.LedgerInfo{Name: "default"}, nil
}

func (s *protocolCapturingServer) ListLedgers(_ *servicepb.ListLedgersRequest, stream grpc.ServerStreamingServer[commonpb.LedgerInfo]) error {
	s.stream, _ = metadata.FromIncomingContext(stream.Context())

	return nil
}

// dialCapturingServer starts the capturing server over an in-process listener
// and returns a Client built through NewClient — the real constructor, so the
// test covers the dial options production uses.
func dialCapturingServer(t *testing.T) (*protocolCapturingServer, *Client) {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	service := &protocolCapturingServer{}
	server := grpc.NewServer()
	servicepb.RegisterBucketServiceServer(server, service)

	go func() { _ = server.Serve(listener) }()

	t.Cleanup(server.Stop)

	client, err := NewClient("passthrough:///bufnet", insecure.NewCredentials(),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return service, client
}

// The ledger rejects any business RPC that does not declare exactly one
// matching protocol revision (EN-1851), so an undeclared client fails every
// call with FailedPrecondition. Assert the header is actually on the wire —
// comparing the whole value slice, which also pins "exactly one": duplicates
// are rejected by the server as hard as a mismatch.
func TestClientDeclaresProtocolVersionOnUnaryRPC(t *testing.T) {
	service, client := dialCapturingServer(t)

	_, err := client.GetLedgerInfo(context.Background(), "default")

	require.NoError(t, err)
	require.Equal(t, []string{grpcprotocol.Version}, service.unary.Get(grpcprotocol.MetadataKey))
}

// The gate applies to stream establishment too, and per-RPC credentials are the
// only dial option gRPC re-evaluates on every call — a streaming regression
// would not show up in the unary test.
func TestClientDeclaresProtocolVersionOnStreamingRPC(t *testing.T) {
	service, client := dialCapturingServer(t)

	_, err := client.ListLedgers(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{grpcprotocol.Version}, service.stream.Get(grpcprotocol.MetadataKey))
}
