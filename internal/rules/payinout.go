package rules

import (
	"context"
	"fmt"
	"github.com/numary/reconciliation/internal/model"
	"github.com/numary/reconciliation/internal/storage"
	"github.com/pkg/errors"
)

type PayInOutRule struct {
	Name      string `json:"name"`
	PspIdPath string `json:"psp_id_path"`
}

func (p PayInOutRule) Accept(ctx context.Context, event model.Event) bool {
	//TODO this need to be calculated at runtime maybe ? see with payment how we manage multple psp
	if ref, ok := event.Payload["reference"]; ok {
		if ref != "" {
			return true
		}
	}

	return false
}

func (p PayInOutRule) reconciliate(ctx context.Context, store storage.Store, event model.Event) (model.ReconciliationStatus, error) {
	payment, err := store.GetPaymentAndTransactionPayInOut(ctx, p.Name, p.PspIdPath, event.Payload["reference"].(string))
	if err != nil {
		//TODO: log
		return model.ReconciliationStatus{}, err
	}

	result, err := reconciliationPayInOut(ctx, payment)
	if result != nil {
		return *result, err
	}

	return model.ReconciliationStatus{}, err
}

func (p PayInOutRule) Reconciliate(ctx context.Context, store storage.Store, event model.Event) error {

	_, err := p.reconciliate(ctx, store, event)

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
