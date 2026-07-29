package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/crc32"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
)

// auditChainLockClass namespaces the chain's advisory lock inside the
// database-global keyspace, following the same two-int4 convention as
// ruleEvalLockClass so reconciliation's locks can never collide with another
// module's on a shared database.
var auditChainLockClass = int32(crc32.ChecksumIEEE([]byte("reconciliation:audit_chain")))

// auditChainLockObj is a constant: there is one chain, so one lock.
//
// A single global lock rather than one per rule is deliberate. Gap detection only
// means something across the whole journal — a per-rule chain would let an entire
// rule's history be removed without leaving a hole anywhere. The cost is that
// journalled writes serialise, which is affordable here in a way it was not for
// the Ledger: V2 gave up per-row chaining under thousands of transactions per
// second, while reconciliation writes a handful of evaluations per rule per
// minute. Three to four orders of magnitude of headroom.
const auditChainLockObj = int32(0)

// ErrAuditChainNotConfigured means storage was built without chain key material.
// The append fails, and with it the operation being recorded — see the comment on
// Storage.chain for why that is the correct outcome rather than skipping the
// journal.
var ErrAuditChainNotConfigured = errors.New("audit chain not configured")

// ErrAuditAppendOutsideTx guards the one mistake that would quietly break the
// chain: pg_advisory_xact_lock released at the end of an implicit single-statement
// transaction leaves the head readable-then-stale, so two concurrent appends could
// both chain onto the same predecessor and fork the chain.
var ErrAuditAppendOutsideTx = errors.New("audit append must run inside a transaction")

// AppendAuditInput is one operation to record.
type AppendAuditInput struct {
	At           time.Time
	Kind         models.AuditEntryKind
	RuleID       *uuid.UUID
	RuleRevision *int64
	AlertID      *uuid.UUID
	EvaluationID *uuid.UUID
	PeriodID     string
	Subject      models.Subject
	// Memento must already be canonical — built through internal/audit so the
	// encoding has exactly one implementation.
	Memento []byte
}

// AppendAuditEntry writes one link of the chain inside the caller's transaction.
//
// The ordering is the whole mechanism: take the chain lock, read the head, hash
// onto it, insert. Holding the lock across the read and the insert is what makes
// the head stable — the same reason Ledger V2's InsertLog takes
// pg_advisory_xact_lock before its trigger reads the previous hash, with the
// comment "we need the last log does not change until the transaction commit".
//
// Being inside the business transaction is the other half: a rolled-back
// operation appends nothing, and a committed one cannot be missing.
func (s *Storage) AppendAuditEntry(ctx context.Context, in AppendAuditInput) (*models.AuditEntry, error) {
	if s.chain == nil {
		return nil, ErrAuditChainNotConfigured
	}
	if _, ok := s.db.(bun.Tx); !ok {
		return nil, fmt.Errorf("%w (kind %s)", ErrAuditAppendOutsideTx, in.Kind)
	}
	if len(in.Memento) == 0 {
		return nil, fmt.Errorf("audit append: empty memento for kind %s", in.Kind)
	}

	if _, err := s.db.NewRaw(
		"SELECT pg_advisory_xact_lock(?, ?)", auditChainLockClass, auditChainLockObj,
	).Exec(ctx); err != nil {
		return nil, e("acquire audit chain lock", err)
	}

	prevSequence, prevHash, err := s.chainHeadLocked(ctx)
	if err != nil {
		return nil, err
	}

	at := in.At
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	subject := audit.NormalizeSubject(in.Subject)
	fields := audit.Fields{
		Sequence:     prevSequence + 1,
		At:           at,
		Kind:         in.Kind,
		RuleID:       in.RuleID,
		RuleRevision: in.RuleRevision,
		AlertID:      in.AlertID,
		EvaluationID: in.EvaluationID,
		PeriodID:     in.PeriodID,
		Subject:      subject,
		Memento:      in.Memento,
	}
	hash, err := s.chain.Compute(prevHash, fields)
	if err != nil {
		return nil, err
	}

	entry := &models.AuditEntry{
		Sequence:      fields.Sequence,
		At:            at,
		Kind:          in.Kind,
		RuleID:        in.RuleID,
		RuleRevision:  in.RuleRevision,
		AlertID:       in.AlertID,
		EvaluationID:  in.EvaluationID,
		PeriodID:      in.PeriodID,
		Subject:       subject,
		Memento:       in.Memento,
		MementoDigest: audit.MementoDigest(in.Memento),
		PrevHash:      prevHash,
		Hash:          hash,
		HashVersion:   audit.HashVersion1,
	}
	if _, err := s.db.NewInsert().Model(entry).Returning("*").Exec(ctx); err != nil {
		return nil, e("append audit entry", err)
	}
	return entry, nil
}

