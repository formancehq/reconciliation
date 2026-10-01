package ledger

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// unavailableServer answers every GetAccount with UNAVAILABLE, the code a
// ledger node returns while it has no leader, and counts the attempts that
// reach it.
type unavailableServer struct {
	servicepb.UnimplementedBucketServiceServer
	attempts atomic.Int32
}

func (s *unavailableServer) GetAccount(context.Context, *servicepb.GetAccountRequest) (*commonpb.Account, error) {
	s.attempts.Add(1)

	return nil, status.Error(codes.Unavailable, "no leader")
}

// grpc-go silently caps a service-config maxAttempts at 5 unless the dial sets
// grpc.WithMaxCallAttempts. Count what reaches the server through NewClient,
// so the declared policy and the effective one cannot drift apart again.
func TestRetryPolicyAttemptsPerCall(t *testing.T) {
	t.Parallel()

	var config struct {
		MethodConfig []struct {
			RetryPolicy struct {
				MaxAttempts int32 `json:"maxAttempts"`
			} `json:"retryPolicy"`
		} `json:"methodConfig"`
	}
	require.NoError(t, json.Unmarshal([]byte(GRPCRetryPolicy), &config))
	require.Len(t, config.MethodConfig, 1)

	declared := config.MethodConfig[0].RetryPolicy.MaxAttempts

	service := &unavailableServer{}
	client := dialBufconn(t, service)

	start := time.Now()
	_, err := client.GetAccount(t.Context(), "default", "world")
	elapsed := time.Since(start)

	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, declared, service.attempts.Load(), "declared maxAttempts vs attempts the server saw")
	require.Equal(t, int32(5), declared, "the budget GRPCRetryPolicy documents")
	// The four waits are 0.25 + 0.5 + 1 + 2 s, each at least 0.8x after
	// jitter: the call must outlast a 1 to 2 s leader election.
	require.GreaterOrEqual(t, elapsed, 3*time.Second)
}
