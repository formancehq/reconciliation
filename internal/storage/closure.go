package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/uptrace/bun"

	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
)

// ErrNoOpenClosure means the journal has no closure accepting entries. It should
// be impossible after boot, since InitClosures opens one, and is reported rather
// than repaired here: silently opening a closure mid-write would give it a
// FirstSequence in the middle of the journal and leave the entries before it
// covered by nothing.
var ErrNoOpenClosure = errors.New("no open closure")

// CloseClosureInput is a request to close the current closure. It carries no
// period id, and that absence is the design: a seal used to derive its range
// from a label the caller typed, which is what made ordering mistakes possible
// and permanent. There is one open closure, and closing closes it.
type CloseClosureInput struct {
	ClosedBy models.Subject
	At       time.Time
}

// InitClosures opens the first closure if none is open.
//
// Called once from the storage module's start hook, after the migration check
// and before anything serves traffic. FirstSequence continues from the journal
// head rather than starting at 1, so an installation that already has entries —
// one that ran before closures existed — gets a closure covering what comes
// next rather than one falsely claiming the entries before it.
func (s *Storage) InitClosures(ctx context.Context) error {
	_, err := s.CurrentClosure(ctx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNoOpenClosure) {
		return err
	}

	head, _, err := s.chainHeadLocked(ctx)
	if err != nil {
		return err
	}
	closure := &models.Closure{
		Status:        models.ClosureOpen,
		OpenedAt:      time.Now().UTC().Truncate(time.Microsecond),
		FirstSequence: head + 1,
	}
	if _, err := s.db.NewInsert().Model(closure).Exec(ctx); err != nil {
		// Two nodes booting together both see no open closure. The partial unique
		// index makes the loser's insert fail rather than produce a second open
		// closure, and losing that race is success: a closure is open.
		if errors.Is(err, ErrDuplicateKeyValue) {
			return nil
		}
		return e("open first closure", err)
	}
	return nil
}

// CurrentClosure returns the closure accepting entries.
func (s *Storage) CurrentClosure(ctx context.Context) (*models.Closure, error) {
	var closure models.Closure
	err := s.db.NewSelect().Model(&closure).
		Where("status = ?", models.ClosureOpen).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoOpenClosure
	}
	if err != nil {
		return nil, e("read current closure", err)
	}
	return &closure, nil
}