// chainHeadLocked reads the current head. Only meaningful while the chain lock is
// held, hence the name.
func (s *Storage) chainHeadLocked(ctx context.Context) (int64, []byte, error) {
	var (
		sequence int64
		hash     []byte
	)
	err := s.db.NewSelect().Model((*models.AuditEntry)(nil)).
		Column("sequence", "hash").
		Order("sequence DESC").Limit(1).
		Scan(ctx, &sequence, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		// Genesis. No seed is needed: the installation key already separates
		// this chain from any other.
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, e("read audit chain head", err)
	}
	return sequence, hash, nil
}

// ChainHead reports the current head sequence and hash without taking the lock —
// a read-only snapshot for the API, not a basis for appending.
func (s *Storage) ChainHead(ctx context.Context) (int64, []byte, error) {
	return s.chainHeadLocked(ctx)
}

// AuditEntryFilters narrows a journal read. Every field is optional; the zero
// value walks the whole chain.
type AuditEntryFilters struct {
	Kinds        []models.AuditEntryKind
	RuleID       *uuid.UUID
	AlertID      *uuid.UUID
	EvaluationID *uuid.UUID
	PeriodID     string
	Subject      string
	// SystemOnly and HumanOnly split the journal by who acted. This is the
	// filter an auditor reaches for first — "show me every manual intervention"
	// — and it is trustworthy here because the distinction is hash-bound rather
	// than declared.
	SystemOnly bool
	HumanOnly  bool
	FromSeq    int64
	ToSeq      int64
	From       *time.Time
	To         *time.Time
}

// ListAuditEntries returns a page of the journal in chain order, plus the
// sequence to resume after. Ascending, always: reading a hash chain backwards is
// not a thing anyone wants to do.
func (s *Storage) ListAuditEntries(ctx context.Context, f AuditEntryFilters, afterSeq int64, limit int) ([]models.AuditEntry, int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var entries []models.AuditEntry
	q := applyAuditFilters(s.db.NewSelect().Model(&entries), f)
	if afterSeq > 0 {
		q = q.Where("sequence > ?", afterSeq)
	}

	// One more than asked for: its presence is what tells us there is a next
	// page, without a second count query.
	if err := q.Order("sequence ASC").Limit(limit + 1).Scan(ctx); err != nil {
		return nil, 0, e("list audit entries", err)
	}

	var next int64
	if len(entries) > limit {
		entries = entries[:limit]
		next = entries[len(entries)-1].Sequence
	}
	return entries, next, nil
}

