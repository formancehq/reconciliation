package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
)

// ErrPeriodAlreadySealed is returned when a period has already been closed.
// Sealing is not idempotent on purpose: a second seal would either contradict
// the first or silently do nothing, and both are worse than an error.
var ErrPeriodAlreadySealed = errors.New("period already sealed")

// ErrPeriodNotSealable is returned for a period that has no end — the
// "continuous" pseudo-period of a live-monitoring rule. Sealing it would freeze
// every continuous rule permanently, since its alerts would all fall behind the
// write barrier with no successor period to move to.
var ErrPeriodNotSealable = errors.New("period cannot be sealed")

// ErrPeriodSealed is returned when a write targets an already-sealed period. It
// is the closing barrier an auditor looks for: after the books are closed, the
// books do not move.
var ErrPeriodSealed = errors.New("period is sealed")

// SealPeriodInput is a request to close a period.
type SealPeriodInput struct {
	PeriodID string
	SealedBy models.Subject
	At       time.Time
}

// SealPeriod closes a contiguous range of the journal under a period label.
//
// Order matters and is the mechanism:
//
//  1. take the chain lock, freezing the head for the rest of the transaction;
//  2. the range runs from the previous seal's end to that frozen head;
//  3. hash the period's derived alert state through a deterministic ordered
//     scan — never an aggregate whose input order is merely conventional, which
//     is the determinism trap in the Ledger V2 block hasher;
//  4. compute the sealing hash and sign it;
//  5. append the seal to the chain, so the act of sealing is itself audited;
//  6. store the seal row, pointing at that entry.
//
// The seal's own chain entry lands at head+1 and therefore belongs to the next
// period, exactly as a Ledger chapter's SealChapter order is proposed after the
// close boundary it describes.
func (s *Storage) SealPeriod(ctx context.Context, in SealPeriodInput) (*models.PeriodSeal, error) {
	if s.chain == nil {
		return nil, ErrAuditChainNotConfigured
	}
	if _, ok := s.db.(bun.Tx); !ok {
		return nil, fmt.Errorf("%w (sealing period %s)", ErrAuditAppendOutsideTx, in.PeriodID)
	}
	if in.PeriodID == "" {
		return nil, fmt.Errorf("seal period: empty period id")
	}
	if !models.ValidPeriodID(in.PeriodID) && in.PeriodID != string(models.CadenceContinuous) {
		return nil, fmt.Errorf(
			"%w: %q is not a period any cadence produces (expected 2026-05, 2026-W12 or 2026-05-15). "+
				"Sealing it would advance the boundary and permanently consume the range belonging to the period you meant, "+
				"and neither seal could be corrected afterwards",
			ErrPeriodNotSealable, in.PeriodID)
	}
	if in.PeriodID == string(models.CadenceContinuous) {
		return nil, fmt.Errorf(
			"%w: %q has no end — a continuous rule's alerts would be frozen with no successor period to move to",
			ErrPeriodNotSealable, in.PeriodID)
	}

	// Lock first, THEN check whether the period is already sealed. Checking first
	// lets two concurrent seals both pass, and the loser's insert then fails on
	// the primary key — surfacing a double-click as a 500 instead of the conflict
	// it is.
	if err := s.lockAuditChain(ctx); err != nil {
		return nil, err
	}

	existing, err := s.GetPeriodSeal(ctx, in.PeriodID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("%w: %s was sealed at %s", ErrPeriodAlreadySealed, in.PeriodID, existing.SealedAt.Format(time.RFC3339))
	}

	// Refuse to go backwards. A seal's range always continues from the previous
	// seal's end, so sealing a period that starts before the last sealed one hands
	// it the entries that came *after* that seal — 2026-06 sealed after 2026-08
	// gets August's closing entry, and June's own entries end up attested by
	// August. Neither seal can be corrected afterwards, so the only place to catch
	// it is before the range is consumed.
	//
	// The candidate's start is compared against the closed calendar's end, not
	// against the last sealed period's start. Start-versus-start leaves a hole:
	// 2026-08-15 begins after 2026-08 does, so a day nested inside an
	// already-closed month would pass and attest calendar that is already sealed.
	if closedThrough, err := s.sealedCalendarEnd(ctx); err != nil {
		return nil, err
	} else if !closedThrough.IsZero() {
		start, ok := models.PeriodStart(in.PeriodID)
		if ok && start.Before(closedThrough) {
			return nil, fmt.Errorf(
				"%w: %q begins at %s, but the books are closed through %s. "+
					"Sealing continues the range from the last seal, so this period would be given the entries "+
					"recorded after that seal, and neither seal could be corrected afterwards",
				ErrPeriodNotSealable, in.PeriodID,
				start.Format("2006-01-02"), closedThrough.Format("2006-01-02"))
		}
	}

	headSeq, headHash, err := s.chainHeadLocked(ctx)
	if err != nil {
		return nil, err
	}

	var prevLast int64
	if err := s.db.NewSelect().Model((*models.PeriodSeal)(nil)).
		ColumnExpr("coalesce(max(last_sequence), 0)").Scan(ctx, &prevLast); err != nil {
		return nil, e("read previous seal boundary", err)
	}

	first := prevLast + 1

	// The mirror of the backwards check, and the case it did not cover: skipping
	// forward. With no seal yet there is no closed calendar to compare against, so
	// sealing 2026-06 while the journal already held unsealed 2026-05 entries
	// succeeded, swallowed them into June's range, and then left May permanently
	// unsealable — the backwards check refuses it from that point on. Reproduced
	// before fixing.
	//
	// So the earliest period still awaiting a seal has to be sealed first. Periods
	// already sealed are excluded, which is what keeps the ordinary flow working:
	// a range legitimately contains the *previous* period's seal entry, tagged with
	// that earlier period, and refusing on it would make every seal after the first
	// impossible.
	earliest, earliestID, err := s.earliestUnsealedPeriod(ctx, first)
	if err != nil {
		return nil, err
	}
	if !earliest.IsZero() {
		if start, ok := models.PeriodStart(in.PeriodID); ok && start.After(earliest) {
			return nil, fmt.Errorf(
				"%w: %q cannot be sealed while %q is still open and holds earlier unsealed entries. "+
					"A seal takes the whole journal since the last one, so sealing %q now would give it %q's "+
					"entries and leave %q unsealable for good. Seal %q first",
				ErrPeriodNotSealable, in.PeriodID, earliestID,
				in.PeriodID, earliestID, earliestID, earliestID)
		}
	}

	// An empty range (last = first - 1) is legal: a quiet period is still worth
	// attesting to, and "we ran the controls and nothing happened" is an audit
	// answer.
	last := headSeq
	if last < first-1 {
		last = first - 1
	}

	var entryCount int64
	if last >= first {
		count, err := s.db.NewSelect().Model((*models.AuditEntry)(nil)).
			Where("sequence >= ?", first).Where("sequence <= ?", last).Count(ctx)
		if err != nil {
			return nil, e("count sealed entries", err)
		}
		entryCount = int64(count)
	}

	stateHash, alertCount, unresolved, err := s.hashPeriodState(ctx, in.PeriodID)
	if err != nil {
		return nil, err
	}

	at := in.At
	if at.IsZero() {
		at = time.Now()
	}
	// Truncated to microseconds here, before it is either hashed or stored, so
	// the two are the same value. timestamptz keeps microseconds while time.Now()
	// on Linux carries nanoseconds, and hashing the un-truncated reading would
	// make every seal verify as tampered in production while passing on macOS —
	// the same trap 8dadf43b fixed for chain entries.
	at = at.UTC().Truncate(time.Microsecond)

	// Build the seal first and hash the struct that is about to be stored, through
	// the same SealInputFor every verification path uses. Hashing a separate,
	// hand-listed copy of these fields is how signing and verification drift
	// apart: whatever is committed here is by construction what gets re-derived.
	seal := &models.PeriodSeal{
		PeriodID:        in.PeriodID,
		FirstSequence:   first,
		LastSequence:    last,
		EntryCount:      entryCount,
		LastAuditHash:   headHash,
		StateHash:       stateHash,
		SealedBy:        audit.NormalizeSubject(in.SealedBy),
		AlertCount:      alertCount,
		UnresolvedCount: unresolved,
		SealedAt:        at,
	}
	seal.SealingHash = audit.ComputeSealingHash(audit.SealInputFor(seal))

	if key := s.signingKey; key.CanSign() {
		signature, err := key.Sign(seal.SealingHash)
		if err != nil {
			return nil, err
		}
		seal.Signature = signature
		seal.SigningKeyID = key.ID
	}

	memento, err := audit.BuildMemento(audit.PeriodSealMemento{
		PeriodID:        seal.PeriodID,
		FirstSequence:   seal.FirstSequence,
		LastSequence:    seal.LastSequence,
		EntryCount:      seal.EntryCount,
		AlertCount:      seal.AlertCount,
		UnresolvedCount: seal.UnresolvedCount,
		StateHash:       hexOf(seal.StateHash),
		SealingHash:     hexOf(seal.SealingHash),
		SigningKeyID:    seal.SigningKeyID,
	})
	if err != nil {
		return nil, err
	}

	entry, err := s.AppendAuditEntry(ctx, AppendAuditInput{
		At:       at,
		Kind:     models.AuditPeriodSealed,
		PeriodID: in.PeriodID,
		Subject:  in.SealedBy,
		Memento:  memento,
	})
	if err != nil {
		return nil, err
	}

	seal.AuditSequence = entry.Sequence
	if _, err := s.db.NewInsert().Model(seal).Returning("*").Exec(ctx); err != nil {
		// Belt and braces behind the lock: if a duplicate ever reaches the insert,
		// report the conflict rather than an internal error.
		if errors.Is(err, ErrDuplicateKeyValue) {
			return nil, fmt.Errorf("%w: %s", ErrPeriodAlreadySealed, in.PeriodID)
		}
		return nil, e("store period seal", err)
	}
	return seal, nil
}

