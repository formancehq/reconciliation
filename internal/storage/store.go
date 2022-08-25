package storage

import (
	"context"

	"github.com/numary/reconciliation/internal/model"
)

type Store interface {
	GetTransactionsWithOrder(ctx context.Context, flowIdPath string) ([]model.LedgerTransactions, error)
	UpdateEndToEndStatus(ctx context.Context, agg model.LedgerTransactions, badBalance map[string]int32) ([]model.FullReconTransaction, error)
	GetPaymentAndTransactionPayInOut(ctx context.Context, paymentType string, pspIdPath string, psp_id string) (model.PaymentReconciliation, error)
	GetRules(ctx context.Context) (model.Rules, error)
	InsertObject(ctx context.Context, event model.Event) error
	UpdatePayinStatus(ctx context.Context, agg model.PaymentReconciliation, status model.ReconciliationStatus) ([]model.FullReconTransaction, error)
	UpdatePayoutStatus(ctx context.Context, agg model.PaymentReconciliation, status model.ReconciliationStatus) ([]model.FullReconTransaction, error)
	CreateIndexes(ctx context.Context) error
	Close(ctx context.Context) error
}
