package service

import (
	"context"
	"github.com/numary/reconciliation/internal/model"
	"github.com/numary/reconciliation/internal/storage"
)

func GetReconciliation(ctx context.Context, store storage.Store, txID int64) (model.FullReconTransaction, error) {

	tx, err := store.GetTransaction(ctx, txID)
	if err != nil {
		//todo: LOG
		return model.FullReconTransaction{}, err
	}

	return tx, nil
}