// earliestUnsealedPeriod returns the start instant and id of the earliest period
// represented in the journal from fromSeq onwards that has not been sealed yet,
// or the zero time when there is none.
//
// Periods with an existing seal are excluded on purpose. A seal's range
// legitimately contains the previous period's seal entry — that entry carries the
// earlier period's id — so counting it would report an already-closed period as
// still awaiting one and refuse every seal after the first.
//
// Ids no cadence orders (continuous, and the empty id on entries that belong to no
// period, such as rule.created) are skipped: they can never be sealed, so treating
// them as blocking would mean nothing could ever be sealed.
func (s *Storage) earliestUnsealedPeriod(ctx context.Context, fromSeq int64) (time.Time, string, error) {
	var rows []struct {
		PeriodID string `bun:"period_id"`
	}
	if err := s.db.NewSelect().
		Model((*models.AuditEntry)(nil)).
		ColumnExpr("DISTINCT period_id").
		Where("sequence >= ?", fromSeq).
		Where("period_id <> ''").
		Where("period_id NOT IN (SELECT period_id FROM reconciliations.period_seal)").
		Scan(ctx, &rows); err != nil {
		return time.Time{}, "", e("read unsealed journal periods", err)
	}

	var (
		earliest time.Time
		id       string
	)
	for _, row := range rows {
		start, ok := models.PeriodStart(row.PeriodID)
		if !ok {
			continue
		}
		if earliest.IsZero() || start.Before(earliest) {
			earliest, id = start, row.PeriodID
		}
	}
	return earliest, id, nil
}

