// Package ledger wraps the Ledger v3 gRPC BucketService client with the
// convenience methods reconciliation needs to run its control-ledger (`_recon`).
//
// Adapted from ledger-connect's internal/infra/ledger/client.go. The generated
// proto lives in internal/ledgerpb (see `just generate-ledger-proto`) and is
// synced directly from Ledger's release/v3.0 branch.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"slices"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/auditpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/raftcmdpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/signaturepb"
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

	// signer, when set, makes every write commit as a SignedApplyBatch so the
	// ledger stores an Ed25519 signature over the batch bytes on each log entry
	// it produces — the native, externally-verifiable audit chain (EN-1930). Set
	// once at construction via UseSigningKey, before the client is shared, then
	// only read; nil preserves the original unsigned behaviour.
	signer *SigningKey
}

// UseSigningKey activates content-signing for every subsequent write. It must be
// called at construction — before the client is shared across request goroutines
// — and paired with RegisterSigningKey so the ledger already knows the key when
// the first signed batch arrives. Passing nil leaves the client unsigned.
func (c *Client) UseSigningKey(key *SigningKey) { c.signer = key }

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
	batch := &servicepb.ApplyBatch{Requests: requests, IdempotencyKey: key}

	// When a signing key is configured, commit as a SignedApplyBatch: serialize
	// the batch once, Ed25519-sign those exact bytes, and ship them as the opaque
	// signed payload. The ledger verifies the signature against the registered
	// public key, unmarshals the same bytes (it never re-serializes), and records
	// the signature on every log entry the batch produces — so a third party can
	// later verify the entry from the public key alone (EN-1930, Phase 1).
	if c.signer != nil && c.signer.CanSign() {
		payload, err := batch.MarshalVT()
		if err != nil {
			return nil, fmt.Errorf("marshal apply batch for signing: %w", err)
		}

		signature, err := c.signer.Sign(payload)
		if err != nil {
			return nil, fmt.Errorf("sign apply batch: %w", err)
		}

		return c.service.Apply(ctx, &servicepb.ApplyRequest{
			Variant: &servicepb.ApplyRequest_Signed{
				Signed: &signaturepb.SignedApplyBatch{
					KeyId:     c.signer.ID,
					Signature: signature,
					Payload:   payload,
				},
			},
		})
	}

	return c.service.Apply(ctx, &servicepb.ApplyRequest{
		Variant: &servicepb.ApplyRequest_Unsigned{Unsigned: batch},
	})
}

