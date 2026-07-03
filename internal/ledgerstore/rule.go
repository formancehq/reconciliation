package ledgerstore

import (
	"context"
	"errors"
	"fmt"

	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CreateRule writes a rule as typed metadata on its `rule:{id}` account.
func (s *LedgerStore) CreateRule(ctx context.Context, r *models.Rule) error {
	if err := s.client.SaveAccountMetadataValues(ctx, s.controlLedger, schema.RuleAccount(r.ID.String()), ruleToMetadata(r)); err != nil {
		return fmt.Errorf("create rule %s: %w", r.ID, err)
	}

	return nil
}

// GetRule reads a rule by ID. Returns storage.ErrNotFound if it has no metadata
// (never created) or the ledger reports the account as missing.
func (s *LedgerStore) GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error) {
	acct, err := s.client.GetAccount(ctx, s.controlLedger, schema.RuleAccount(id.String()), 0)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("get rule %s: %w", id, storage.ErrNotFound)
		}

		return nil, fmt.Errorf("get rule %s: %w", id, err)
	}

	if len(acct.GetMetadata()) == 0 {
		return nil, fmt.Errorf("get rule %s: %w", id, storage.ErrNotFound)
	}

	rule, err := ruleFromAccount(acct)
	if err != nil {
		return nil, fmt.Errorf("decode rule %s: %w", id, err)
	}

	return rule, nil
}

// isNotFound reports whether err (possibly wrapped) is a gRPC NotFound status.
func isNotFound(err error) bool {
	for err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return true
		}

		err = errors.Unwrap(err)
	}

	return false
}