// sealedCalendarEnd returns the instant the closed books run through — the
// latest end of any sealed period — or the zero time when nothing is sealed yet.
//
// Derived from the period ids rather than from sealed_at: what matters is which
// stretch of calendar is closed, not when someone ran the closing. An operator
// sealing last month today must still be refused if next month is already
// sealed.
func (s *Storage) sealedCalendarEnd(ctx context.Context) (time.Time, error) {
	var seals []models.PeriodSeal
	if err := s.db.NewSelect().Model(&seals).Column("period_id").Scan(ctx); err != nil {
		return time.Time{}, e("read sealed period ids", err)
	}
	var latest time.Time
	for _, seal := range seals {
		end, ok := models.PeriodEnd(seal.PeriodID)
		if !ok {
			// A label no cadence produces cannot be ordered. Ids are validated at
			// seal time, so this only happens for rows predating that check;
			// skipping is right — an unorderable seal must not block every future
			// one.
			continue
		}
		if end.After(latest) {
			latest = end
		}
	}
	return latest, nil
}

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

// GetPeriodSeal returns a period's seal, or ErrNotFound when the period is still
// open. Absence is the open state — there is no row to mean "not sealed yet".
func (s *Storage) GetPeriodSeal(ctx context.Context, periodID string) (*models.PeriodSeal, error) {
	var seal models.PeriodSeal
	err := s.db.NewSelect().Model(&seal).Where("period_id = ?", periodID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, e("get period seal", err)
	}
	if err != nil {
		return nil, e("get period seal", err)
	}
	return &seal, nil
}