// RegisterSigningKey registers the public half of an Ed25519 key with the ledger
// so it will accept — and store — batches signed by the matching private key.
//
// Sent UNSIGNED by construction, bypassing c.signer: the key being registered is
// not yet known to the ledger, so it cannot sign its own registration. Idempotent
// — a key already registered is swallowed as AlreadyExists, so re-registering the
// same key on every boot is a no-op.
func (c *Client) RegisterSigningKey(ctx context.Context, key *SigningKey) error {
	_, err := c.service.Apply(ctx, &servicepb.ApplyRequest{
		Variant: &servicepb.ApplyRequest_Unsigned{
			Unsigned: &servicepb.ApplyBatch{
				Requests: []*servicepb.Request{{
					Type: &servicepb.Request_RegisterSigningKey{
						RegisterSigningKey: &servicepb.RegisterSigningKeyRequest{
							KeyId:     key.ID,
							PublicKey: key.Public,
						},
					},
				}},
			},
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}

	return err
}

// RegisterConfiguredSigningKey registers this client's own signing key (the one
// set via UseSigningKey) with the ledger, if one is configured. Call it once at
// startup, before the first signed write; a no-op when the client is unsigned.
//
// Idempotent across restarts, and it has to be by checking first: the ledger
// accepts an UNSIGNED RegisterSigningKey only while its keystore is still empty
// (the bootstrap window). Once our key is known, blindly re-registering it
// unsigned would be rejected as a missing signature — not swallowed as
// AlreadyExists — so we register only when the key is genuinely absent.
func (c *Client) RegisterConfiguredSigningKey(ctx context.Context) error {
	if c.signer == nil {
		return nil
	}

	registered, err := c.signingKeyRegistered(ctx, c.signer.ID)
	if err != nil {
		return err
	}
	if registered {
		return nil
	}

	return c.RegisterSigningKey(ctx, c.signer)
}

// SigningKeyInfo is a registered signing key as served to callers: the key id,
// the raw Ed25519 public key, and the parent key id (set on a rotated key that
// descends from an earlier one). The public key is what an external auditor uses
// to verify a `_recon` entry.
type SigningKeyInfo struct {
	KeyID       string
	PublicKey   []byte
	ParentKeyID string
}

// ListSigningKeys returns every signing key the ledger has registered. Few in
// practice (one, plus any rotation ancestors), but paginated for correctness.
func (c *Client) ListSigningKeys(ctx context.Context) ([]SigningKeyInfo, error) {
	var (
		keys   []SigningKeyInfo
		cursor string
	)

	for {
		stream, err := c.service.ListSigningKeys(ctx, &servicepb.ListSigningKeysRequest{
			Options: &commonpb.ListOptions{PageSize: queryPageSize, Cursor: cursor},
		})
		if err != nil {
			return nil, fmt.Errorf("list signing keys: %w", err)
		}

		for {
			key, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}

			if rerr != nil {
				return nil, fmt.Errorf("recv signing key: %w", rerr)
			}

			keys = append(keys, SigningKeyInfo{
				KeyID:       key.GetKeyId(),
				PublicKey:   key.GetPublicKey(),
				ParentKeyID: key.GetParentKeyId(),
			})
		}

		cursor = nextCursorFromTrailer(stream.Trailer())
		if cursor == "" {
			return keys, nil
		}
	}
}

// AuditEntryInfo is one entry of the ledger's native audit trail, as served to
// an external auditor: the externally-verifiable {Payload, Signature} — over
// which ed25519.Verify(publicKey, Payload, Signature) is the whole check — plus
// the dense Sequence and the outcome. Payload is the exact serialized ApplyBatch
// the signature commits to. All of this is the ledger's own AuditEntry, read
// back verbatim; reconciliation stores no parallel audit log of its own.
type AuditEntryInfo struct {
	Sequence   uint64
	Timestamp  time.Time
	KeyID      string
	Signature  []byte
	Payload    []byte
	Signed     bool
	Outcome    string
	OrderCount uint32
	Ledgers    []string
	// Failure detail (outcome == "failure"): the ledger's own reason enum name
	// and human message — e.g. why a guarded write was rejected.
	FailureReason  string
	FailureMessage string
	// Actions is the decoded per-order business intent of the proposal. Populated
	// only on the single-entry read (GetAuditEntry) — the stream omits the items
	// these are decoded from. Usually one entry (a batch is one Apply order).
	Actions []AuditAction
}

// AuditAction is the human-readable intent of one order in an audit proposal,
// decoded from its business-intent bytes. Deliberately shallow: it names the
// kind of write (Apply batch, register numscript, register signing key, …) and,
// where the order carries them cheaply, a couple of identifying fields — it does
// not crack open an Apply batch's numscript/postings.
type AuditAction struct {
	// Kind is a short human label, e.g. "Apply batch" or "Register numscript".
	Kind string
	// Ledger is the target ledger for a ledger-scoped order; empty for
	// system-scoped orders (signing keys, sinks, cluster policy, …).
	Ledger string
	// Detail is an optional identifier the order carries cheaply, e.g. a
	// numscript "name v2.0.0" or a prepared-query name. Empty when there is none.
	Detail string
}

// ListAuditEntries returns the ledger's audit entries for one ledger (recon's
// control ledger), newest first, up to limit. Each entry carries the batch
// Ed25519 signature the ledger stored, so a third party can verify it from the
// public key with no ledger access. The audit sequence is bucket-wide, so a
// filtered subset is authentic per-entry but not necessarily gapless — see the
// P1.3 completeness note.
func (c *Client) ListAuditEntries(ctx context.Context, ledgerName string, limit int) ([]AuditEntryInfo, error) {
	if limit <= 0 {
		limit = 50
	}

	filter := &commonpb.QueryFilter{
		Filter: &commonpb.QueryFilter_Audit{
			Audit: &commonpb.AuditCondition{
				Field: commonpb.AuditField_AUDIT_FIELD_LEDGER,
				Condition: &commonpb.AuditCondition_StringCond{
					StringCond: &commonpb.StringCondition{
						Value: &commonpb.StringCondition_Hardcoded{Hardcoded: ledgerName},
					},
				},
			},
		},
	}

	var (
		entries []AuditEntryInfo
		cursor  string
	)

	for len(entries) < limit {
		pageSize := limit - len(entries)
		if pageSize > queryPageSize {
			pageSize = queryPageSize
		}

		stream, err := c.service.ListAuditEntries(ctx, &servicepb.ListAuditEntriesRequest{
			Options: &commonpb.ListOptions{
				PageSize: uint32(pageSize),
				Cursor:   cursor,
				Reverse:  true, // newest first
				Filter:   filter,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("list audit entries: %w", err)
		}

		for len(entries) < limit {
			entry, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}

			if rerr != nil {
				return nil, fmt.Errorf("recv audit entry: %w", rerr)
			}

			entries = append(entries, auditEntryInfoFrom(entry))
		}

		cursor = nextCursorFromTrailer(stream.Trailer())
		if cursor == "" {
			break
		}
	}

	return entries, nil
}

// GetAuditEntry returns one audit entry by sequence, with the detail the stream
// omits — notably the failure reason and message on a rejected write (the ledger
// populates those, like the per-order items, only on the single-entry read).
func (c *Client) GetAuditEntry(ctx context.Context, sequence uint64) (AuditEntryInfo, error) {
	entry, err := c.service.GetAuditEntry(ctx, &servicepb.GetAuditEntryRequest{Sequence: sequence})
	if err != nil {
		return AuditEntryInfo{}, fmt.Errorf("get audit entry %d: %w", sequence, err)
	}

	return auditEntryInfoFrom(entry), nil
}

// auditEntryInfoFrom maps a ledger AuditEntry to the caller-facing shape. Shared
// by the stream (ListAuditEntries) and the single-entry read (GetAuditEntry);
// the failure reason/message are empty on the streamed form and populated on the
// single read.
func auditEntryInfoFrom(entry *auditpb.AuditEntry) AuditEntryInfo {
	info := AuditEntryInfo{
		Sequence:   entry.GetSequence(),
		OrderCount: entry.GetOrderCount(),
		Ledgers:    entry.GetLedgers(),
		Outcome:    "success",
	}
	if failure := entry.GetFailure(); failure != nil {
		info.Outcome = "failure"
		info.FailureReason = failure.GetReason().String()
		info.FailureMessage = failure.GetMessage()
	}
	if ts := entry.GetTimestamp(); ts != nil {
		info.Timestamp = time.UnixMicro(int64(ts.GetData())).UTC()
	}
	if sig := entry.GetSignature(); sig != nil {
		info.KeyID = sig.GetKeyId()
		info.Signature = sig.GetSignature()
		info.Payload = sig.GetPayload()
		info.Signed = len(sig.GetSignature()) > 0
	}
	// Items are populated only on the single-entry read; decode each order's
	// business intent for the audit-tab detail. Best-effort — an order we can't
	// decode is skipped rather than failing the whole entry.
	for _, item := range entry.GetItems() {
		if action, ok := decodeAuditAction(item.GetSerializedOrder()); ok {
			info.Actions = append(info.Actions, action)
		}
	}

	return info
}

// decodeAuditAction turns one order's business-intent bytes into a shallow,
// human-readable AuditAction. It names the kind of write (and, where cheap, an
// identifier) but deliberately does NOT decode an Apply batch's numscript or
// postings — that intent lives in the batch and cracking it open would couple the
// audit view to the transaction model. Returns ok=false when the bytes don't
// decode, so the caller skips the order rather than surfacing a bogus label.
func decodeAuditAction(serialized []byte) (AuditAction, bool) {
	if len(serialized) == 0 {
		return AuditAction{}, false
	}
	order := &raftcmdpb.Order{}
	if err := order.UnmarshalVT(serialized); err != nil {
		return AuditAction{}, false
	}

	switch scoped := order.GetType().(type) {
	case *raftcmdpb.Order_LedgerScoped:
		ls := scoped.LedgerScoped
		action := AuditAction{Ledger: ls.GetLedger()}
		switch payload := ls.GetPayload().(type) {
		case *raftcmdpb.LedgerScopedOrder_Apply:
			action.Kind = "Apply batch"
		case *raftcmdpb.LedgerScopedOrder_SaveNumscript:
			action.Kind = "Register numscript"
			if n := payload.SaveNumscript.GetName(); n != "" {
				action.Detail = n
				if v := payload.SaveNumscript.GetVersion(); v != "" {
					action.Detail = n + " v" + v
				}
			}
		case *raftcmdpb.LedgerScopedOrder_CreatePreparedQuery:
			action.Kind = "Create prepared query"
		case *raftcmdpb.LedgerScopedOrder_UpdatePreparedQuery:
			action.Kind = "Update prepared query"
		default:
			action.Kind = "Ledger write"
		}
		return action, true
	case *raftcmdpb.Order_SystemScoped:
		switch scoped.SystemScoped.GetPayload().(type) {
		case *raftcmdpb.SystemScopedOrder_RegisterSigningKey:
			return AuditAction{Kind: "Register signing key"}, true
		default:
			return AuditAction{Kind: "System write"}, true
		}
	default:
		return AuditAction{}, false
	}
}

// signingKeyRegistered reports whether a key with the given id is already known
// to the ledger's keystore. See RegisterConfiguredSigningKey for why the check
// is load-bearing rather than a mere optimisation.
func (c *Client) signingKeyRegistered(ctx context.Context, keyID string) (bool, error) {
	keys, err := c.ListSigningKeys(ctx)
	if err != nil {
		return false, err
	}

	for _, k := range keys {
		if k.KeyID == keyID {
			return true, nil
		}
	}

	return false, nil
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

// AddAccountType adds a single account type to an existing ledger's chart.
// Ignores AlreadyExists so a re-provision is idempotent — this is the reconcile
// path that brings a previously-created control ledger up to the current chart
// (F8), so an *additive* chart change (a new account type) no longer requires
// recreating the ledger. A conflicting redefinition (same name, different
// pattern/persistence — or a type that already holds accounts) still surfaces
// its error: changing an existing type is not an additive evolution.
func (c *Client) AddAccountType(ctx context.Context, ledger string, accountType *commonpb.AccountType) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_AddAccountType{
			AddAccountType: &servicepb.AddAccountTypeLedgerRequest{Ledger: ledger, AccountType: accountType},
		},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}

	return err
}

// SetMetadataFieldType declares (or re-affirms) the type of a single metadata
// key on an existing ledger. Naturally idempotent: re-declaring the same type is
// a no-op, declaring a new key adds it, and a changed type updates the
// declaration (triggering a forward-index rewrite). The reconcile path for
// metadata-schema evolution (F8).
func (c *Client) SetMetadataFieldType(ctx context.Context, ledger string, cmd *commonpb.SetMetadataFieldTypeCommand) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_SetMetadataFieldType{
			SetMetadataFieldType: &servicepb.SetMetadataFieldTypeRequest{
				Ledger:     ledger,
				TargetType: cmd.GetTargetType(),
				Key:        cmd.GetKey(),
				Type:       cmd.GetType(),
			},
		},
	})

	return err
}

