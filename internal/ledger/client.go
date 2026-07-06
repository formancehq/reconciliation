// Package ledger wraps the Ledger v3 gRPC BucketService client with the
// convenience methods reconciliation needs to run its control-ledger (`_recon`).
//
// Adapted from ledger-connect's internal/infra/ledger/client.go. The generated
// proto lives in internal/ledgerpb (see `just generate-ledger-proto`), synced
// from ledger-connect and aligned on ledger v3.0.0-alpha.3.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"slices"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// nextCursorTrailerKey is the gRPC trailer the ledger sets with the opaque
// resume token when a streamed list has more pages (matches the ledger's
// NextCursorTrailerKey).
const nextCursorTrailerKey = "x-next-cursor"

// queryPageSize is the per-page size for streamed account queries; QueryAccounts
// follows the cursor across pages, so this only trades round-trips for memory.
const queryPageSize = 200

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
	return c.applyIdempotent(ctx, "", requests...)
}

// applyIdempotent sends an atomic batch under an idempotency key. A repeated
// batch with the same key is deduplicated by the ledger (it returns the cached
// outcome instead of applying twice) — the at-least-once safety net for
// crash-replays. An empty key disables dedup.
func (c *Client) applyIdempotent(ctx context.Context, key string, requests ...*servicepb.Request) (*servicepb.ApplyResponse, error) {
	return c.service.Apply(ctx, &servicepb.ApplyRequest{
		Variant: &servicepb.ApplyRequest_Unsigned{
			Unsigned: &servicepb.ApplyBatch{Requests: requests, IdempotencyKey: key},
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

// CreateTransactionInput is the payload for CreateTransaction: a library
// Numscript transaction plus atomic account-metadata reconciliation (set some
// keys, delete others), all committed as one idempotent batch. Reconciliation
// uses it for the alert-lifecycle transitions, where a marker move (the guarded
// source-of-truth) and the descriptive/status metadata mirror must land
// together or not at all.
type CreateTransactionInput struct {
	Ledger string
	// The transaction runs a library numscript referenced by name+version (see
	// SaveNumscript). ScriptVersion "" resolves the latest pointer.
	ScriptName    string
	ScriptVersion string
	Vars          map[string]string // account addresses passed to the script
	// TxMetadata is transaction-level metadata (COMMITTED_TRANSACTION payload).
	TxMetadata map[string]*commonpb.MetadataValue
	// AccountMetadata sets typed metadata per account, atomically with the tx
	// (address → typed key/value map).
	AccountMetadata map[string]*commonpb.MetadataMap
	// DeleteMetadata removes metadata keys per account, atomically with the tx
	// (address → keys). The ledger rejects deleting an absent key, so callers
	// must only list keys they know are present.
	DeleteMetadata map[string][]string
	IdempotencyKey string
}

// CreateTransaction runs a library Numscript transaction, reconciling account
// metadata in the same atomic, idempotent batch. Balance guards in the script (a
// bare source that must hold the funds) act as compare-and-swap on the marker
// accounts: an illegal transition fails the whole batch.
func (c *Client) CreateTransaction(ctx context.Context, in CreateTransactionInput) error {
	reqs := make([]*servicepb.Request, 0, 1+len(in.DeleteMetadata))
	reqs = append(reqs, &servicepb.Request{
		Type: &servicepb.Request_Apply{
			Apply: &servicepb.LedgerApplyRequest{
				Ledger: in.Ledger,
				Action: &servicepb.LedgerAction{
					Data: &servicepb.LedgerAction_CreateTransaction{
						CreateTransaction: &servicepb.CreateTransactionPayload{
							ScriptReference: &servicepb.ScriptReference{
								Name:    in.ScriptName,
								Version: in.ScriptVersion,
								Vars:    in.Vars,
							},
							Metadata:        in.TxMetadata,
							AccountMetadata: in.AccountMetadata,
						},
					},
				},
			},
		},
	})

	// Sort addresses so the batch composition is deterministic (dedup keys off
	// the explicit idempotency key, but a stable batch keeps replays byte-equal).
	for _, addr := range slices.Sorted(maps.Keys(in.DeleteMetadata)) {
		for _, k := range in.DeleteMetadata[addr] {
			reqs = append(reqs, deleteMetadataRequest(in.Ledger, addr, k))
		}
	}

	_, err := c.applyIdempotent(ctx, in.IdempotencyKey, reqs...)

	return err
}

// SaveAccountMetadata saves string metadata on an account (no transaction).
func (c *Client) SaveAccountMetadata(ctx context.Context, ledgerName, address string, metadata map[string]string) error {
	return c.SaveAccountMetadataValues(ctx, ledgerName, address, commonpb.MetadataFromMap(metadata))
}

// SaveAccountMetadataValues saves typed metadata on an account (no transaction).
// Setting metadata on a fresh address creates the account.
func (c *Client) SaveAccountMetadataValues(ctx context.Context, ledgerName, address string, metadata map[string]*commonpb.MetadataValue) error {
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
							Metadata: metadata,
						},
					},
				},
			},
		},
	})

	return err
}

