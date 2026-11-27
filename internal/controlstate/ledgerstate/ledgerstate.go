// Package ledgerstate is the Ledger v3 adapter of the controlstate port, on
// the account layout of RFC 0022 (9959818): one account per entity, reused for
// life, whose balance in the asset V is its version.
//
// A unit of work becomes one atomic ApplyBatch of Numscript transactions:
//
//   - A write posts [V 1] from the entity's gauge to the entity, under the
//     transaction reference <entity>:v<n+1>. A reference is unique for the life
//     of the ledger, so a writer that read an outdated version gets
//     AlreadyExists. The fields go to account metadata in the same transaction.
//   - A move posts one unit of a status asset between the gauge and the
//     entity. Giving back a unit the entity does not hold fails on
//     insufficient funds, so the move also proves the status it leaves.
//   - A precondition without a write proves that the V balance still equals
//     the version read: it moves the expected n units to a TRANSIENT account,
//     the remainder to a second TRANSIENT account, and the n units back. A
//     TRANSIENT account must be empty at the end of the batch, so any
//     remainder, a later version, rejects the batch.
//   - A bump posts to a counter (OCC) and sets live metadata, without V.
//   - A record is transaction metadata: on the transaction of its subject's
//     write when there is one, otherwise on a transaction of its own that
//     posts one unit from the gauge to itself.
//
// The gauge is the overdraft source of every posting. Its negative balances
// are counts: with the reconciliation layout, one gauge per rule gives the
// rule's open, acknowledged and accepted alerts and its occurrences.
package ledgerstate

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/formancehq/reconciliation/internal/controlstate"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/formancehq/reconciliation/internal/ledgerschema"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	assetVersion = controlstate.VersionAsset
	assetRecord  = controlstate.RecordAsset

	metaFields        = "cs_fields"
	metaSubject       = "cs_subject"
	metaRecordKind    = "cs_record_kind"
	metaRecordFields  = "cs_record_fields"
	liveMetaPrefix    = "live."
	idempotencyPrefix = "cs2:"
)

// Store is a controlstate.ControlState on one ledger.
type Store struct {
	client *ledger.Client
	ledger string
	layout Layout
}

var _ controlstate.ControlState = (*Store)(nil)

// New returns a store that keeps its state in ledgerName, on the given layout.
func New(client *ledger.Client, ledgerName string, layout Layout) *Store {
	return &Store{client: client, ledger: ledgerName, layout: layout}
}

// Layout returns the store's account layout.
func (s *Store) Layout() Layout { return s.layout }

// Provision declares the account types the layout needs that the ledger does
// not have yet, and the indexes the store reads through. It is idempotent.
func (s *Store) Provision(ctx context.Context) error {
	info, err := s.client.GetLedgerInfo(ctx, s.ledger)
	if err != nil {
		return fmt.Errorf("read ledger %q: %w", s.ledger, err)
	}
	types := s.layout.AccountTypes()
	for _, name := range slices.Sorted(maps.Keys(types)) {
		if _, ok := info.GetAccountTypes()[name]; ok {
			continue
		}
		if err := s.client.AddAccountType(ctx, s.ledger, types[name]); err != nil {
			return fmt.Errorf("add account type %q: %w", name, err)
		}
	}
	// An index needs its field in the metadata schema first.
	declare := func(target commonpb.TargetType, have map[string]*commonpb.MetadataFieldSchema, keys []string) error {
		for _, k := range keys {
			if _, ok := have[k]; ok {
				continue
			}
			if err := s.client.SetMetadataFieldType(ctx, s.ledger, &commonpb.SetMetadataFieldTypeCommand{TargetType: target, Key: k, Type: commonpb.MetadataType_METADATA_TYPE_STRING}); err != nil {
				return fmt.Errorf("declare metadata field %q: %w", k, err)
			}
		}
		return nil
	}
	if err := declare(commonpb.TargetType_TARGET_TYPE_TRANSACTION, info.GetMetadataSchema().GetTransactionFields(), append([]string{metaSubject}, s.layout.TxIndexes()...)); err != nil {
		return err
	}
	if err := declare(commonpb.TargetType_TARGET_TYPE_ACCOUNT, info.GetMetadataSchema().GetAccountFields(), s.layout.AccountIndexes()); err != nil {
		return err
	}
	existing, err := s.client.ListIndexIDs(ctx, s.ledger)
	if err != nil {
		return fmt.Errorf("list indexes: %w", err)
	}
	want := []*commonpb.IndexID{commonpb.TxMetadataIndexID(metaSubject)}
	for _, k := range s.layout.TxIndexes() {
		want = append(want, commonpb.TxMetadataIndexID(k))
	}
	for _, k := range s.layout.AccountIndexes() {
		want = append(want, commonpb.AccountMetadataIndexID(k))
	}
	for _, idx := range want {
		if slices.ContainsFunc(existing, idx.EqualVT) {
			continue
		}
		if err := s.client.CreateIndex(ctx, s.ledger, &servicepb.CreateIndexRequest{Id: idx}); err != nil {
			return fmt.Errorf("create index %v: %w", idx, err)
		}
	}
	return nil
}

