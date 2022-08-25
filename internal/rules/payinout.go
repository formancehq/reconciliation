package rules

import (
	"context"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/numary/reconciliation/internal/model"
	"github.com/numary/reconciliation/internal/storage"
	"github.com/pkg/errors"
)

type PayInOut struct {
	Rule model.Rule
}

func (p PayInOut) Accept(ctx context.Context, event model.Event) bool {
	if ref, ok := event.Payload["reference"]; ok {
		if ref != "" {
			return true
		}
	}

	return false
}

func (p PayInOut) Reconciliate(ctx context.Context, store storage.Store, event model.Event) error {
	payment, err := store.GetPaymentAndTransactionPayInOut(ctx, p.Rule.Name, p.Rule.Configuration["psp_id_path"], event.Payload["reference"].(string))
	if err != nil {
		//TODO: log
		return err
	}

	result, err := reconciliationPayInOut(ctx, payment)
	spew.Dump(result.Status)

	return err
}

var reconciliationPayInOut = func(ctx context.Context, agg model.PaymentReconciliation) (*model.ReconciliationStatus, error) {
	var reconStatus model.ReconciliationStatus

	if len(agg.Transactions) <= 0 {
		// generate reconciliation error
		return nil, errors.New("no transactions found")
	} else {
		var txAmount int64

		// TODO XXX
		for _, chargePosting := range agg.Transactions[0].Postings {
			txAmount += int64(chargePosting.Amount) // what to do if multiples postings ? check world dest ?
		}

		if agg.InitialAmount == txAmount {
			fmt.Printf("reconciliation successful for pay-in/payout : payment %s and ledger_tx %d\n", agg.Reference, agg.Transactions[0].Txid)
			reconStatus = model.SuccessStatus
		} else {
			fmt.Printf("reconciliation failed for pay-in/payout : payment %s and ledger_tx %d : amount mismatch (%d vs %d)\n", agg.Reference, agg.Transactions[0].Txid, agg.InitialAmount, int64(agg.Transactions[0].Postings[0].Amount))
			reconStatus = model.AmountMismatchStatus
		}
	}
	return &reconStatus, nil
}
