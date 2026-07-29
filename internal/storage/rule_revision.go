package storage

import (
	"context"

	"github.com/google/uuid"

	"github.com/formancehq/reconciliation/internal/models"
)

// insertRuleRevision freezes a control definition at one revision.
//
// Called inside the same transaction as the rule write and its chain entry, so
// the three either all land or none do. The revision row is what makes the chain
// answer "what was being checked", which a verdict alone cannot: the rule row
// only ever holds the latest spec, and an evaluation that references revision 3
// of a rule now at revision 7 would otherwise be unexplainable.
func (s *Storage) insertRuleRevision(ctx context.Context, rule *models.Rule, auditSequence int64) error {
	rev := &models.RuleRevision{
		RuleID:         rule.ID,
		Revision:       rule.Revision,
		Name:           rule.Name,
		TemplateKind:   rule.TemplateKind,
		TemplateSpec:   rule.TemplateSpec,
		ExplanationCEL: rule.ExplanationCEL,
		Severity:       rule.Severity,
		Cadence:        rule.Cadence,
		Enabled:        rule.Enabled,
		Schedule:       rule.Schedule,
		Notifications:  rule.Notifications,
		Labels:         rule.Labels,
		AuditSequence:  auditSequence,
	}
	// A revision is written once. A conflict means the same revision was already
	// frozen — keep the original rather than overwrite it, since the chain entry
	// that witnessed it refers to those bytes.
	if _, err := s.db.NewInsert().Model(rev).
		On("CONFLICT (rule_id, revision) DO NOTHING").Exec(ctx); err != nil {
		return e("insert rule revision", err)
	}
	return nil
}

// GetRuleRevision returns one frozen definition.
func (s *Storage) GetRuleRevision(ctx context.Context, ruleID uuid.UUID, revision int64) (*models.RuleRevision, error) {
	var rev models.RuleRevision
	if err := s.db.NewSelect().Model(&rev).
		Where("rule_id = ?", ruleID).Where("revision = ?", revision).Scan(ctx); err != nil {
		return nil, e("get rule revision", err)
	}
	return &rev, nil
}

// ListRuleRevisions returns a rule's definitions newest first.
func (s *Storage) ListRuleRevisions(ctx context.Context, ruleID uuid.UUID) ([]models.RuleRevision, error) {
	var revs []models.RuleRevision
	if err := s.db.NewSelect().Model(&revs).
		Where("rule_id = ?", ruleID).Order("revision DESC").Scan(ctx); err != nil {
		return nil, e("list rule revisions", err)
	}
	return revs, nil
}
