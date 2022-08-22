package service

import (
	"context"
	"fmt"

	"github.com/numary/reconciliation/internal/events"
	"github.com/numary/reconciliation/internal/rules"
	"github.com/numary/reconciliation/internal/storage"
	"github.com/numary/reconciliation/internal/transform"
)

func ReconciliateEndToEnd(ctx context.Context, store storage.Store, flowIdPath string) error {
	reconciliations, err := store.GetTransactionsWithOrder(ctx, flowIdPath)
	if err != nil {
		return fmt.Errorf("storage.Store.GetTransactionsWithOrder: %w", err)
	}

	for _, recon := range reconciliations {
		badTxs, err := rules.ReconciliateEndToEnd(ctx, recon)
		if err != nil {
			return err
		}

		fullTxs, err := store.UpdateEndToEndStatus(ctx, recon, badTxs)
		if err != nil {
			return err
		}

		// i know we loop through the tx multiple times, but we'll see later if we get performance issues
		for _, fullTx := range fullTxs {
			lightTx, err := transform.FullTxToPaymentReconciliation(fullTx)
			if err != nil {
				//TODO: log error
				return err
			}

			//TODO: implement
			if err := events.SendTxEventSearch(lightTx); err != nil {
				return err
			}
		}
	}

	return nil
}