// Load implements controlstate.ControlState with one ListAccounts call on an
// or of exact addresses.
func (s *Store) Load(ctx context.Context, keys ...controlstate.Key) (controlstate.Snapshot, error) {
	snap := make(controlstate.Snapshot, len(keys))
	if len(keys) == 0 {
		return snap, nil
	}
	byAddr := make(map[string]controlstate.Key, len(keys))
	filters := make([]*commonpb.QueryFilter, 0, len(keys))
	for _, k := range keys {
		snap[k] = controlstate.Entry{Key: k}
		addr, err := s.layout.Address(k)
		if err != nil {
			return nil, err
		}
		if _, dup := byAddr[addr]; dup {
			continue
		}
		byAddr[addr] = k
		filters = append(filters, ledgerschema.FilterAddressExact(addr))
	}
	filter := filters[0]
	if len(filters) > 1 {
		filter = ledgerschema.FilterAny(filters...)
	}
	err := s.client.QueryAccountsFunc(ctx, s.ledger, filter, func(acct *commonpb.Account) error {
		k, ok := byAddr[acct.GetAddress()]
		if !ok {
			return nil
		}
		e, err := entryOf(k, acct)
		if err != nil {
			return err
		}
		snap[k] = e
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}
	return snap, nil
}

func entryOf(k controlstate.Key, acct *commonpb.Account) (controlstate.Entry, error) {
	e := controlstate.Entry{Key: k}
	for _, v := range acct.GetVolumes() {
		if v.GetColor() != "" {
			continue
		}
		b, err := balanceOf(v.GetVolumes())
		if err != nil {
			return e, fmt.Errorf("load %s: balance of %s: %w", k, v.GetAsset(), err)
		}
		if !b.IsInt64() {
			return e, fmt.Errorf("load %s: balance of %s out of range: %s", k, v.GetAsset(), b)
		}
		switch {
		case v.GetAsset() == assetVersion:
			e.Version = controlstate.Version(b.Int64())
		case b.Sign() != 0:
			if e.Balances == nil {
				e.Balances = map[string]int64{}
			}
			e.Balances[v.GetAsset()] = b.Int64()
		}
	}
	md := commonpb.MetadataToMap(acct.GetMetadata())
	if e.Version != controlstate.Absent {
		if err := json.Unmarshal([]byte(cmp.Or(md[metaFields], "{}")), &e.Fields); err != nil {
			return e, fmt.Errorf("load %s: fields: %w", k, err)
		}
	}
	for mk, v := range md {
		if name, ok := strings.CutPrefix(mk, liveMetaPrefix); ok {
			if e.Live == nil {
				e.Live = map[string]string{}
			}
			e.Live[name] = v
		}
	}
	return e, nil
}

// Commit implements controlstate.ControlState.
func (s *Store) Commit(ctx context.Context, uow controlstate.UnitOfWork) (controlstate.Versions, error) {
	if err := uow.Validate(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(uow.IdempotencyKey))
	requests, err := s.requests(uow, idempotencyPrefix+hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, err
	}
	if _, err := s.client.ApplyIdempotent(ctx, idempotencyPrefix+hex.EncodeToString(sum[:]), requests...); err != nil {
		return nil, mapError(err)
	}
	return uow.Versions(), nil
}

func byKey(a, b controlstate.Key) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID))
}

