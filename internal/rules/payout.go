package rules

import (
	"context"
	"fmt"

	"github.com/numary/reconciliation/internal/model"
	"github.com/pkg/errors"
)

func ReconciliationPayout(ctx context.Context, agg model.PaymentReconciliation) (*model.ReconciliationStatus, error) {
	var reconStatus model.ReconciliationStatus

	//TODO: use ctx to do some logging

	// check if the reconciliation is OK
	// in production, we would have a dedicated method that would parse the rule configuration
	if len(agg.Transactions) <= 0 {
		fmt.Println(agg.Reference)
		return nil, errors.New("no transactons found")
	} else {
		var txAmount int64
		for _, tx := range agg.Transactions {
			txAmount += int64(tx.Postings[0].Amount) // what to do if multiples postings ? check world dest ?
		}
		fmt.Printf("total amount : %d -- payout amount : %d\n", agg.InitialAmount, txAmount)
		if txAmount == agg.InitialAmount {
			reconStatus = SuccessStatus
		} else {
			reconStatus = AmountMismatchStatus
			fmt.Printf("failure : %s with total : %d and payout %d\n", agg.Reference, agg.InitialAmount, txAmount)
		}
	}
	return &reconStatus, nil
}