// DeleteAccountMetadata deletes the given metadata keys from an account in a
// single atomic batch (no-op if keys is empty).
func (c *Client) DeleteAccountMetadata(ctx context.Context, ledgerName, address string, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	reqs := make([]*servicepb.Request, 0, len(keys))
	for _, k := range keys {
		reqs = append(reqs, deleteMetadataRequest(ledgerName, address, k))
	}

	_, err := c.Apply(ctx, reqs...)

	return err
}

// deleteMetadataRequest builds a single DeleteMetadata action for one account
// key. Shared by DeleteAccountMetadata and CreateTransaction so the delete
// shape lives in one place.
func deleteMetadataRequest(ledgerName, address, key string) *servicepb.Request {
	return &servicepb.Request{
		Type: &servicepb.Request_Apply{
			Apply: &servicepb.LedgerApplyRequest{
				Ledger: ledgerName,
				Action: &servicepb.LedgerAction{
					Data: &servicepb.LedgerAction_DeleteMetadata{
						DeleteMetadata: &commonpb.DeleteMetadataCommand{
							Target: &commonpb.Target{
								Target: &commonpb.Target_Account{
									Account: &commonpb.TargetAccount{Addr: address},
								},
							},
							Key: key,
						},
					},
				},
			},
		},
	}
}

// QueryAccountsFunc streams every account matching filter and invokes fn for
// each, following the ledger's x-next-cursor across pages (one ListAccounts call
// returns a single page). A non-zero checkpointID reads from a query checkpoint.
// fn returning a non-nil error aborts the stream and surfaces that error verbatim
// — the seam a bounded reader uses to enforce an accounts budget without
// collecting the whole set into memory first. A metadata-filtered query returns
// codes.Unavailable while the field's index is still building — the client's
// retry policy absorbs that.
func (c *Client) QueryAccountsFunc(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter, checkpointID uint64, fn func(*commonpb.Account) error) error {
	var cursor string

	for {
		stream, err := c.service.ListAccounts(ctx, &servicepb.ListAccountsRequest{
			Ledger: ledgerName,
			Options: &commonpb.ListOptions{
				Read:     &commonpb.ReadOptions{CheckpointId: checkpointID},
				Filter:   filter,
				PageSize: queryPageSize,
				Cursor:   cursor,
			},
		})
		if err != nil {
			return fmt.Errorf("list accounts on %s: %w", ledgerName, err)
		}

		for {
			acct, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}

			if rerr != nil {
				return fmt.Errorf("recv account on %s: %w", ledgerName, rerr)
			}

			if ferr := fn(acct); ferr != nil {
				return ferr
			}
		}

		cursor = nextCursorFromTrailer(stream.Trailer())
		if cursor == "" {
			return nil
		}
	}
}