// requests builds the batch: one transaction per precondition, in key order,
// then one per bump, in key order, then one per record not merged into its
// subject's write, in the order given.
//
// Bumps and records carry a reference derived from the unit of work's key.
// A replay inside the ledger's idempotency window returns the first outcome;
// a replay after it hits the reference and fails with ErrConflict instead of
// adding the counter or the record twice.
func (s *Store) requests(uow controlstate.UnitOfWork, uowRef string) ([]*servicepb.Request, error) {
	extra := 0
	writes := make(map[controlstate.Key]controlstate.Write, len(uow.Put))
	for _, w := range uow.Put {
		writes[w.Key] = w
	}
	// The first record about a written key rides on that key's transaction.
	merged := map[controlstate.Key]int{}
	for i, r := range uow.Records {
		if _, ok := writes[r.Subject]; ok {
			if _, done := merged[r.Subject]; !done {
				merged[r.Subject] = i
			}
		}
	}

	var out []*servicepb.Request
	for _, p := range slices.SortedFunc(slices.Values(uow.Expect), func(a, b controlstate.Precondition) int { return byKey(a.Key, b.Key) }) {
		addr, err := s.layout.Address(p.Key)
		if err != nil {
			return nil, err
		}
		gauge, err := s.layout.Gauge(p.Key)
		if err != nil {
			return nil, err
		}
		txMeta := s.txMeta(p.Key)
		w, written := writes[p.Key]
		if !written {
			guardA, guardB := s.layout.Guards()
			out = append(out, s.apply(&servicepb.CreateTransactionPayload{
				Script:   &commonpb.Script{Plain: guardScript(addr, guardA, guardB, uint64(p.Version))},
				Metadata: commonpb.MetadataFromMap(txMeta),
			}))
			continue
		}
		encoded, err := encode(w.Fields)
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", p.Key, err)
		}
		acctMeta := map[string]string{metaFields: encoded}
		// Every projected key is written, empty when the field is absent, so a
		// list filter never matches a value the entity no longer has.
		for _, f := range s.layout.Projected(p.Key.Kind) {
			acctMeta[f] = w.Fields[f]
		}
		if i, ok := merged[p.Key]; ok {
			if err := addRecord(txMeta, uow.Records[i]); err != nil {
				return nil, err
			}
		}
		out = append(out, s.apply(&servicepb.CreateTransactionPayload{
			Script:          &commonpb.Script{Plain: writeScript(addr, gauge, w.Move)},
			Reference:       s.layout.Reference(p.Key, p.Version+1),
			Metadata:        commonpb.MetadataFromMap(txMeta),
			AccountMetadata: map[string]*commonpb.MetadataMap{addr: {Values: commonpb.MetadataFromMap(acctMeta)}},
		}))
	}

	for _, b := range slices.SortedFunc(slices.Values(uow.Bumps), func(a, b controlstate.Bump) int { return byKey(a.Key, b.Key) }) {
		addr, err := s.layout.Address(b.Key)
		if err != nil {
			return nil, err
		}
		gauge, err := s.layout.Gauge(b.Key)
		if err != nil {
			return nil, err
		}
		live := make(map[string]string, len(b.Live))
		for k, v := range b.Live {
			live[liveMetaPrefix+k] = v
		}
		payload := &servicepb.CreateTransactionPayload{
			Script:    &commonpb.Script{Plain: bumpScript(addr, gauge, b.Add)},
			Reference: fmt.Sprintf("%s:%d", uowRef, extra),
			Metadata:  commonpb.MetadataFromMap(s.txMeta(b.Key)),
		}
		extra++
		if len(live) > 0 {
			payload.AccountMetadata = map[string]*commonpb.MetadataMap{addr: {Values: commonpb.MetadataFromMap(live)}}
		}
		out = append(out, s.apply(payload))
	}

	for i, r := range uow.Records {
		if j, ok := merged[r.Subject]; ok && j == i {
			continue
		}
		gauge, err := s.layout.Gauge(r.Subject)
		if err != nil {
			return nil, err
		}
		txMeta := s.txMeta(r.Subject)
		if err := addRecord(txMeta, r); err != nil {
			return nil, err
		}
		out = append(out, s.apply(&servicepb.CreateTransactionPayload{
			Script:    &commonpb.Script{Plain: send(assetRecord, 1, gauge+" allowing unbounded overdraft", gauge)},
			Reference: fmt.Sprintf("%s:%d", uowRef, extra),
			Metadata:  commonpb.MetadataFromMap(txMeta),
		}))
		extra++
	}
	return out, nil
}

func (s *Store) txMeta(k controlstate.Key) map[string]string {
	m := map[string]string{metaSubject: k.String()}
	maps.Copy(m, s.layout.TxMetadata(k))
	return m
}

func addRecord(txMeta map[string]string, r controlstate.Record) error {
	encoded, err := encode(r.Fields)
	if err != nil {
		return fmt.Errorf("record on %s: %w", r.Subject, err)
	}
	txMeta[metaRecordKind] = r.Kind
	txMeta[metaRecordFields] = encoded
	return nil
}

func (s *Store) apply(tx *servicepb.CreateTransactionPayload) *servicepb.Request {
	return &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
		Ledger: s.ledger,
		Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: tx}},
	}}}
}

func send(asset string, amount uint64, source, destination string) string {
	return fmt.Sprintf("send [%s %d] (\n\tsource = @%s\n\tdestination = @%s\n)\n", asset, amount, source, destination)
}

// writeScript gives back the units the write moves out, takes the next
// version, then takes the units it moves in. Assets are sorted so that a
// replay is byte-equal.
func writeScript(entity, gauge string, move map[string]int64) string {
	var b strings.Builder
	for _, a := range slices.Sorted(maps.Keys(move)) {
		if d := move[a]; d < 0 {
			b.WriteString(send(a, uint64(-d), entity, gauge))
		}
	}
	b.WriteString(send(assetVersion, 1, gauge+" allowing unbounded overdraft", entity))
	for _, a := range slices.Sorted(maps.Keys(move)) {
		if d := move[a]; d > 0 {
			b.WriteString(send(a, uint64(d), gauge+" allowing unbounded overdraft", entity))
		}
	}
	return b.String()
}

