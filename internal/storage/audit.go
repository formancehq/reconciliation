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

// lockAuditChain serialises the chain for the rest of the caller's transaction.
//
// LOCK-ORDER INVARIANT: this lock is acquired BEFORE any alert row lock, in
// every path. Violating it deadlocks.
//
// The evaluation path necessarily takes the chain lock first — it journals the
// evaluation before driving any alert, and driving an alert takes that alert's
// row lock. The manual paths (ack, resolve, accept, snooze, unsnooze) used to do
// the opposite: SELECT ... FOR UPDATE on the alert row, then reach the chain lock
// on their way through appendAlertEvent. Two concurrent transactions in opposite
// orders is a deadlock, which Postgres resolves by aborting one — surfacing as a
// 500 on whichever operator lost.
//
// So every alert-mutating transaction now takes this lock as its first act. The
// cost is a slightly longer hold on a lock those transactions were all going to
// take anyway, since each of them ends in an append; the benefit is that the
// deadlock is impossible by construction rather than merely unlikely.
//
// Exported through this helper rather than inlined in AppendAuditEntry because
// two callers need the lock *before* they decide whether to append at all: the
// alert-transition and evaluation paths check whether the target period is
// sealed, and that check is only meaningful if a seal cannot commit between the
// check and the append. Postgres advisory locks are re-entrant within a
// transaction, so taking it here and again inside AppendAuditEntry is free.
func (s *Storage) lockAuditChain(ctx context.Context) error {
	if _, err := s.db.NewRaw(
		"SELECT pg_advisory_xact_lock(?, ?)", auditChainLockClass, auditChainLockObj,
	).Exec(ctx); err != nil {
		return e("acquire audit chain lock", err)
	}
	return nil
}

// assertPeriodWritable rejects a write into a closed period. MUST be called with
// the chain lock already held — see lockAuditChain. Checking without the lock is
// the race NumaryBot caught on the first review: the check passes while a seal is
// still uncommitted, the write then blocks on the lock, and appends *after* the
// seal without rechecking, landing evidence in books that are already closed.
func (s *Storage) assertPeriodWritable(ctx context.Context, periodID, what string) error {
	sealed, err := s.IsPeriodSealed(ctx, periodID)
	if err != nil {
		return err
	}
	if sealed {
		return fmt.Errorf("%w: period %q was closed, so %s can no longer be recorded in it",
			ErrPeriodSealed, periodID, what)
	}
	return nil
}

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

	if err := s.lockAuditChain(ctx); err != nil {
		return nil, err
	}

	prevSequence, prevHash, err := s.chainHeadLocked(ctx)
	if err != nil {
		return nil, err
	}

	at := in.At
	if at.IsZero() {
		at = time.Now()
	}
	// Truncate to what Postgres can actually store before hashing it.
	//
	// timestamptz keeps microseconds; time.Now() on Linux carries nanoseconds. A
	// hash taken over the un-truncated value can never be reproduced from the
	// stored row, so every entry would verify as tampered — and only on Linux,
	// because macOS wall-clock readings are already microsecond-granular. That is
	// a bug the unit tests structurally cannot catch: they run on the developer's
	// host while production runs in a container, so the two platforms disagree
	// about whether the bug exists. Truncating here makes the hashed value equal
	// to the persisted value on every platform.
	at = at.UTC().Truncate(time.Microsecond)

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
		q = q.Where("kind IN (?)", bun.List(kinds))
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
	requestedTo := toSeq
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

	// A period sealed while the journal was still empty commits to the boundary
	// sequence 0, and no entry exists there for the walk to cross. sealsInRange
	// starts at fromSeq, never below 1, so that seal was the one a full
	// verification never checked: editing its figures produced a clean bill of
	// health from the very call an auditor would rely on most. Having no
	// crossable boundary, it is checked by re-deriving its own fields — the same
	// check the period-scoped path makes on an empty range.
	//
	// Only when the range starts at 1. A caller who asked about 10..20 is not
	// asking about the chain's opening prefix.
	if fromSeq == 1 {
		genesis, err := s.zeroBoundarySeals(ctx)
		if err != nil {
			return nil, err
		}
		for i := range genesis {
			seal := &genesis[i]
			if ok, reason := s.VerifySealIntegrity(seal); !ok {
				// The seal has no boundary entry to point at, so name the entry
				// that recorded it — a real, fetchable sequence an investigator
				// can start from.
				at := seal.AuditSequence
				result.OK = false
				result.Violation = models.ChainViolationHashMismatch
				result.AtSequence = &at
				result.Detail = reason
				return result, nil
			}
			// Reported so the caller can tell the seal was checked rather than
			// skipped, which is the whole distinction this fix restores.
			result.SealsCrossed = append(result.SealsCrossed, seal.PeriodID)
		}
	}

	if fromSeq > toSeq {
		// A genuinely empty range is intact by definition.
		return result, nil
	}
	if head == 0 {
		// An empty journal is intact — but only as an answer to "verify whatever
		// is there". A caller who explicitly asked for entries that are now
		// absent must be told they are missing, not handed a clean bill of
		// health: a journal truncated to nothing is the most complete tampering
		// there is, and it would otherwise verify best of all.
		if requestedTo > 0 || fromSeq > 1 {
			missing := fromSeq
			result.OK = false
			result.Violation = models.ChainViolationSequenceGap
			result.AtSequence = &missing
			result.Detail = fmt.Sprintf(
				"the journal is empty but sequences %d..%d were requested: every entry in that range is missing",
				fromSeq, toSeq)
			return result, nil
		}
		result.LastSequence = head
		return result, nil
	}

	// Starting mid-chain needs the preceding entry's hash to check the first
	// link. Absent, the first link cannot be verified and we say so rather than
	// skipping the check silently.
	var expectedPrev []byte
	if fromSeq > 1 {
		prev, err := s.GetAuditEntry(ctx, fromSeq-1)
		switch {
		case err == nil:
			expectedPrev = prev.Hash
		case errors.Is(err, ErrNotFound):
			// In an intact chain the predecessor of any sequence above 1 exists.
			// Its absence is a deletion, and reporting it beats silently skipping
			// the first link check and returning OK for a chain that is broken at
			// exactly the boundary the caller asked about.
			missing := fromSeq - 1
			result.OK = false
			result.Violation = models.ChainViolationSequenceGap
			result.AtSequence = &missing
			result.Detail = fmt.Sprintf(
				"sequence %d is missing, so the first link of the requested range cannot be verified: an entry was removed",
				missing)
			return result, nil
		default:
			return nil, err
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

// zeroBoundarySeals returns the seals that close at sequence 0 — a period sealed
// while the journal was still empty. They are invisible to the walk, which
// re-derives a seal only when it reaches the entry at its boundary, and there is
// no entry at sequence 0.
func (s *Storage) zeroBoundarySeals(ctx context.Context) ([]models.PeriodSeal, error) {
	var seals []models.PeriodSeal
	if err := s.db.NewSelect().Model(&seals).
		Where("last_sequence = 0").
		Order("period_id ASC").Scan(ctx); err != nil {
		return nil, e("list zero-boundary seals", err)
	}
	return seals, nil
}

// verifySealAgainstHead re-derives a seal's sealing hash from its own stored
// fields plus the chain head it claims to close. A seal whose fields were edited,
// or that was moved onto a different head, fails here.
func verifySealAgainstHead(seal *models.PeriodSeal, headHash []byte) error {
	recomputed := audit.ComputeSealingHash(audit.SealInputFor(seal).WithHead(headHash))
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