// QueryAccounts streams every account matching filter and collects them. The
// server-side filter keeps the collected set to the matches only, so this is
// bounded by the query's selectivity (id lookup → ≤1; a rule/period sweep → its
// active alerts). Callers that need a bounded scan should use QueryAccountsFunc
// and stop from the callback instead of collecting unboundedly here.
func (c *Client) QueryAccounts(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter, checkpointID uint64) ([]*commonpb.Account, error) {
	var accounts []*commonpb.Account

	if err := c.QueryAccountsFunc(ctx, ledgerName, filter, checkpointID, func(acct *commonpb.Account) error {
		accounts = append(accounts, acct)

		return nil
	}); err != nil {
		return nil, err
	}

	return accounts, nil
}

// nextCursorFromTrailer reads the opaque next-page token from a streamed list's
// trailer (empty when the last page has been drained).
func nextCursorFromTrailer(trailer metadata.MD) string {
	if vals := trailer.Get(nextCursorTrailerKey); len(vals) > 0 {
		return vals[0]
	}

	return ""
}

// AggregateVolumes returns the per-asset aggregate balance (input − output) of
// the accounts matching filter. A non-zero checkpointID reads from a query
// checkpoint instead of live state — the checkpoint-consistent read that lets an
// evaluation aggregate ledgers A and B at the same instant (ADR-002).
func (c *Client) AggregateVolumes(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter, checkpointID uint64) (map[string]*big.Int, error) {
	resp, err := c.service.AggregateVolumes(ctx, &servicepb.AggregateVolumesRequest{
		Ledger:       ledgerName,
		Filter:       filter,
		CheckpointId: checkpointID,
	})
	if err != nil {
		return nil, fmt.Errorf("aggregate volumes on %s: %w", ledgerName, err)
	}

	out := make(map[string]*big.Int, len(resp.GetVolumes()))
	for _, v := range resp.GetVolumes() {
		out[v.GetAsset()] = new(big.Int).Sub(v.GetInput().ToBigInt(), v.GetOutput().ToBigInt())
	}

	return out, nil
}

// CreateQueryCheckpoint creates a query checkpoint via Raft and returns its
// assigned id and the max log sequence it pins. Because all ledgers share one
// log, the checkpoint is a globally consistent cross-ledger cut — the anchor for
// reading A and B at the same instant (ADR-002). Checkpoints are NOT auto-cleaned:
// the caller owns the lifecycle (see Checkpoint).
func (c *Client) CreateQueryCheckpoint(ctx context.Context) (checkpointID, maxSequence uint64, err error) {
	resp, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_CreateQueryCheckpoint{CreateQueryCheckpoint: &servicepb.CreateQueryCheckpointRequest{}},
	})
	if err != nil {
		return 0, 0, fmt.Errorf("create query checkpoint: %w", err)
	}

	for _, log := range resp.GetLogs() {
		if cp := log.GetPayload().GetCreatedQueryCheckpoint(); cp != nil {
			return cp.GetCheckpointId(), cp.GetMaxSequence(), nil
		}
	}

	return 0, 0, fmt.Errorf("create query checkpoint: response carried no CreatedQueryCheckpointLog")
}

// DeleteQueryCheckpoint removes a query checkpoint via Raft, releasing the SSTs
// it pinned. Idempotent-friendly: a NotFound is swallowed so a double-release is
// safe.
func (c *Client) DeleteQueryCheckpoint(ctx context.Context, checkpointID uint64) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_DeleteQueryCheckpoint{
			DeleteQueryCheckpoint: &servicepb.DeleteQueryCheckpointRequest{CheckpointId: checkpointID},
		},
	})
	if status.Code(err) == codes.NotFound {
		return nil
	}

	if err != nil {
		return fmt.Errorf("delete query checkpoint %d: %w", checkpointID, err)
	}

	return nil
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