// ListPeriodSeals returns every seal, most recently sealed first.
func (s *Storage) ListPeriodSeals(ctx context.Context) ([]models.PeriodSeal, error) {
	var seals []models.PeriodSeal
	if err := s.db.NewSelect().Model(&seals).Order("last_sequence DESC").Scan(ctx); err != nil {
		return nil, e("list period seals", err)
	}
	return seals, nil
}

// sealsInRange returns the seals whose boundary falls inside a sequence range, so
// a chain walk can re-derive each one as it crosses it.
func (s *Storage) sealsInRange(ctx context.Context, fromSeq, toSeq int64) ([]models.PeriodSeal, error) {
	var seals []models.PeriodSeal
	if err := s.db.NewSelect().Model(&seals).
		Where("last_sequence >= ?", fromSeq).
		Where("last_sequence <= ?", toSeq).
		Order("last_sequence ASC").Scan(ctx); err != nil {
		return nil, e("list seals in range", err)
	}
	return seals, nil
}

// IsPeriodSealed reports whether a period is closed.
func (s *Storage) IsPeriodSealed(ctx context.Context, periodID string) (bool, error) {
	if periodID == "" {
		return false, nil
	}
	count, err := s.db.NewSelect().Model((*models.PeriodSeal)(nil)).
		Where("period_id = ?", periodID).Count(ctx)
	if err != nil {
		return false, e("check period seal", err)
	}
	return count > 0, nil
}

// VerifySealIntegrity re-derives a seal's sealing hash from its own stored fields
// and reports whether it still matches — no signature involved.
//
// Separate from VerifySealSignature because the two answer different questions. An
// installation with no signing key produces unsigned seals by design, so asking
// about the signature there is meaningless; asking whether the seal's own numbers
// still reproduce its hash is always meaningful. This is what a chain walk needs
// when a sealed range contains no entries to walk: there is nothing to recompute,
// but the seal itself can still have been edited.
func (s *Storage) VerifySealIntegrity(seal *models.PeriodSeal) (bool, string) {
	recomputed := audit.ComputeSealingHash(audit.SealInputFor(seal))
	if hexOf(recomputed) != hexOf(seal.SealingHash) {
		return false, fmt.Sprintf(
			"the seal for period %q no longer reproduces its own sealing hash: its recorded fields were altered",
			seal.PeriodID)
	}
	return true, ""
}

// VerifySealSignature re-derives a seal's sealing hash and checks its signature
// against the key that signed it, including a retired one.
//
// This is what an auditor does offline; the endpoint exists so they can confirm
// their own tooling agrees with ours before trusting it.
func (s *Storage) VerifySealSignature(ctx context.Context, seal *models.PeriodSeal) (bool, string, error) {
	recomputed := audit.ComputeSealingHash(audit.SealInputFor(seal))
	if hexOf(recomputed) != hexOf(seal.SealingHash) {
		return false, "the seal's fields do not reproduce its sealing hash", nil
	}
	if len(seal.Signature) == 0 {
		// Two very different situations, previously reported identically. An
		// auditor needs to tell "nobody was configured to sign this" from "the
		// signature was taken off", because only the second is an incident.
		if seal.SigningKeyID != "" {
			return false, fmt.Sprintf(
				"the signature was removed: this seal names signing key %q but carries none",
				seal.SigningKeyID), nil
		}
		return false, "this seal was recorded without a signature because no signing key was available at the time; " +
			"it is not evidence of tampering, but it cannot be verified by a third party either", nil
	}

	var row auditSigningKeyRow
	err := s.db.NewSelect().Model(&row).Where("key_id = ?", seal.SigningKeyID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Sprintf("signing key %q is not known to this installation", seal.SigningKeyID), nil
	}
	if err != nil {
		return false, "", e("load signing key for seal verification", err)
	}
	if !audit.SigningKeyFromPublic(row.PublicKey).Verify(seal.SealingHash, seal.Signature) {
		return false, "the signature does not verify against the recorded key", nil
	}
	return true, "", nil
}