func bumpScript(entity, gauge string, add map[string]int64) string {
	var b strings.Builder
	for _, a := range slices.Sorted(maps.Keys(add)) {
		b.WriteString(send(a, uint64(add[a]), gauge+" allowing unbounded overdraft", entity))
	}
	return b.String()
}

// guardScript proves that the entity's V balance is exactly n without
// changing it. guardA and guardB are TRANSIENT accounts.
func guardScript(entity, guardA, guardB string, n uint64) string {
	return send(assetVersion, n, entity, guardA) +
		fmt.Sprintf("send [%s *] (\n\tsource = @%s\n\tdestination = @%s\n)\n", assetVersion, entity, guardB) +
		send(assetVersion, n, guardA, entity)
}

// encode stores fields as one JSON value. encoding/json sorts map keys, so
// equal fields encode equally and a replay stays byte-equal.
func encode(fields map[string]string) (string, error) {
	b, err := json.Marshal(fields)
	return string(b), err
}

// conflictReasons are the Ledger refusals that mean "the state moved on":
// a version or create already taken, a status unit no longer held, or a
// guard that found a later version. Any other refusal, such as an address
// outside the chart, is returned as it is: retrying would not help.
var conflictReasons = map[string]bool{
	"TRANSACTION_REFERENCE_CONFLICT": true,
	"INSUFFICIENT_FUNDS":             true,
	"TRANSIENT_ACCOUNT_NON_ZERO":     true,
}

// mapError turns ledger refusals into port errors.
func mapError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	reason := ""
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			reason = info.GetReason()
		}
	}
	switch {
	case reason == "IDEMPOTENCY_KEY_CONFLICT",
		st.Code() == codes.AlreadyExists && strings.Contains(st.Message(), "idempotency key"):
		return fmt.Errorf("%w: %s", controlstate.ErrKeyReused, st.Message())
	case conflictReasons[reason],
		reason == "NUMSCRIPT_EXECUTION_ERROR" && strings.Contains(strings.ToLower(st.Message()), "insufficient"):
		return fmt.Errorf("%w: %s", controlstate.ErrConflict, st.Message())
	case reason == "" && (st.Code() == codes.FailedPrecondition || st.Code() == codes.AlreadyExists):
		// No ErrorInfo: keep the previous mapping.
		return fmt.Errorf("%w: %s", controlstate.ErrConflict, st.Message())
	default:
		return fmt.Errorf("ledger refused the unit of work (%s): %w", reason, err)
	}
}

// Records returns the records committed about subject, oldest first, through
// the indexed transaction metadata cs_subject. It waits for the ledger's
// projections first. It serves the contract suite and diagnostics.
func (s *Store) Records(ctx context.Context, subject controlstate.Key) ([]controlstate.Record, error) {
	if _, err := s.client.Service().Barrier(ctx, &servicepb.BarrierRequest{}); err != nil {
		return nil, fmt.Errorf("barrier: %w", err)
	}
	type item struct {
		id uint64
		r  controlstate.Record
	}
	var items []item
	err := s.client.ListTransactionsFunc(ctx, s.ledger, ledgerschema.FilterMetadataString(metaSubject, subject.String()), func(tx *commonpb.Transaction) error {
		md := commonpb.MetadataToMap(tx.GetMetadata())
		kind, ok := md[metaRecordKind]
		if !ok {
			return nil
		}
		r := controlstate.Record{Kind: kind, Subject: subject}
		if err := json.Unmarshal([]byte(md[metaRecordFields]), &r.Fields); err != nil {
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

// VersionOf parses the V balance of an account, for callers that read the
// ledger directly.
func VersionOf(acct *commonpb.Account) controlstate.Version {
	for _, v := range acct.GetVolumes() {
		if v.GetAsset() == assetVersion && v.GetColor() == "" {
			if b, err := balanceOf(v.GetVolumes()); err == nil && b.IsUint64() {
				return controlstate.Version(b.Uint64())
			}
		}
	}
	return controlstate.Absent
}

// balanceOf reads the balance the Ledger returns, or input minus output.
func balanceOf(v *commonpb.VolumesWithBalance) (*big.Int, error) {
	if v == nil {
		return new(big.Int), nil
	}
	if v.GetBalance() != nil {
		return v.GetBalance().ToBigInt()
	}
	in, err := v.GetInput().ToBigInt()
	if err != nil {
		return nil, err
	}
	out, err := v.GetOutput().ToBigInt()
	if err != nil {
		return nil, err
	}
	return in.Sub(in, out), nil
}
