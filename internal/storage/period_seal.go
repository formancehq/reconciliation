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

	sealingHash := audit.ComputeSealingHash(audit.SealInput{
		PeriodID:      in.PeriodID,
		FirstSequence: first,
		LastSequence:  last,
		EntryCount:    entryCount,
		LastAuditHash: headHash,
		StateHash:     stateHash,
	})

	key := s.signingKey
	var (
		signature []byte
		keyID     string
	)
	if key.CanSign() {
		signature, err = key.Sign(sealingHash)
		if err != nil {
			return nil, err
		}
		keyID = key.ID
	}

	at := in.At
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	memento, err := audit.BuildMemento(audit.PeriodSealMemento{
		PeriodID:        in.PeriodID,
		FirstSequence:   first,
		LastSequence:    last,
		EntryCount:      entryCount,
		AlertCount:      alertCount,
		UnresolvedCount: unresolved,
		StateHash:       hexOf(stateHash),
		SealingHash:     hexOf(sealingHash),
		SigningKeyID:    keyID,
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

	seal := &models.PeriodSeal{
		PeriodID:        in.PeriodID,
		FirstSequence:   first,
		LastSequence:    last,
		EntryCount:      entryCount,
		LastAuditHash:   headHash,
		StateHash:       stateHash,
		SealingHash:     sealingHash,
		Signature:       signature,
		SigningKeyID:    keyID,
		SealedBy:        audit.NormalizeSubject(in.SealedBy),
		AlertCount:      alertCount,
		UnresolvedCount: unresolved,
		SealedAt:        at,
		AuditSequence:   entry.Sequence,
	}
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

// VerifySealSignature re-derives a seal's sealing hash and checks its signature
// against the key that signed it, including a retired one.
//
// This is what an auditor does offline; the endpoint exists so they can confirm
// their own tooling agrees with ours before trusting it.
func (s *Storage) VerifySealSignature(ctx context.Context, seal *models.PeriodSeal) (bool, string, error) {
	recomputed := audit.ComputeSealingHash(audit.SealInput{
		PeriodID:      seal.PeriodID,
		FirstSequence: seal.FirstSequence,
		LastSequence:  seal.LastSequence,
		EntryCount:    seal.EntryCount,
		LastAuditHash: seal.LastAuditHash,
		StateHash:     seal.StateHash,
	})
	if hexOf(recomputed) != hexOf(seal.SealingHash) {
		return false, "the seal's fields do not reproduce its sealing hash", nil
	}
	if len(seal.Signature) == 0 {
		return false, "the seal carries no signature", nil
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