// CloseCurrentClosure seals the open closure and opens its successor.
//
// Order matters and is the mechanism:
//
//  1. take the chain lock, freezing the head for the rest of the transaction;
//  2. the range runs from the closure's own FirstSequence — recorded when it was
//     opened, not derived here — to that frozen head;
//  3. break the range down by business period, and hash the breakdown;
//  4. compute the closing hash and sign it;
//  5. append the closing to the chain, so the act is itself audited;
//  6. mark the closure closed, freeze the periods that have ended, and open the
//     successor in the same transaction.
//
// Step 6 is what removes the hazard a period seal had. A seal froze a period and
// left the next write with nowhere to go; the successor here exists before the
// transaction commits, so writes continue immediately.
func (s *Storage) CloseCurrentClosure(ctx context.Context, in CloseClosureInput) (*models.Closure, error) {
	if s.chain == nil {
		return nil, ErrAuditChainNotConfigured
	}
	if _, ok := s.db.(bun.Tx); !ok {
		return nil, fmt.Errorf("%w (closing the journal)", ErrAuditAppendOutsideTx)
	}
	if err := s.lockAuditChain(ctx); err != nil {
		return nil, err
	}

	closure, err := s.CurrentClosure(ctx)
	if err != nil {
		return nil, err
	}

	headSeq, headHash, err := s.chainHeadLocked(ctx)
	if err != nil {
		return nil, err
	}

	at := in.At
	if at.IsZero() {
		at = time.Now()
	}
	// Truncated before it is either hashed or stored, so the two are the same
	// value. timestamptz keeps microseconds while time.Now() on Linux carries
	// nanoseconds, and hashing the un-truncated reading would make every closure
	// verify as tampered in production while passing on macOS.
	at = at.UTC().Truncate(time.Microsecond)

	// An empty closure (last = first - 1) is legal and worth attesting: "we ran
	// the controls and nothing happened" is an audit answer.
	last := headSeq
	if last < closure.FirstSequence-1 {
		last = closure.FirstSequence - 1
	}

	periods, entryCount, err := s.breakDownByPeriod(ctx, closure.FirstSequence, last, at)
	if err != nil {
		return nil, err
	}

	stateHasher := audit.NewClosureStateHasher()
	frozen := make([]string, 0, len(periods))
	for i := range periods {
		stateHasher.Add(periods[i])
		if periods[i].Frozen {
			frozen = append(frozen, periods[i].PeriodID)
		}
	}

	closure.Status = models.ClosureClosed
	closure.ClosedAt = &at
	closure.LastSequence = &last
	closure.EntryCount = entryCount
	closure.LastAuditHash = headHash
	closure.Periods = periods
	closure.StateHash = stateHasher.Sum()
	closure.ClosedBy = audit.NormalizeSubject(in.ClosedBy)
	closure.SealingHash = audit.ComputeClosureHash(audit.ClosureInputFor(closure))

	if key := s.signingKey; key.CanSign() {
		signature, err := key.Sign(closure.SealingHash)
		if err != nil {
			return nil, err
		}
		closure.Signature = signature
		closure.SigningKeyID = key.ID
	}

	memento, err := audit.BuildMemento(audit.ClosureMemento{
		ClosureID:     closure.ID,
		FirstSequence: closure.FirstSequence,
		LastSequence:  last,
		EntryCount:    closure.EntryCount,
		PeriodCount:   len(periods),
		FrozenPeriods: frozen,
		StateHash:     hexOf(closure.StateHash),
		SealingHash:   hexOf(closure.SealingHash),
		SigningKeyID:  closure.SigningKeyID,
	})
	if err != nil {
		return nil, err
	}

	entry, err := s.AppendAuditEntry(ctx, AppendAuditInput{
		At:      at,
		Kind:    models.AuditClosureSealed,
		Subject: in.ClosedBy,
		Memento: memento,
	})
	if err != nil {
		return nil, err
	}
	closure.AuditSequence = &entry.Sequence

	if _, err := s.db.NewUpdate().Model(closure).WherePK().
		Where("status = ?", models.ClosureOpen).Exec(ctx); err != nil {
		return nil, e("store closure", err)
	}

	for _, periodID := range frozen {
		row := &models.FrozenPeriod{PeriodID: periodID, ClosureID: closure.ID, FrozenAt: at}
		if _, err := s.db.NewInsert().Model(row).
			On("CONFLICT (period_id) DO NOTHING").Exec(ctx); err != nil {
			return nil, e("freeze period", err)
		}
	}

	// The successor, in the same transaction. Its first sequence is one past the
	// closing entry we just appended, so the partition has no gap and no overlap.
	successor := &models.Closure{
		Status:        models.ClosureOpen,
		OpenedAt:      at,
		FirstSequence: entry.Sequence + 1,
	}
	if _, err := s.db.NewInsert().Model(successor).Exec(ctx); err != nil {
		return nil, e("open successor closure", err)
	}

	return closure, nil
}

