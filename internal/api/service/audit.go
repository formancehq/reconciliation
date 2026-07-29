package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/formancehq/reconciliation/internal/audit"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
)

// ListAuditEntries pages through the journal in chain order.
func (s *Service) ListAuditEntries(ctx context.Context, f storage.AuditEntryFilters, afterSeq int64, limit int) ([]models.AuditEntry, int64, error) {
	entries, next, err := s.store.ListAuditEntries(ctx, f, afterSeq, limit)
	if err != nil {
		return nil, 0, newStorageError(err, "listing audit entries")
	}
	return entries, next, nil
}

// GetAuditEntry returns one journal entry, memento included, so a caller can
// recompute its digest without asking us to do it for them.
func (s *Service) GetAuditEntry(ctx context.Context, sequence int64) (*models.AuditEntry, error) {
	entry, err := s.store.GetAuditEntry(ctx, sequence)
	if err != nil {
		return nil, newStorageError(err, "getting audit entry")
	}
	return entry, nil
}

// ChainHead reports where the journal currently ends.
func (s *Service) ChainHead(ctx context.Context) (int64, []byte, error) {
	seq, hash, err := s.store.ChainHead(ctx)
	if err != nil {
		return 0, nil, newStorageError(err, "reading chain head")
	}
	return seq, hash, nil
}

// VerifyChain walks a range of the journal and recomputes it.
func (s *Service) VerifyChain(ctx context.Context, fromSeq, toSeq int64) (*models.ChainVerification, error) {
	result, err := s.store.VerifyChain(ctx, fromSeq, toSeq)
	if err != nil {
		return nil, newStorageError(err, "verifying audit chain")
	}
	return result, nil
}

// ListRuleRevisions returns a control's frozen definitions.
func (s *Service) ListRuleRevisions(ctx context.Context, ruleID uuid.UUID) ([]models.RuleRevision, error) {
	revs, err := s.store.ListRuleRevisions(ctx, ruleID)
	if err != nil {
		return nil, newStorageError(err, "listing rule revisions")
	}
	return revs, nil
}

// GetRuleRevision returns one frozen definition — what the control actually was
// when a given evaluation ran.
func (s *Service) GetRuleRevision(ctx context.Context, ruleID uuid.UUID, revision int64) (*models.RuleRevision, error) {
	rev, err := s.store.GetRuleRevision(ctx, ruleID, revision)
	if err != nil {
		return nil, newStorageError(err, "getting rule revision")
	}
	return rev, nil
}

// ListPeriodSeals returns every closed period.
func (s *Service) ListPeriodSeals(ctx context.Context) ([]models.PeriodSeal, error) {
	seals, err := s.store.ListPeriodSeals(ctx)
	if err != nil {
		return nil, newStorageError(err, "listing period seals")
	}
	return seals, nil
}

// GetPeriodSeal returns a period's seal.
func (s *Service) GetPeriodSeal(ctx context.Context, periodID string) (*models.PeriodSeal, error) {
	seal, err := s.store.GetPeriodSeal(ctx, periodID)
	if err != nil {
		return nil, newStorageError(err, "getting period seal")
	}
	return seal, nil
}

// SealPeriod closes a period.
//
// The transaction is opened here rather than inside storage because sealing must
// freeze the chain head for its whole duration: the range boundary, the state
// hash and the seal's own journal entry have to agree, and they only do if
// nothing else appends in between.
//
// The operator is taken from the request's verified token, not from the payload.
// A seal is the strongest claim the system makes — "these books are closed" —
// and it would be worth considerably less if the name attached to it were
// something the caller typed.
func (s *Service) SealPeriod(ctx context.Context, periodID string) (*models.PeriodSeal, error) {
	if periodID == "" {
		return nil, fmt.Errorf("%w: period id is required", ErrValidation)
	}

	// The transactional capability, asserted rather than required on Store: only a
	// real storage can provide it, and demanding it of every implementation would
	// force the in-memory fakes to pretend.
	type transactional interface {
		RunInTx(ctx context.Context, fn func(ctx context.Context, store *storage.Storage) error) error
	}
	tx, ok := s.store.(transactional)
	if !ok {
		return nil, fmt.Errorf("sealing a period requires a transactional store")
	}

	var seal *models.PeriodSeal
	err := tx.RunInTx(ctx, func(ctx context.Context, txStore *storage.Storage) error {
		out, err := txStore.SealPeriod(ctx, storage.SealPeriodInput{
			PeriodID: periodID,
			SealedBy: audit.SubjectFrom(ctx),
		})
		if err != nil {
			return err
		}
		seal = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return seal, nil
}

// VerifySealSignature re-derives a seal's sealing hash and checks its signature.
func (s *Service) VerifySealSignature(ctx context.Context, periodID string) (*models.PeriodSeal, bool, string, error) {
	seal, err := s.store.GetPeriodSeal(ctx, periodID)
	if err != nil {
		return nil, false, "", newStorageError(err, "getting period seal")
	}
	ok, reason, err := s.store.VerifySealSignature(ctx, seal)
	if err != nil {
		return nil, false, "", newStorageError(err, "verifying seal signature")
	}
	return seal, ok, reason, nil
}

// VerifySealIntegrity re-derives a seal's own sealing hash, without regard to its
// signature.
func (s *Service) VerifySealIntegrity(ctx context.Context, periodID string) (bool, string, error) {
	seal, err := s.store.GetPeriodSeal(ctx, periodID)
	if err != nil {
		return false, "", newStorageError(err, "getting period seal")
	}
	ok, reason := s.store.VerifySealIntegrity(seal)
	return ok, reason, nil
}

// ListVerificationKeys publishes the public keys an auditor needs. Retired keys
// stay listed: a seal signed two years ago must remain checkable.
func (s *Service) ListVerificationKeys(ctx context.Context) ([]storage.VerificationKey, error) {
	keys, err := s.store.ListVerificationKeys(ctx)
	if err != nil {
		return nil, newStorageError(err, "listing verification keys")
	}
	return keys, nil
}
