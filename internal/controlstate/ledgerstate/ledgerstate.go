// Package ledgerstate is the Ledger v3 adapter of the controlstate port.
//
// A unit of work becomes one atomic ApplyBatch of Numscript transactions:
//
//   - The version of an entity is the location of one version token: version n
//     means the token sits on the account <ns>:v:<kind>:<id>:<n>.
//   - A create mints the token onto version 1 under a transaction reference
//     unique to the entity, so a second create fails with AlreadyExists.
//   - A write moves the token from version n to n+1. The move fails with
//     insufficient funds when the entity is no longer at version n.
//   - A precondition without a write moves the token onto itself, which proves
//     the version without changing it.
//   - The fields of an entity, and its version, are account metadata on
//     <ns>:e:<kind>:<id>, set in the same transaction as the token move.
//   - A record is a transaction to <ns>:r:<kind>:<id> that carries the record
//     in its metadata.
//
// The batch content derives from the unit of work alone, so a replay with the
// same idempotency key is byte-equal and returns the first outcome.
package ledgerstate

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/formancehq/reconciliation/internal/controlstate"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/formancehq/reconciliation/internal/ledgerschema"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	assetVersion = "CSV"
	assetRecord  = "CSR"

	metaVersion       = "cs_version"
	metaFields        = "cs_fields"
	metaRecordKind    = "cs_record_kind"
	metaRecordSubject = "cs_record_subject"
)

// Store is a controlstate.ControlState on one ledger.
type Store struct {
	client *ledger.Client
	ledger string
	ns     string
}

var _ controlstate.ControlState = (*Store)(nil)

// New returns a store that keeps its state in ledgerName, under the account
// namespace "cs".
func New(client *ledger.Client, ledgerName string) *Store {
	return &Store{client: client, ledger: ledgerName, ns: "cs"}
}

// plainSegment is what a ledger address segment may hold as is. Anything else
// is hex-encoded behind an underscore, which plain segments never contain, so
// two different IDs never share an address.
var plainSegment = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

func segment(s string) string {
	if plainSegment.MatchString(s) {
		return s
	}
	return "_" + hex.EncodeToString([]byte(s))
}

func (s *Store) entityAccount(k controlstate.Key) string {
	return fmt.Sprintf("%s:e:%s:%s", s.ns, segment(k.Kind), segment(k.ID))
}

func (s *Store) versionAccount(k controlstate.Key, v controlstate.Version) string {
	return fmt.Sprintf("%s:v:%s:%s:%d", s.ns, segment(k.Kind), segment(k.ID), v)
}

func (s *Store) recordAccount(k controlstate.Key) string {
	return fmt.Sprintf("%s:r:%s:%s", s.ns, segment(k.Kind), segment(k.ID))
}

func (s *Store) pool() string { return s.ns + ":pool" }

// Provision declares what the store reads through: the transaction address
// index that Records lists by. It is idempotent. The control ledger
// provisioner already declares this index, so a store on the control ledger
// needs no extra step.
func (s *Store) Provision(ctx context.Context) error {
	idx := commonpb.TxBuiltinIndexID(commonpb.TransactionBuiltinIndex_TX_BUILTIN_INDEX_ADDRESS)
	if err := s.client.CreateIndex(ctx, s.ledger, &servicepb.CreateIndexRequest{Id: idx}); err != nil {
		return fmt.Errorf("create transaction address index: %w", err)
	}
	return nil
}

// Load implements controlstate.ControlState.
func (s *Store) Load(ctx context.Context, keys ...controlstate.Key) (controlstate.Snapshot, error) {
	snap := make(controlstate.Snapshot, len(keys))
	for _, k := range keys {
		e := controlstate.Entry{Key: k}
		acct, err := s.client.GetAccount(ctx, s.ledger, s.entityAccount(k))
		switch {
		case status.Code(err) == codes.NotFound:
		case err != nil:
			return nil, fmt.Errorf("load %s: %w", k, err)
		default:
			md := commonpb.MetadataToMap(acct.GetMetadata())
			if raw, ok := md[metaVersion]; ok {
				v, err := strconv.ParseUint(raw, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("load %s: version %q: %w", k, raw, err)
				}
				e.Version = controlstate.Version(v)
				if err := json.Unmarshal([]byte(md[metaFields]), &e.Fields); err != nil {
					return nil, fmt.Errorf("load %s: fields: %w", k, err)
				}
			}
		}
		snap[k] = e
	}
	return snap, nil
}

// Commit implements controlstate.ControlState.
func (s *Store) Commit(ctx context.Context, uow controlstate.UnitOfWork) (controlstate.Versions, error) {
	if err := uow.Validate(); err != nil {
		return nil, err
	}
	requests, err := s.requests(uow)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(uow.IdempotencyKey))
	if _, err := s.client.ApplyIdempotent(ctx, s.ns+":"+hex.EncodeToString(sum[:]), requests...); err != nil {
		return nil, mapError(err)
	}
	return uow.Versions(), nil
}

