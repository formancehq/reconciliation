//go:build it

package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestIntegration_EventsSink proves the ED-2 delivery primitives against a live
// ledger: register an HTTP webhook sink, read it back, update it in place
// (add-or-update), then remove it (idempotently). A unique name + benign
// unreachable endpoint + immediate removal keep the shared dev cluster clean.
func TestIntegration_EventsSink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	name := "recon-it-sink-" + uuid.NewString()
	// Always clean up, even if an assertion fails mid-test.
	defer func() { _ = client.RemoveEventsSink(ctx, name) }()

	eventTypes := []commonpb.EventType{commonpb.EventType_SAVED_METADATA}
	require.NoError(t, client.AddEventsSink(ctx, EventSinkConfig{
		Name:       name,
		Endpoint:   "http://127.0.0.1:9/recon-it",
		EventTypes: eventTypes,
	}))

	got := findSink(t, client, ctx, name)
	require.NotNil(t, got, "sink registered")
	require.Equal(t, "http://127.0.0.1:9/recon-it", got.GetHttp().GetEndpoint())
	require.Equal(t, eventTypes, got.GetEventTypes())

	// Idempotent boot re-provision: re-adding the same name is a no-op (the
	// server is add-only; AddEventsSink swallows AlreadyExists), leaving the
	// original config in place.
	require.NoError(t, client.AddEventsSink(ctx, EventSinkConfig{
		Name:       name,
		Endpoint:   "http://127.0.0.1:9/ignored",
		EventTypes: eventTypes,
	}))
	require.Equal(t, "http://127.0.0.1:9/recon-it", findSink(t, client, ctx, name).GetHttp().GetEndpoint(), "re-add is a no-op")

	// Remove → gone; a second remove is a no-op.
	require.NoError(t, client.RemoveEventsSink(ctx, name))
	require.Nil(t, findSink(t, client, ctx, name), "removed")
	require.NoError(t, client.RemoveEventsSink(ctx, name), "double-remove is a no-op")
}

func findSink(t *testing.T, c *Client, ctx context.Context, name string) *commonpb.SinkConfig {
	t.Helper()

	sinks, err := c.GetEventsSinks(ctx)
	require.NoError(t, err)

	for _, s := range sinks {
		if s.GetName() == name {
			return s
		}
	}

	return nil
}
