package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/numary/reconciliation/pkg/database"
	"github.com/numary/reconciliation/pkg/reconciliation"
	"go.mongodb.org/mongo-driver/mongo"
)

func EndToEndHandler(ctx context.Context, db *mongo.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flowIDpath := r.URL.Query().Get("flow_id_path")
		if flowIDpath == "" {
			flowIDpath = "metadata.order_id"
		}

		// end to end
		ledgerCursor, err := database.GetTransactionsWithOrder(ctx, db, flowIDpath)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
		} else {
			err = reconciliation.ReconciliateEndToEnd(ctx, ledgerCursor, db)
			if err != nil {
				fmt.Printf("error: could not reconciliate flow : %v", err)
			}
		}
		// TODO: send to kafka
	}
}

func AmountMatchingHandler(ctx context.Context, db *mongo.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		pspIdPath := r.URL.Query().Get("psp_id_path")
		if pspIdPath == "" {
			pspIdPath = "metadata.psp_id"
		}

		// pay-in
		paymentCursor, err := database.GetPaymentAndTransactionPayIn(ctx, db, pspIdPath)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
		} else {
			err = reconciliation.ReconciliationPayIn(ctx, paymentCursor, db)
			if err != nil {
				fmt.Printf("error: could not reconciliate pay-ins : %v", err)
			}
		}

		// payout
		paymentCursor, err = database.GetPaymentAndTransactionPayOut(ctx, db, pspIdPath)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
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
//		txCursor, err := database.GetReconciliationFailures(ctx, db)
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