// requests builds the batch: one transaction per precondition, in key order,
// then one per record, in the order given.
func (s *Store) requests(uow controlstate.UnitOfWork) ([]*servicepb.Request, error) {
	writes := make(map[controlstate.Key]map[string]string, len(uow.Put))
	for _, w := range uow.Put {
		writes[w.Key] = w.Fields
	}
	expect := slices.SortedFunc(slices.Values(uow.Expect), func(a, b controlstate.Precondition) int {
		return cmp.Or(cmp.Compare(a.Key.Kind, b.Key.Kind), cmp.Compare(a.Key.ID, b.Key.ID))
	})

	var out []*servicepb.Request
	for _, p := range expect {
		fields, written := writes[p.Key]
		tx := &servicepb.CreateTransactionPayload{}
		switch {
		case p.Version == controlstate.Absent:
			tx.Script = send(assetVersion, s.pool()+" allowing unbounded overdraft", s.versionAccount(p.Key, 1))
			tx.Reference = s.ns + ":create:" + segment(p.Key.Kind) + ":" + segment(p.Key.ID)
		case written:
			tx.Script = send(assetVersion, s.versionAccount(p.Key, p.Version), s.versionAccount(p.Key, p.Version+1))
		default:
			tx.Script = send(assetVersion, s.versionAccount(p.Key, p.Version), s.versionAccount(p.Key, p.Version))
		}
		if written {
			encoded, err := encodeFields(fields)
			if err != nil {
				return nil, fmt.Errorf("write %s: %w", p.Key, err)
			}
			tx.AccountMetadata = map[string]*commonpb.MetadataMap{
				s.entityAccount(p.Key): {Values: commonpb.MetadataFromMap(map[string]string{
					metaVersion: strconv.FormatUint(uint64(p.Version+1), 10),
					metaFields:  encoded,
				})},
			}
		}
		out = append(out, s.apply(tx))
	}

	for _, r := range uow.Records {
		encoded, err := encodeFields(r.Fields)
		if err != nil {
			return nil, fmt.Errorf("record on %s: %w", r.Subject, err)
		}
		out = append(out, s.apply(&servicepb.CreateTransactionPayload{
			Script: send(assetRecord, s.pool()+" allowing unbounded overdraft", s.recordAccount(r.Subject)),
			Metadata: commonpb.MetadataFromMap(map[string]string{
				metaRecordKind:    r.Kind,
				metaRecordSubject: r.Subject.String(),
				metaFields:        encoded,
			}),
		}))
	}
	return out, nil
}

func (s *Store) apply(tx *servicepb.CreateTransactionPayload) *servicepb.Request {
	return &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
		Ledger: s.ledger,
		Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: tx}},
	}}}
}

// send moves one unit of asset. source may carry an overdraft clause.
func send(asset, source, destination string) *commonpb.Script {
	return &commonpb.Script{Plain: fmt.Sprintf("send [%s 1] (\n\tsource = @%s\n\tdestination = @%s\n)\n", asset, source, destination)}
}

// encodeFields stores the fields as one JSON value. encoding/json sorts map
// keys, so equal fields encode equally and a replay stays byte-equal.
func encodeFields(fields map[string]string) (string, error) {
	b, err := json.Marshal(fields)
	return string(b), err
}

// mapError turns ledger refusals into port errors: a guard that no longer
// holds, or a duplicate create, is a conflict; a reused idempotency key with
// a different content is ErrKeyReused.
func mapError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.FailedPrecondition:
		return fmt.Errorf("%w: %s", controlstate.ErrConflict, st.Message())
	case codes.AlreadyExists:
		if strings.Contains(st.Message(), "idempotency key") {
			return fmt.Errorf("%w: %s", controlstate.ErrKeyReused, st.Message())
		}
		return fmt.Errorf("%w: %s", controlstate.ErrConflict, st.Message())
	default:
		return err
	}
}

// Records returns the records committed about subject, oldest first. It waits
// for the ledger's projections to catch up first, so it sees every committed
// record. It serves the contract suite and diagnostics, not the service.
func (s *Store) Records(ctx context.Context, subject controlstate.Key) ([]controlstate.Record, error) {
	if _, err := s.client.Service().Barrier(ctx, &servicepb.BarrierRequest{}); err != nil {
		return nil, fmt.Errorf("barrier: %w", err)
	}
	type item struct {
		id uint64
		r  controlstate.Record
	}
	var items []item
	err := s.client.ListTransactionsFunc(ctx, s.ledger, ledgerschema.FilterAddressExact(s.recordAccount(subject)), func(tx *commonpb.Transaction) error {
		md := commonpb.MetadataToMap(tx.GetMetadata())
		if md[metaRecordSubject] != subject.String() {
			return nil
		}
		r := controlstate.Record{Kind: md[metaRecordKind], Subject: subject}
		if err := json.Unmarshal([]byte(md[metaFields]), &r.Fields); err != nil {
			return fmt.Errorf("record fields: %w", err)
		}
		items = append(items, item{id: tx.GetId(), r: r})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b item) int { return cmp.Compare(a.id, b.id) })
	out := make([]controlstate.Record, 0, len(items))
	for _, it := range items {
		out = append(out, it.r)
	}
	return out, nil
}
