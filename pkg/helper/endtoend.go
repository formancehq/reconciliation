package helper

import (
	"context"
	"fmt"
	"github.com/numary/reconciliation/pkg/events"
	"github.com/numary/reconciliation/pkg/rules"
	"github.com/numary/reconciliation/pkg/storage"
	"github.com/numary/reconciliation/pkg/transform"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"log"
)

func ReconciliateEndToEnd(ctx context.Context, db *mongo.Database, flowID string) error {
	paymentCursor, err := storage.GetTransactionsWithOrder(ctx, db, flowID)
	if err != nil {
		fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err) //TODO: async
		return err
	}
	reconciliations, err := UnmarshalEndToEnd(ctx, paymentCursor)

	for _, recon := range reconciliations {
		badTxs, err := rules.ReconciliateEndToEnd(ctx, recon)
		if err != nil {
			return err
		}

		fullTxs, err := UpdateEndToEndStatus(ctx, db, recon, badTxs)
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
			events.SendTxEventSearch(lightTx)
		}
	}

	return nil
}

// get the mongo result and unmarshal it in usable objects
func UnmarshalEndToEnd(ctx context.Context, paymentCursor *mongo.Cursor) ([]storage.LedgerTransactions, error) {
	var txs []storage.LedgerTransactions

	for paymentCursor.Next(ctx) {
		var tx storage.LedgerTransactions
		if err := bson.Unmarshal(paymentCursor.Current, &tx); err != nil {
			fmt.Println("error: could not unmarshal transaction to bson")
			return []storage.LedgerTransactions{}, err
		}

		txs = append(txs, tx)
	}

	if err := paymentCursor.Err(); err != nil {
		fmt.Println("error: something went wrong while going through mongo cursor")
		return nil, err
	}
	return txs, nil
}

func UpdateEndToEndStatus(ctx context.Context, db *mongo.Database, agg storage.LedgerTransactions, badBalance map[string]int32) ([]storage.FullReconTransaction, error) {
	var fullTxs []storage.FullReconTransaction

	// maybe not shadow external_id ?
	//status.LinkedID = agg. // this may not be the best id to use (it's mongodb id)

	// update txledger
	//TODO: we could maybe do an updateMany with the flow_id filter and type = XXX ?
	for _, tx := range agg.Transactions {
		fmt.Println("updating tx...")

		reconStatus := make(storage.Statuses)
		reconStatus["end-to-end"] = rules.SuccessStatus
		// update txledger
		for _, txID := range badBalance { // do not like this loop
			if tx.Txid == txID {
				reconStatus["end-to-end"] = rules.EndToEndMismatchStatus
			}
		}

		if _, err := db.
			Collection(storage.CollLedger).
			UpdateOne(ctx, bson.M{"txid": tx.Txid}, bson.M{"$set": bson.M{"reconciliation_status": reconStatus}}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("update ledger recon status : OK")

		fullTxs = append(fullTxs, storage.FullReconTransaction{ //TODO: this type is not good for this rule
			Transaction:          tx,
			ReconciliationStatus: reconStatus, // check if no shenanigans with the loop on reconstatus
		})
	}

	return fullTxs, nil
}
