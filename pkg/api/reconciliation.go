package api

import (
	"fmt"
	"net/http"

	"github.com/numary/reconciliation/pkg/reconciliation"
	"github.com/numary/reconciliation/pkg/storage"
	"go.mongodb.org/mongo-driver/mongo"
)

func EndToEndHandler(db *mongo.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		
		flowIDpath := r.URL.Query().Get("flow_id_path") //TODO: see if camelcase standard
		if flowIDpath == "" {
			//TODO: see if default is a good idea
			flowIDpath = "metadata.order_id" //TODO: flow_id
		}

		// end to end
		ledgerCursor, err := storage.GetTransactionsWithOrder(ctx, db, flowIDpath)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
		} else {
			err = reconciliation.ReconciliateEndToEnd(ctx, ledgerCursor, db) //TODO: async
			if err != nil {
				fmt.Printf("error: could not reconciliate flow : %v", err)
			}
		}
		// TODO: send to kafka
	}
}

func AmountMatchingHandler(db *mongo.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		pspIdPath := r.URL.Query().Get("psp_id_path")
		if pspIdPath == "" {
			pspIdPath = "metadata.psp_id"
		}

		// pay-in
		paymentCursor, err := storage.GetPaymentAndTransactionPayIn(ctx, db, pspIdPath)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
		} else {
			err = reconciliation.ReconciliationPayIn(ctx, paymentCursor, db) //TODO: async
			if err != nil {
				fmt.Printf("error: could not reconciliate pay-ins : %v", err)
			}
		}

		// payout
		paymentCursor, err = storage.GetPaymentAndTransactionPayOut(ctx, db, pspIdPath)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err) //TODO: async
		} else {
			err = reconciliation.ReconciliationPayouts(ctx, paymentCursor, db)
			if err != nil {
				fmt.Printf("error: could not reconciliate payouts : %v", err)
			}
		}
		// TODO: send to kafka
	}
}

//// maybe not useful anymore if we just send stuff to kafka
//func ListReconciliationsHandler(ctx context.Context, db *mongo.Database) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		// payout
//		txCursor, err := storage.GetReconciliationFailures(ctx, db)
//		if err != nil {
//			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
//		} else {
//			_, err := reconciliation.ListFailures(ctx, txCursor)
//			if err != nil {
//				fmt.Printf("error: could not reconciliate payouts : %v", err)
//			}
//		}
//	}
//}
