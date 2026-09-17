//go:build it

package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/grpcprotocol"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/require"
)

// TestIntegration_ProtocolVersionMatchesServer pins our vendored protocol
// revision to the one the live ledger requires.
//
// The vendored protos and grpcprotocol.Version are a single contract, but
// nothing in the build couples them: a re-sync that picks up renumbered field
// tags while leaving the constant behind compiles cleanly, passes every unit
// test, and then either fails every RPC at runtime or — worse, if the constant
// is bumped without the protos — satisfies the gate while sending fields the
// server reads as something else. This test is the coupling.
//
// Discovery is deliberately exempt from the revision gate (`public: true` in
// bucket.proto) precisely so a mismatched client can still ask what the server
// wants, which is why this can report a useful diff instead of just failing
// with FailedPrecondition like every other RPC would.
func TestIntegration_ProtocolVersionMatchesServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	resp, err := client.Service().Discovery(ctx, &servicepb.DiscoveryRequest{})
	require.NoError(t, err)

	require.Equal(t, grpcprotocol.Version, resp.GetServerInfo().GetProtocolVersion(),
		"vendored protocol revision differs from the live ledger's: re-run "+
			"`just sync-ledger-proto <ledger checkout>` and update "+
			"internal/ledgerpb/grpcprotocol.Version together")
}
