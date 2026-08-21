package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

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

// ListClosures returns every closure, open one included.
func (s *Service) ListClosures(ctx context.Context) ([]models.Closure, error) {
	closures, err := s.store.ListClosures(ctx)
	if err != nil {
		return nil, newStorageError(err, "listing closures")
	}
	return closures, nil
}

// GetClosure returns one closure by id.
func (s *Service) GetClosure(ctx context.Context, id int64) (*models.Closure, error) {
	closure, err := s.store.GetClosure(ctx, id)
	if err != nil {
		return nil, newStorageError(err, "getting closure")
	}
	return closure, nil
}

// AttestationsForPeriod answers the question an auditor actually asks — "what do
// you attest for May" — by resolving the business period to the closures that
// observed it. The label stays the way in, even though it is no longer the way
// closing works.
func (s *Service) AttestationsForPeriod(ctx context.Context, periodID string) ([]storage.PeriodAttestation, error) {
	if periodID == "" {
		return nil, fmt.Errorf("%w: period id is required", ErrValidation)
	}
	out, err := s.store.AttestationsForPeriod(ctx, periodID)
	if err != nil {
		return nil, newStorageError(err, "getting attestations for period")
	}
	return out, nil
}

// CloseJournal closes the current closure and opens its successor.
//
// It takes no period id, and that is the whole point: the range is the one the
// open closure has carried since it opened, so there is nothing for a caller to
// name and therefore nothing to name wrongly.
//
// The transaction is opened here rather than inside storage because closing must
// freeze the chain head for its whole duration: the boundary, the per-period
// breakdown and the closing's own journal entry have to agree, and they only do
// if nothing else appends in between.
//
// The operator comes from the request's verified token, never from the payload.
// A closing is the strongest claim the system makes — "these books are closed" —
// and it would be worth considerably less if the name attached to it were
// something the caller typed.
func (s *Service) CloseJournal(ctx context.Context) (*models.Closure, error) {
	// The transactional capability, asserted rather than required on Store: only a
	// real storage can provide it, and demanding it of every implementation would
	// force the in-memory fakes to pretend.
	type transactional interface {
		RunInTx(ctx context.Context, fn func(ctx context.Context, store *storage.Storage) error) error
	}
	tx, ok := s.store.(transactional)
	if !ok {
		return nil, fmt.Errorf("closing the journal requires a transactional store")
	}

	var closure *models.Closure
	err := tx.RunInTx(ctx, func(ctx context.Context, txStore *storage.Storage) error {
		out, err := txStore.CloseCurrentClosure(ctx, storage.CloseClosureInput{
			ClosedBy: audit.SubjectFrom(ctx),
		})
		if err != nil {
			return err
		}
		closure = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return closure, nil
}

// VerifyClosure re-derives a closure's hash and checks its signature.
func (s *Service) VerifyClosure(ctx context.Context, id int64) (*models.Closure, bool, string, error) {
	closure, err := s.store.GetClosure(ctx, id)
	if err != nil {
		return nil, false, "", newStorageError(err, "getting closure")
	}
	ok, reason, err := s.store.VerifyClosureSignature(ctx, closure)
	if err != nil {
		return nil, false, "", newStorageError(err, "verifying closure signature")
	}
	return closure, ok, reason, nil
}

// GetClosingSchedule returns the cron rotating closures, empty when manual.
func (s *Service) GetClosingSchedule(ctx context.Context) (string, error) {
	cron, err := s.store.GetClosingSchedule(ctx)
	if err != nil {
		return "", newStorageError(err, "getting closing schedule")
	}
	return cron, nil
}

// SetClosingSchedule stores the rotation cron, validating it first.
//
// Validated here rather than at the storage boundary because a schedule that
// does not parse is a silent outage: rotation simply stops, and nobody finds out
// until an auditor asks why the books were never closed.
func (s *Service) SetClosingSchedule(ctx context.Context, spec string) error {
	if spec != "" {
		if _, err := cron.ParseStandard(spec); err != nil {
			return fmt.Errorf("%w: %q is not a valid cron expression: %s", ErrValidation, spec, err)
		}
	}
	if err := s.store.SetClosingSchedule(ctx, spec); err != nil {
		return newStorageError(err, "setting closing schedule")
	}
	return nil
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
