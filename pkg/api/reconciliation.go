package api

import (
	"context"
	"fmt"
	"github.com/numary/reconciliation/pkg/database"
	"github.com/numary/reconciliation/pkg/reconciliation"
	"go.mongodb.org/mongo-driver/mongo"
	"net/http"
)

func EndToEndHandler(ctx context.Context, db *mongo.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// end to end
		ledgerCursor, err := database.GetTransactionsWithOrder(ctx, db)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
		} else {
			err = reconciliation.ReconciliateEndToEnd(ctx, ledgerCursor, db)
			if err != nil {
				fmt.Printf("error: could not reconciliate flow : %v", err)
			}
		}

		// TODO: return something
	}
}

func AmountMatchingHandler(ctx context.Context, db *mongo.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// pay-in
		paymentCursor, err := database.GetPaymentAndTransactionPayIn(ctx, db)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
		} else {
			err = reconciliation.ReconciliationPayIn(ctx, paymentCursor, db)
			if err != nil {
				fmt.Printf("error: could not reconciliate pay-ins : %v", err)
			}
		}

		// payout
		paymentCursor, err = database.GetPaymentAndTransactionPayOut(ctx, db)
		if err != nil {
			fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err)
		} else {
			err = reconciliation.ReconciliationPayouts(ctx, paymentCursor, db)
			if err != nil {
				fmt.Printf("error: could not reconciliate payouts : %v", err)
			}
		}

		// TODO return something
	}
}