// breakDownByPeriod groups a closure's range by business period.
//
// This is the half a purely operational boundary cannot supply. A period id is
// derived from the evaluation's point-in-time rather than from the wall clock,
// so a backfill of May run in August belongs to May — and only the label knows
// that. Entries belonging to no period, such as rule.created, are counted in the
// closure's total but have no period row.
func (s *Storage) breakDownByPeriod(
	ctx context.Context, first, last int64, at time.Time,
) ([]models.ClosurePeriod, int64, error) {
	if last < first {
		return []models.ClosurePeriod{}, 0, nil
	}

	var rows []struct {
		PeriodID string `bun:"period_id"`
		Count    int64  `bun:"count"`
	}
	if err := s.db.NewSelect().Model((*models.AuditEntry)(nil)).
		ColumnExpr("coalesce(period_id, '') AS period_id").
		ColumnExpr("count(*) AS count").
		Where("sequence >= ?", first).Where("sequence <= ?", last).
		GroupExpr("coalesce(period_id, '')").
		Scan(ctx, &rows); err != nil {
		return nil, 0, e("group closure entries by period", err)
	}

	var total int64
	periods := make([]models.ClosurePeriod, 0, len(rows))
	for _, row := range rows {
		total += row.Count
		if row.PeriodID == "" {
			continue
		}
		stateHash, alertCount, unresolved, err := s.hashPeriodState(ctx, row.PeriodID)
		if err != nil {
			return nil, 0, err
		}
		// A period is frozen only once it is over. A closure that runs mid-period
		// attests what it saw and leaves the period open, which is why closing no
		// longer needs a guard against closing something still live.
		ended := periodHasEnded(row.PeriodID, at)
		periods = append(periods, models.ClosurePeriod{
			PeriodID:        row.PeriodID,
			EntryCount:      row.Count,
			AlertCount:      alertCount,
			UnresolvedCount: unresolved,
			StateHash:       hexOf(stateHash),
			Ended:           ended,
			Frozen:          ended,
		})
	}

	// Sorted explicitly. The digest is taken over this sequence, and a hash over a
	// set is only meaningful if the set has an agreed order — never one the query
	// planner happens to return, which is the determinism trap in Ledger V2's
	// block hasher.
	sort.Slice(periods, func(i, j int) bool { return periods[i].PeriodID < periods[j].PeriodID })
	return periods, total, nil
}

// ErrPeriodSealed is returned when a write targets a period whose books are
// closed. It is the closing barrier an auditor looks for: after the books are
// closed, the books do not move.
var ErrPeriodSealed = errors.New("period is sealed")

// hashPeriodState folds the period's alerts into a digest, in an order the query
// states explicitly. Evidence is not included: it is already bound by the
// evaluation entries inside the sealed range, and rehashing it here would double
// the cost of a seal for no extra guarantee.
func (s *Storage) hashPeriodState(ctx context.Context, periodID string) ([]byte, int64, int64, error) {
	var alerts []models.Alert
	if err := s.db.NewSelect().Model(&alerts).
		Where("period_id = ?", periodID).
		Order("rule_id ASC", "fingerprint ASC").Scan(ctx); err != nil {
		return nil, 0, 0, e("scan period alerts for seal", err)
	}

	hasher := audit.NewStateHasher()
	var unresolved int64
	for i := range alerts {
		a := &alerts[i]
		kind := ""
		if a.Resolution != nil {
			kind = string(a.Resolution.Kind)
		}
		if a.Status != models.AlertResolved {
			unresolved++
		}
		hasher.AddAlert(audit.AlertState{
			ID:              a.ID,
			RuleID:          a.RuleID,
			Fingerprint:     a.Fingerprint,
			Status:          a.Status,
			Severity:        a.Severity,
			OccurrenceCount: a.OccurrenceCount,
			ResolutionKind:  kind,
		})
	}
	return hasher.Sum(), int64(len(alerts)), unresolved, nil
}

// periodHasEnded reports whether a period's calendar span is over.
//
// The continuous pseudo-period never ends by definition, so a continuous rule's
// alerts are never frozen by a closure — which is what lets live monitoring run
// across closings untouched.
func periodHasEnded(periodID string, at time.Time) bool {
	end, ok := models.PeriodEnd(periodID)
	if !ok {
		return false
	}
	return !at.Before(end)
}

// IsPeriodFrozen reports whether a business period has stopped accepting writes.
// This is the closing barrier an auditor looks for: after the books are closed,
// the books do not move.
func (s *Storage) IsPeriodFrozen(ctx context.Context, periodID string) (bool, error) {
	if periodID == "" || periodID == models.ContinuousPeriod {
		return false, nil
	}
	count, err := s.db.NewSelect().Model((*models.FrozenPeriod)(nil)).
		Where("period_id = ?", periodID).Count(ctx)
	if err != nil {
		return false, e("check frozen period", err)
	}
	return count > 0, nil
}