func applyAuditFilters(q *bun.SelectQuery, f AuditEntryFilters) *bun.SelectQuery {
	if len(f.Kinds) > 0 {
		kinds := make([]string, 0, len(f.Kinds))
		for _, k := range f.Kinds {
			kinds = append(kinds, string(k))
		}
		q = q.Where("kind IN (?)", bun.In(kinds))
	}
	if f.RuleID != nil {
		q = q.Where("rule_id = ?", *f.RuleID)
	}
	if f.AlertID != nil {
		q = q.Where("alert_id = ?", *f.AlertID)
	}
	if f.EvaluationID != nil {
		q = q.Where("evaluation_id = ?", *f.EvaluationID)
	}
	if f.PeriodID != "" {
		q = q.Where("period_id = ?", f.PeriodID)
	}
	if f.Subject != "" {
		q = q.Where("subject->>'subject' = ?", f.Subject)
	}
	if f.SystemOnly {
		q = q.Where("subject->>'source' = ?", string(models.SubjectSourceSystem))
	}
	if f.HumanOnly {
		q = q.Where("coalesce(subject->>'source', '') <> ?", string(models.SubjectSourceSystem))
	}
	if f.FromSeq > 0 {
		q = q.Where("sequence >= ?", f.FromSeq)
	}
	if f.ToSeq > 0 {
		q = q.Where("sequence <= ?", f.ToSeq)
	}
	if f.From != nil {
		q = q.Where("at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("at <= ?", *f.To)
	}
	return q
}

// GetAuditEntry returns one entry by its logical sequence.
func (s *Storage) GetAuditEntry(ctx context.Context, sequence int64) (*models.AuditEntry, error) {
	var entry models.AuditEntry
	if err := s.db.NewSelect().Model(&entry).Where("sequence = ?", sequence).Scan(ctx); err != nil {
		return nil, e("get audit entry", err)
	}
	return &entry, nil
}

// auditVerifyBatch bounds memory while walking an arbitrarily long chain.
const auditVerifyBatch = 500

// VerifyChain walks a range of the journal and recomputes it.
//
// Four checks per entry, and the walk stops at the first failure — past a break
// every downstream comparison is meaningless, so reporting a list of violations
// would be reporting noise. This mirrors the Ledger checker, which also halts on
// the first hash mismatch.
//
// Neither Ledger V2 nor V3 exposes this. V2 publishes each log's hash and leaves
// the client to reimplement a bespoke canonical form — including its `"id":0`
// placeholder — which no customer, let alone their auditor, will do correctly.
// V3 keeps verification inside an internal checker. Making it an endpoint is what
// turns stored hashes into a usable answer.
func (s *Storage) VerifyChain(ctx context.Context, fromSeq, toSeq int64) (*models.ChainVerification, error) {
	if s.chain == nil {
		return nil, ErrAuditChainNotConfigured
	}
	if fromSeq <= 0 {
		fromSeq = 1
	}

	head, _, err := s.chainHeadLocked(ctx)
	if err != nil {
		return nil, err
	}
	// Clamp only when the caller did not name an end. An explicit range that
	// extends past the head must NOT be silently shrunk to fit: shrinking is
	// exactly what would turn "the last entries were deleted" into a clean bill
	// of health.
	if toSeq <= 0 {
		toSeq = head
	}

	result := &models.ChainVerification{OK: true, FirstSequence: fromSeq, LastSequence: toSeq}

	// The one truncation the journal can detect on its own: a seal committed to a
	// boundary that no longer exists. Past the last seal the tail is genuinely
	// unverifiable from the data alone — the head is derived from the same table
	// an attacker just truncated — which is the strongest practical argument for
	// sealing periods promptly rather than eventually.
	if sealed, err := s.maxSealedSequence(ctx); err != nil {
		return nil, err
	} else if sealed > head {
		missing := head + 1
		result.OK = false
		result.Violation = models.ChainViolationSequenceGap
		result.AtSequence = &missing
		result.Detail = fmt.Sprintf(
			"a period seal closes the journal at sequence %d but the journal now ends at %d: %d entries were removed from the tail",
			sealed, head, sealed-head)
		return result, nil
	}

	if head == 0 || fromSeq > toSeq {
		// An empty journal is intact, and saying so beats inventing a violation.
		result.LastSequence = head
		return result, nil
	}

	// Starting mid-chain needs the preceding entry's hash to check the first
	// link. Absent, the first link cannot be verified and we say so rather than
	// skipping the check silently.
	var expectedPrev []byte
	if fromSeq > 1 {
		prev, err := s.GetAuditEntry(ctx, fromSeq-1)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if prev != nil {
			expectedPrev = prev.Hash
		}
	}

	seals, err := s.sealsInRange(ctx, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	sealByLastSeq := make(map[int64]*models.PeriodSeal, len(seals))
	for i := range seals {
		sealByLastSeq[seals[i].LastSequence] = &seals[i]
	}

	expectedSeq := fromSeq
	cursor := fromSeq - 1

	for {
		var batch []models.AuditEntry
		if err := s.db.NewSelect().Model(&batch).
			Where("sequence > ?", cursor).
			Where("sequence <= ?", toSeq).
			Order("sequence ASC").Limit(auditVerifyBatch).Scan(ctx); err != nil {
			return nil, e("walk audit chain", err)
		}
		if len(batch) == 0 {
			break
		}

		for i := range batch {
			entry := &batch[i]

			if entry.Sequence != expectedSeq {
				missing := expectedSeq
				result.OK = false
				result.Violation = models.ChainViolationSequenceGap
				result.AtSequence = &missing
				result.Detail = fmt.Sprintf(
					"sequence %d is missing: the journal jumps from %d to %d, so an entry was removed",
					missing, expectedSeq-1, entry.Sequence)
				return result, nil
			}

			if entry.HashVersion != audit.HashVersion1 {
				seq := entry.Sequence
				result.OK = false
				result.Violation = models.ChainViolationHashMismatch
				result.AtSequence = &seq
				result.Detail = fmt.Sprintf("entry %d declares unknown hash version %d", seq, entry.HashVersion)
				return result, nil
			}

			if !bytes.Equal(audit.MementoDigest(entry.Memento), entry.MementoDigest) {
				seq := entry.Sequence
				result.OK = false
				result.Violation = models.ChainViolationMementoDigest
				result.AtSequence = &seq
				result.Detail = fmt.Sprintf("entry %d's payload no longer matches its digest", seq)
				return result, nil
			}

			// expectedPrev is nil only at genesis or when a mid-chain start had
			// no predecessor to read.
			if expectedPrev != nil && !bytes.Equal(entry.PrevHash, expectedPrev) {
				seq := entry.Sequence
				result.OK = false
				result.Violation = models.ChainViolationBrokenLink
				result.AtSequence = &seq
				result.Detail = fmt.Sprintf(
					"entry %d does not link to its predecessor: entries were reordered or spliced", seq)
				return result, nil
			}

			computed, err := s.chain.Compute(entry.PrevHash, audit.Fields{
				Sequence:     entry.Sequence,
				At:           entry.At,
				Kind:         entry.Kind,
				RuleID:       entry.RuleID,
				RuleRevision: entry.RuleRevision,
				AlertID:      entry.AlertID,
				EvaluationID: entry.EvaluationID,
				PeriodID:     entry.PeriodID,
				Subject:      entry.Subject,
				Memento:      entry.Memento,
			})
			if err != nil {
				return nil, err
			}
			if !bytes.Equal(computed, entry.Hash) {
				seq := entry.Sequence
				result.OK = false
				result.Violation = models.ChainViolationHashMismatch
				result.AtSequence = &seq
				result.Detail = fmt.Sprintf(
					"entry %d's stored hash does not match its contents: a hashed field was altered after the fact", seq)
				return result, nil
			}

			if seal, ok := sealByLastSeq[entry.Sequence]; ok {
				if err := verifySealAgainstHead(seal, entry.Hash); err != nil {
					seq := entry.Sequence
					result.OK = false
					result.Violation = models.ChainViolationHashMismatch
					result.AtSequence = &seq
					result.Detail = err.Error()
					return result, nil
				}
				result.SealsCrossed = append(result.SealsCrossed, seal.PeriodID)
			}

			expectedPrev = entry.Hash
			expectedSeq = entry.Sequence + 1
			result.EntriesWalked++
			cursor = entry.Sequence
		}

		if len(batch) < auditVerifyBatch {
			break
		}
	}

	// The range was expected to end at toSeq; falling short means the tail is
	// gone, which a per-entry gap check cannot see.
	if expectedSeq-1 < toSeq {
		missing := expectedSeq
		result.OK = false
		result.Violation = models.ChainViolationSequenceGap
		result.AtSequence = &missing
		result.Detail = fmt.Sprintf(
			"the journal ends at %d but the head reports %d: %d entries are missing from the tail",
			expectedSeq-1, toSeq, toSeq-(expectedSeq-1))
	}
	return result, nil
}

// maxSealedSequence is the highest boundary any period seal committed to.
func (s *Storage) maxSealedSequence(ctx context.Context) (int64, error) {
	var max int64
	if err := s.db.NewSelect().Model((*models.PeriodSeal)(nil)).
		ColumnExpr("coalesce(max(last_sequence), 0)").Scan(ctx, &max); err != nil {
		return 0, e("read highest sealed sequence", err)
	}
	return max, nil
}

// verifySealAgainstHead re-derives a seal's sealing hash from its own stored
// fields plus the chain head it claims to close. A seal whose fields were edited,
// or that was moved onto a different head, fails here.
func verifySealAgainstHead(seal *models.PeriodSeal, headHash []byte) error {
	recomputed := audit.ComputeSealingHash(audit.SealInput{
		PeriodID:      seal.PeriodID,
		FirstSequence: seal.FirstSequence,
		LastSequence:  seal.LastSequence,
		EntryCount:    seal.EntryCount,
		LastAuditHash: headHash,
		StateHash:     seal.StateHash,
	})
	if !bytes.Equal(recomputed, seal.SealingHash) {
		return fmt.Errorf(
			"the seal for period %q does not match the journal it claims to close", seal.PeriodID)
	}
	if len(seal.LastAuditHash) > 0 && !bytes.Equal(seal.LastAuditHash, headHash) {
		return fmt.Errorf(
			"the seal for period %q records a different chain head than the journal has at sequence %d",
			seal.PeriodID, seal.LastSequence)
	}
	return nil
}
