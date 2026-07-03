// Package ledger wraps the Ledger v3 gRPC BucketService client with the
// convenience methods reconciliation needs to run its control-ledger (`_recon`).
//
// Adapted from ledger-connect's internal/infra/ledger/client.go. The generated
// proto lives in internal/ledgerpb (see `just generate-ledger-proto`), synced
// from ledger-connect and aligned on ledger v3.0.0-alpha.3.
package ledger

import (
	"context"
	"fmt"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// GRPCRetryPolicy retries on UNAVAILABLE (leader failover, cluster unhealthy).
const GRPCRetryPolicy = `{
	"methodConfig": [{
		"name": [{"service": "ledger.BucketService"}],
		"retryPolicy": {
			"maxAttempts": 50,
			"initialBackoff": "0.2s",
			"maxBackoff": "2s",
			"backoffMultiplier": 1.5,
			"retryableStatusCodes": ["UNAVAILABLE"]
		}
	}]
}`

// Client wraps the ledger v3 gRPC client with convenience methods.
type Client struct {
	conn    *grpc.ClientConn
	service servicepb.BucketServiceClient
}

// NewClient creates a new ledger gRPC client. If creds is nil, insecure
// credentials are used. Extra dial options can carry Ed25519 request-signing
// per-RPC credentials, mTLS, etc.
func NewClient(address string, creds credentials.TransportCredentials, dialOpts ...grpc.DialOption) (*Client, error) {
	if creds == nil {
		creds = insecure.NewCredentials()
	}

	baseOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultServiceConfig(GRPCRetryPolicy),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(64*1024*1024),
			grpc.MaxCallRecvMsgSize(64*1024*1024),
		),
	}

	conn, err := grpc.NewClient(address, append(baseOpts, dialOpts...)...)
	if err != nil {
		return nil, fmt.Errorf("connect to ledger at %s: %w", address, err)
	}

	return &Client{conn: conn, service: servicepb.NewBucketServiceClient(conn)}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Service returns the raw BucketServiceClient for calls not covered by a helper.
func (c *Client) Service() servicepb.BucketServiceClient {
	return c.service
}

// Apply sends a batch of requests to the ledger in a single atomic batch.
func (c *Client) Apply(ctx context.Context, requests ...*servicepb.Request) (*servicepb.ApplyResponse, error) {
	return c.service.Apply(ctx, &servicepb.ApplyRequest{
		Variant: &servicepb.ApplyRequest_Unsigned{
			Unsigned: &servicepb.ApplyBatch{Requests: requests},
		},
	})
}

// CreateLedger creates a ledger with an initial metadata schema, account types,
// and default chart-enforcement mode. Ignores AlreadyExists so bootstrap is
// idempotent. Note: the zero value of ChartEnforcementMode is STRICT — pass
// AUDIT explicitly for a gradual rollout.
func (c *Client) CreateLedger(ctx context.Context, name string, schema []*commonpb.SetMetadataFieldTypeCommand, accountTypes map[string]*commonpb.AccountType, enforcement commonpb.ChartEnforcementMode) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_CreateLedger{
			CreateLedger: &servicepb.CreateLedgerRequest{
				Name:                   name,
				InitialSchema:          schema,
				AccountTypes:           accountTypes,
				DefaultEnforcementMode: enforcement,
			},
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}

	return err
}

// CreateIndex creates an index on a ledger. Ignores AlreadyExists.
func (c *Client) CreateIndex(ctx context.Context, ledger string, index *servicepb.CreateIndexRequest) error {
	index.Ledger = ledger

	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_CreateIndex{CreateIndex: index},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}

	return err
}

// CreatePreparedQuery registers a named prepared query on a ledger. Ignores AlreadyExists.
func (c *Client) CreatePreparedQuery(ctx context.Context, ledger string, query *commonpb.PreparedQuery) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_CreatePreparedQuery{
			CreatePreparedQuery: &servicepb.CreatePreparedQueryRequest{Ledger: ledger, Query: query},
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}

	return err
}

// SaveNumscript registers a numscript in the ledger's library so transactions
// can reference it by name+version instead of sending the source inline. Ignores
// AlreadyExists. Reconciliation uses this for the alert-lifecycle transitions.
func (c *Client) SaveNumscript(ctx context.Context, ledger, name, content, version string) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_SaveNumscript{
			SaveNumscript: &servicepb.SaveNumscriptRequest{
				Ledger:  ledger,
				Name:    name,
				Content: content,
				Version: version,
			},
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}

	return err
}

// SaveAccountMetadata saves string metadata on an account (no transaction).
func (c *Client) SaveAccountMetadata(ctx context.Context, ledgerName, address string, metadata map[string]string) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_Apply{
			Apply: &servicepb.LedgerApplyRequest{
				Ledger: ledgerName,
				Action: &servicepb.LedgerAction{
					Data: &servicepb.LedgerAction_AddMetadata{
						AddMetadata: &commonpb.SaveMetadataCommand{
							Target: &commonpb.Target{
								Target: &commonpb.Target_Account{
									Account: &commonpb.TargetAccount{Addr: address},
								},
							},
							Metadata: commonpb.MetadataFromMap(metadata),
						},
					},
				},
			},
		},
	})

	return err
}

// GetAccount retrieves an account (volumes + metadata) by address. A non-zero
// checkpointID reads from a query checkpoint instead of live state.
func (c *Client) GetAccount(ctx context.Context, ledgerName, address string, checkpointID uint64) (*commonpb.Account, error) {
	acct, err := c.service.GetAccount(ctx, &servicepb.GetAccountRequest{
		Ledger:       ledgerName,
		Address:      address,
		CheckpointId: checkpointID,
	})
	if err != nil {
		return nil, fmt.Errorf("get account %s@%s: %w", address, ledgerName, err)
	}

	return acct, nil
}