// GetClosure returns one closure by id.
func (s *Storage) GetClosure(ctx context.Context, id int64) (*models.Closure, error) {
	var closure models.Closure
	if err := s.db.NewSelect().Model(&closure).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, e("get closure", err)
	}
	return &closure, nil
}

// ListClosures returns every closure, most recent first.
func (s *Storage) ListClosures(ctx context.Context) ([]models.Closure, error) {
	var closures []models.Closure
	if err := s.db.NewSelect().Model(&closures).Order("id DESC").Scan(ctx); err != nil {
		return nil, e("list closures", err)
	}
	return closures, nil
}

// PeriodAttestation is what an auditor asking about a business period gets back:
// the period's signed figures, and the closure that attested them.
type PeriodAttestation struct {
	Period  models.ClosurePeriod
	Closure *models.Closure
}

// AttestationsForPeriod returns every closed closure that observed a business
// period, with that period's figures.
//
// Usually one. More than one means the period's evidence was recorded across
// several closings — a period spanning a closure boundary, or a backfill landing
// long after the fact — and an auditor needs to see all of them rather than the
// most convenient one.
func (s *Storage) AttestationsForPeriod(ctx context.Context, periodID string) ([]PeriodAttestation, error) {
	var closures []models.Closure
	if err := s.db.NewSelect().Model(&closures).
		Where("status = ?", models.ClosureClosed).
		Where("periods @> ?", fmt.Sprintf(`[{"periodID":%q}]`, periodID)).
		Order("id ASC").Scan(ctx); err != nil {
		return nil, e("find closures for period", err)
	}

	out := make([]PeriodAttestation, 0, len(closures))
	for i := range closures {
		for _, p := range closures[i].Periods {
			if p.PeriodID == periodID {
				out = append(out, PeriodAttestation{Period: p, Closure: &closures[i]})
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

// closuresInRange returns the closed closures whose boundary falls inside a
// sequence range, so a chain walk can re-derive each one as it crosses it.
func (s *Storage) closuresInRange(ctx context.Context, fromSeq, toSeq int64) ([]models.Closure, error) {
	var closures []models.Closure
	if err := s.db.NewSelect().Model(&closures).
		Where("status = ?", models.ClosureClosed).
		Where("last_sequence >= ?", fromSeq).
		Where("last_sequence <= ?", toSeq).
		Order("last_sequence ASC").Scan(ctx); err != nil {
		return nil, e("list closures in range", err)
	}
	return closures, nil
}

// zeroBoundaryClosures returns closed closures whose boundary is below sequence
// 1 — a closure that closed while the journal was still empty. The walk cannot
// reach them, because it re-derives a closure only on arriving at the entry at
// its boundary, and there is no entry at sequence 0.
func (s *Storage) zeroBoundaryClosures(ctx context.Context) ([]models.Closure, error) {
	var closures []models.Closure
	if err := s.db.NewSelect().Model(&closures).
		Where("status = ?", models.ClosureClosed).
		Where("last_sequence < 1").
		Order("id ASC").Scan(ctx); err != nil {
		return nil, e("list zero-boundary closures", err)
	}
	return closures, nil
}

// maxClosedSequence is the highest boundary any closure committed to.
func (s *Storage) maxClosedSequence(ctx context.Context) (int64, error) {
	var max int64
	if err := s.db.NewSelect().Model((*models.Closure)(nil)).
		ColumnExpr("coalesce(max(last_sequence), 0)").
		Where("status = ?", models.ClosureClosed).Scan(ctx, &max); err != nil {
		return 0, e("read highest closed sequence", err)
	}
	return max, nil
}

// VerifyClosureIntegrity re-derives a closure's hash from its own stored fields
// and reports whether it still matches — no signature involved.
//
// Separate from the signature check because the two answer different questions:
// an installation with no signing key produces unsigned closures by design, so
// asking about the signature there is meaningless, whereas asking whether the
// numbers still reproduce the hash always is.
func (s *Storage) VerifyClosureIntegrity(closure *models.Closure) (bool, string) {
	if closure.Status != models.ClosureClosed {
		return false, "this closure is still open and has nothing to verify yet"
	}
	if !closureStateMatches(closure) {
		return false, fmt.Sprintf(
			"closure %d no longer reproduces its own state hash: its per-period figures were altered",
			closure.ID)
	}
	recomputed := audit.ComputeClosureHash(audit.ClosureInputFor(closure))
	if hexOf(recomputed) != hexOf(closure.SealingHash) {
		return false, fmt.Sprintf(
			"closure %d no longer reproduces its own sealing hash: its recorded fields were altered",
			closure.ID)
	}
	return true, ""
}

// closureStateMatches re-derives the state hash from the stored breakdown.
//
// Needed because the breakdown enters the sealing hash only through this digest:
// without re-deriving it, editing a period's alert count inside the jsonb would
// leave the sealing hash reproducing perfectly. That is the same gap that let an
// edited count read "0 still open" while verification answered intact.
func closureStateMatches(closure *models.Closure) bool {
	hasher := audit.NewClosureStateHasher()
	periods := make([]models.ClosurePeriod, len(closure.Periods))
	copy(periods, closure.Periods)
	sort.Slice(periods, func(i, j int) bool { return periods[i].PeriodID < periods[j].PeriodID })
	for i := range periods {
		hasher.Add(periods[i])
	}
	return hexOf(hasher.Sum()) == hexOf(closure.StateHash)
}

// VerifyClosureSignature re-derives a closure's hash and checks its signature
// against the key that signed it, including a retired one. This is what an
// auditor does offline; the endpoint exists so they can confirm their own
// tooling agrees with ours before trusting it.
func (s *Storage) VerifyClosureSignature(ctx context.Context, closure *models.Closure) (bool, string, error) {
	if ok, reason := s.VerifyClosureIntegrity(closure); !ok {
		return false, reason, nil
	}
	if len(closure.Signature) == 0 {
		// Two very different situations, and an auditor needs to tell them apart,
		// because only the second is an incident.
		if closure.SigningKeyID != "" {
			return false, fmt.Sprintf(
				"the signature was removed: closure %d names signing key %q but carries none",
				closure.ID, closure.SigningKeyID), nil
		}
		return false, "this closure was recorded without a signature because no signing key was available at the time; " +
			"it is not evidence of tampering, but it cannot be verified by a third party either", nil
	}

	var row auditSigningKeyRow
	err := s.db.NewSelect().Model(&row).Where("key_id = ?", closure.SigningKeyID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Sprintf("signing key %q is not known to this installation", closure.SigningKeyID), nil
	}
	if err != nil {
		return false, "", e("load signing key for closure verification", err)
	}
	if !audit.SigningKeyFromPublic(row.PublicKey).Verify(closure.SealingHash, closure.Signature) {
		return false, "the signature does not verify against the recorded key", nil
	}
	return true, "", nil
}

// GetClosingSchedule returns the cron rotating closures, or an empty string when
// rotation is manual.
func (s *Storage) GetClosingSchedule(ctx context.Context) (string, error) {
	var row models.ClosingSchedule
	err := s.db.NewSelect().Model(&row).Where("singleton").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", e("read closing schedule", err)
	}
	return row.Cron, nil
}

// SetClosingSchedule stores the rotation cron. An empty expression disables
// automatic rotation without deleting the history of having had one.
func (s *Storage) SetClosingSchedule(ctx context.Context, cron string) error {
	row := &models.ClosingSchedule{
		Singleton: true,
		Cron:      cron,
		UpdatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	if _, err := s.db.NewInsert().Model(row).
		On("CONFLICT (singleton) DO UPDATE").
		Set("cron = EXCLUDED.cron, updated_at = EXCLUDED.updated_at").
		Exec(ctx); err != nil {
		return e("store closing schedule", err)
	}
	return nil
}
