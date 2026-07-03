package ledgerstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CreateRule writes a rule as typed metadata on its `rule:{id}` account.
//
// NOTE: unlike the Postgres store (which fails on duplicate PK), this is an
// upsert (metadata is last-write-wins). Rule IDs are generated UUIDs, so
// collisions do not occur in practice; a guarded create would cost a
// read-before-write round-trip on every call. See migration log F13.
func (s *LedgerStore) CreateRule(ctx context.Context, r *models.Rule) error {
	md, err := ruleToMetadata(r)
	if err != nil {
		return fmt.Errorf("create rule %s: %w", r.ID, err)
	}

	if err := s.client.SaveAccountMetadataValues(ctx, s.controlLedger, schema.RuleAccount(r.ID.String()), md); err != nil {
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

// PatchRule applies a partial update (read-modify-write). Returns
// storage.ErrNotFound if the rule is gone. Removed labels are deleted so a
// label map replacement does not leave stale `label.*` keys behind.
func (s *LedgerStore) PatchRule(ctx context.Context, id uuid.UUID, patch storage.RulePatch) error {
	rule, err := s.GetRule(ctx, id)
	if err != nil {
		return err // already wrapped (incl. ErrNotFound)
	}

	oldLabels := rule.Labels
	applyRulePatch(rule, patch)
	rule.UpdatedAt = time.Now().UTC()

	md, err := ruleToMetadata(rule)
	if err != nil {
		return fmt.Errorf("patch rule %s: %w", id, err)
	}

	if err := s.client.SaveAccountMetadataValues(ctx, s.controlLedger, schema.RuleAccount(id.String()), md); err != nil {
		return fmt.Errorf("patch rule %s: %w", id, err)
	}

	if patch.Labels != nil {
		if removed := removedLabelKeys(oldLabels, rule.Labels); len(removed) > 0 {
			if err := s.client.DeleteAccountMetadata(ctx, s.controlLedger, schema.RuleAccount(id.String()), removed...); err != nil {
				return fmt.Errorf("patch rule %s: prune labels: %w", id, err)
			}
		}
	}

	return nil
}

// DeleteRule removes a rule by clearing all its account metadata. Returns
// storage.ErrNotFound if the rule does not exist.
func (s *LedgerStore) DeleteRule(ctx context.Context, id uuid.UUID) error {
	acct, err := s.client.GetAccount(ctx, s.controlLedger, schema.RuleAccount(id.String()), 0)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("delete rule %s: %w", id, storage.ErrNotFound)
		}

		return fmt.Errorf("delete rule %s: %w", id, err)
	}

	keys := make([]string, 0, len(acct.GetMetadata()))
	for k := range acct.GetMetadata() {
		keys = append(keys, k)
	}

	if len(keys) == 0 {
		return fmt.Errorf("delete rule %s: %w", id, storage.ErrNotFound)
	}

	if err := s.client.DeleteAccountMetadata(ctx, s.controlLedger, schema.RuleAccount(id.String()), keys...); err != nil {
		return fmt.Errorf("delete rule %s: %w", id, err)
	}

	return nil
}

// ListRules returns a cursor-paginated set of rules, ordered created_at DESC to
// match the Postgres store. The query.Builder filters (id → address; name /
// templateKind / enabled → metadata; createdAt / updatedAt → datetime range) are
// translated to a ledger QueryFilter; the full matching set is fetched, ordered,
// then offset-sliced (see pagination.go for the cost note).
func (s *LedgerStore) ListRules(ctx context.Context, q storage.GetRulesQuery) (*bunpaginate.Cursor[models.Rule], error) {
	filter, err := buildListFilter(schema.RulePrefix(), q.Options.QueryBuilder, ruleLeaf)
	if err != nil {
		return nil, err
	}

	accts, err := s.client.QueryAccounts(ctx, s.controlLedger, filter, 0)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}

	rules := make([]models.Rule, 0, len(accts))
	for _, acct := range accts {
		r, derr := ruleFromAccount(acct)
		if derr != nil {
			return nil, fmt.Errorf("list rules: decode %s: %w", acct.GetAddress(), derr)
		}

		rules = append(rules, *r)
	}

	slices.SortFunc(rules, func(a, b models.Rule) int { return b.CreatedAt.Compare(a.CreatedAt) })

	data, hasMore := paginateSlice(rules, q.Offset, q.PageSize)

	return offsetCursor(bunpaginate.OffsetPaginatedQuery[storage.PaginatedQueryOptions[storage.RulesFilters]](q), data, hasMore), nil
}

// applyRulePatch mutates rule in place with the non-nil fields of patch.
func applyRulePatch(rule *models.Rule, patch storage.RulePatch) {
	if patch.Name != nil {
		rule.Name = *patch.Name
	}

	if patch.TemplateKind != nil {
		rule.TemplateKind = *patch.TemplateKind
	}

	if patch.TemplateSpec != nil {
		rule.TemplateSpec = patch.TemplateSpec
	}

	if patch.CompiledCEL != nil {
		rule.CompiledCEL = *patch.CompiledCEL
	}

	if patch.Enabled != nil {
		rule.Enabled = *patch.Enabled
	}

	if patch.Severity != nil {
		rule.Severity = *patch.Severity
	}

	if patch.Schedule != nil {
		rule.Schedule = patch.Schedule
	}

	if patch.Notifications != nil {
		rule.Notifications = *patch.Notifications
	}

	if patch.Labels != nil {
		rule.Labels = *patch.Labels
	}
}

// removedLabelKeys returns the metadata keys (label.*) present in old but not in
// updated.
func removedLabelKeys(old, updated map[string]string) []string {
	var removed []string

	for k := range old {
		if _, ok := updated[k]; !ok {
			removed = append(removed, schema.LabelPrefix+k)
		}
	}

	return removed
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