// GetLedgerInfo returns a ledger's current config (account types, typed metadata
// schema, ...), or (nil, nil) when it does not exist. Provision uses it to
// reconcile only the delta: a redundant SetMetadataFieldType on an indexed field
// re-triggers a forward-index rewrite (the FSM bumps forward_encoding_version
// unconditionally), so skipping already-correct fields keeps boot cheap.
func (c *Client) GetLedgerInfo(ctx context.Context, name string) (*commonpb.LedgerInfo, error) {
	info, err := c.service.GetLedger(ctx, &servicepb.GetLedgerRequest{Ledger: name})
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return info, nil
}

// ListLedgers streams every ledger in the cluster and collects the names of the
// live ones — soft-deleted ledgers (DeletedAt set) are skipped — paging
// internally via the stream's x-next-cursor trailer (ListLedgers is
// server-streaming, like ListAccounts). Backs the standalone UI's rule-builder
// ledger picker, sourced through this module's ledger gRPC connection (UI
// federation) rather than a browser→ledger connection.
func (c *Client) ListLedgers(ctx context.Context) ([]string, error) {
	var (
		names  []string
		cursor string
	)

	for {
		stream, err := c.service.ListLedgers(ctx, &servicepb.ListLedgersRequest{
			Options: &commonpb.ListOptions{
				PageSize: queryPageSize,
				Cursor:   cursor,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("list ledgers: %w", err)
		}

		for {
			info, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}

			if rerr != nil {
				return nil, fmt.Errorf("recv ledger: %w", rerr)
			}

			// Soft-deleted ledgers keep their row (an unaudited ledger still
			// streams here) — skip them so the picker only offers live ledgers.
			if info.GetDeletedAt() != nil {
				continue
			}

			if name := info.GetName(); name != "" {
				names = append(names, name)
			}
		}

		cursor = nextCursorFromTrailer(stream.Trailer())
		if cursor == "" {
			return names, nil
		}
	}
}

// DeleteLedger removes a ledger. Ignores NotFound so cleanup is idempotent.
// Primarily a test-hygiene helper — the reconciliation control ledger is never
// deleted at runtime.
func (c *Client) DeleteLedger(ctx context.Context, name string) error {
	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_DeleteLedger{
			DeleteLedger: &servicepb.DeleteLedgerRequest{Name: name},
		},
	})
	if status.Code(err) == codes.NotFound {
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
	_, err := c.Apply(ctx, addMetadataRequest(ledgerName, address, metadata))

	return err
}

