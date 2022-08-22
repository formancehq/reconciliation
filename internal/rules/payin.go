package rules

import (
	"context"
	"fmt"

	"github.com/numary/reconciliation/internal/model"
	"github.com/pkg/errors"
)

func ReconciliationPayin(ctx context.Context, agg model.PaymentReconciliation) (*model.ReconciliationStatus, error) {
	var reconStatus model.ReconciliationStatus

	if len(agg.Transactions) <= 0 {
		// generate reconciliation error
		return nil, errors.New("no transactions found")
	} else {
		var txAmount int64

		for _, chargePosting := range agg.Transactions[0].Postings {
			txAmount += int64(chargePosting.Amount) // what to do if multiples postings ? check world dest ?
		}

		if agg.InitialAmount == txAmount {
			fmt.Printf("reconciliation successful for pay-in : payment %s and ledger_tx %d\n", agg.Reference, agg.Transactions[0].Txid)
			reconStatus = SuccessStatus
		} else {
			fmt.Printf("reconciliation failed for pay-in : payment %s and ledger_tx %d : amount mismatch (%d vs %d)\n", agg.Reference, agg.Transactions[0].Txid, agg.InitialAmount, int64(agg.Transactions[0].Postings[0].Amount))
			reconStatus = AmountMismatchStatus
		}
	}
	return &reconStatus, nil
}
