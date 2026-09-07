package service

import (
	"context"
	"fmt"

	"github.com/formancehq/reconciliation/internal/contractversion"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
)

func createContractVersion(ctx context.Context) models.ContractVersion {
	if version, ok := contractversion.FromContext(ctx); ok {
		return version
	}
	return models.ContractVersionV1
}

func requireContractVersion(ctx context.Context, actual models.ContractVersion) error {
	expected, scoped := contractversion.FromContext(ctx)
	if scoped && actual.Effective() != expected {
		return fmt.Errorf("contract version mismatch: %w", store.ErrNotFound)
	}
	return nil
}

func validateTemplateContract(version models.ContractVersion, kind models.TemplateKind) error {
	switch version.Effective() {
	case models.ContractVersionV1:
		switch kind {
		case models.TemplateLedgerInvariant, models.TemplateSourceParity, models.TemplateAccountThreshold:
			return nil
		}
	case models.ContractVersionV2:
		switch kind {
		case models.TemplateBalanceEquation, models.TemplateExchangeRateBounds,
			models.TemplateSourceConsensus, models.TemplateCoverageRatioBounds,
			models.TemplateStaleHolds:
			return nil
		}
	}
	return fmt.Errorf("template kind %q is not available in contract V%d", kind, version.Effective())
}

func (s *Service) getRuleForContract(ctx context.Context, id uuid.UUID) (*models.Rule, error) {
	rule, err := s.store.GetRule(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := requireContractVersion(ctx, rule.ContractVersion); err != nil {
		return nil, err
	}
	return rule, nil
}

func (s *Service) getAlertForContract(ctx context.Context, id uuid.UUID) (*models.Alert, error) {
	alert, err := s.store.GetAlert(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := requireContractVersion(ctx, alert.ContractVersion); err != nil {
		return nil, err
	}
	return alert, nil
}