// ApplyMetadata sets and/or deletes account metadata keys in one atomic batch —
// the metadata-only counterpart to CreateTransaction's set+delete reconciliation
// (used e.g. to record a transition and clear a snooze together). Deleting an
// absent key fails the batch, so callers list only keys known to be present.
func (c *Client) ApplyMetadata(ctx context.Context, ledgerName, address string, set map[string]*commonpb.MetadataValue, deleteKeys ...string) error {
	reqs := make([]*servicepb.Request, 0, 1+len(deleteKeys))
	if len(set) > 0 {
		reqs = append(reqs, addMetadataRequest(ledgerName, address, set))
	}

	for _, k := range deleteKeys {
		reqs = append(reqs, deleteMetadataRequest(ledgerName, address, k))
	}

	if len(reqs) == 0 {
		return nil
	}

	_, err := c.Apply(ctx, reqs...)

	return err
}

// addMetadataRequest builds a single AddMetadata action for an account. Shared by
// SaveAccountMetadataValues and ApplyMetadata so the add shape lives in one place.
func addMetadataRequest(ledgerName, address string, metadata map[string]*commonpb.MetadataValue) *servicepb.Request {
	return &servicepb.Request{
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
	}
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
// returns a single page). Reads live state.
// fn returning a non-nil error aborts the stream and surfaces that error verbatim
// — the seam a bounded reader uses to enforce an accounts budget without
// collecting the whole set into memory first. A metadata-filtered query returns
// codes.Unavailable while the field's index is still building — the client's
// retry policy absorbs that.
func (c *Client) QueryAccountsFunc(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter, fn func(*commonpb.Account) error) error {
	var cursor string

	for {
		stream, err := c.service.ListAccounts(ctx, &servicepb.ListAccountsRequest{
			Ledger: ledgerName,
			Options: &commonpb.ListOptions{
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

// ListTransactionsFunc streams every transaction matching filter (address order)
// and invokes fn for each, paging internally via the stream's next-cursor
// trailer — the transaction-side mirror of QueryAccountsFunc. Used to read a
// rule's capture history from the `_recon` control ledger: captures are
// first-class transactions, so they are queryable live (no event sink needed).
func (c *Client) ListTransactionsFunc(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter, fn func(*commonpb.Transaction) error) error {
	var cursor string

	for {
		stream, err := c.service.ListTransactions(ctx, &servicepb.ListTransactionsRequest{
			Ledger: ledgerName,
			Options: &commonpb.ListOptions{
				Filter:   filter,
				PageSize: queryPageSize,
				Cursor:   cursor,
			},
		})
		if err != nil {
			return fmt.Errorf("list transactions on %s: %w", ledgerName, err)
		}

		for {
			tx, rerr := stream.Recv()
			if errors.Is(rerr, io.EOF) {
				break
			}

			if rerr != nil {
				return fmt.Errorf("recv transaction on %s: %w", ledgerName, rerr)
			}

			if ferr := fn(tx); ferr != nil {
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
func (c *Client) QueryAccounts(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter) ([]*commonpb.Account, error) {
	var accounts []*commonpb.Account

	if err := c.QueryAccountsFunc(ctx, ledgerName, filter, func(acct *commonpb.Account) error {
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
// the accounts matching filter, read from live state (the server computes it
// against one consistent Pebble snapshot, so a single call is internally
// consistent). Reconciliation reads its account universes live and records the
// observed state in an immutable _recon capture (ADR-003).
func (c *Client) AggregateVolumes(ctx context.Context, ledgerName string, filter *commonpb.QueryFilter) (map[string]*big.Int, error) {
	resp, err := c.service.AggregateVolumes(ctx, &servicepb.AggregateVolumesRequest{
		Ledger:         ledgerName,
		Filter:         filter,
		CollapseColors: true,
	})
	if err != nil {
		return nil, fmt.Errorf("aggregate volumes on %s: %w", ledgerName, err)
	}

	out := make(map[string]*big.Int, len(resp.GetVolumes()))
	for _, v := range resp.GetVolumes() {
		if v == nil {
			continue
		}

		balance := new(big.Int).Sub(v.GetInput().ToBigInt(), v.GetOutput().ToBigInt())
		if out[v.GetAsset()] == nil {
			out[v.GetAsset()] = new(big.Int)
		}
		out[v.GetAsset()].Add(out[v.GetAsset()], balance)
	}

	return out, nil
}

// GetAccount retrieves an account (volumes + metadata) by address, from live state.
func (c *Client) GetAccount(ctx context.Context, ledgerName, address string) (*commonpb.Account, error) {
	acct, err := c.service.GetAccount(ctx, &servicepb.GetAccountRequest{
		Ledger:         ledgerName,
		Address:        address,
		CollapseColors: true,
	})
	if err != nil {
		return nil, fmt.Errorf("get account %s@%s: %w", address, ledgerName, err)
	}

	return acct, nil
}
