package ledger

import (
	"context"
	"fmt"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// EventSinkConfig describes an HTTP webhook events sink to register on the
// cluster (RFC §4.4). EventTypes filters which committed log events are
// delivered; empty means all. The ledger's HTTP sink ships in the light binary,
// so this is reconciliation's whole delivery path — no self-owned message bus.
type EventSinkConfig struct {
	Name       string
	Endpoint   string
	Secret     string // optional HMAC-SHA256 secret for the X-Webhook-Signature header
	EventTypes []commonpb.EventType
}

// AddEventsSink registers an HTTP webhook events sink via Raft. The ledger then
// delivers every matching committed log entry to the endpoint. Ignores
// AlreadyExists so provisioning at every boot is a no-op — matching CreateLedger
// et al. NB the server is add-only (not add-or-update): changing an existing
// sink's endpoint/filter requires an explicit RemoveEventsSink first, which
// resets the per-sink cursor (so the new sink re-delivers from the log head) —
// deliberately an operator action, not silent boot behaviour.
func (c *Client) AddEventsSink(ctx context.Context, cfg EventSinkConfig) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_AddEventsSink{
			AddEventsSink: &servicepb.AddEventsSinkRequest{
				Config: &commonpb.SinkConfig{
					Name:       cfg.Name,
					Type:       &commonpb.SinkConfig_Http{Http: &commonpb.HttpSinkConfig{Endpoint: cfg.Endpoint, Secret: cfg.Secret}},
					EventTypes: cfg.EventTypes,
				},
			},
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}

	if err != nil {
		return fmt.Errorf("add events sink %q: %w", cfg.Name, err)
	}

	return nil
}

// RemoveEventsSink deletes a named events sink via Raft. NotFound-tolerant so a
// double-remove is a no-op.
func (c *Client) RemoveEventsSink(ctx context.Context, name string) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_RemoveEventsSink{
			RemoveEventsSink: &servicepb.RemoveEventsSinkRequest{Name: name},
		},
	})
	if status.Code(err) == codes.NotFound {
		return nil
	}

	if err != nil {
		return fmt.Errorf("remove events sink %q: %w", name, err)
	}

	return nil
}

// GetEventsSinks returns the currently-configured sink configurations.
func (c *Client) GetEventsSinks(ctx context.Context) ([]*commonpb.SinkConfig, error) {
	resp, err := c.service.GetEventsSinks(ctx, &servicepb.GetEventsSinksRequest{})
	if err != nil {
		return nil, fmt.Errorf("get events sinks: %w", err)
	}

	return resp.GetSinks(), nil
}
