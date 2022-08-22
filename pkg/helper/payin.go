package helper

import (
	"context"
	"fmt"
	"github.com/numary/reconciliation/pkg/events"
	"github.com/numary/reconciliation/pkg/transform"
	"log"
	"strconv"

	"github.com/numary/reconciliation/pkg/rules"
	"github.com/numary/reconciliation/pkg/storage"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

func ReconciliatePayins(ctx context.Context, db *mongo.Database, pspIdPath string) error {
	paymentCursor, err := storage.GetPaymentAndTransactionPayOut(ctx, db, pspIdPath)
	if err != nil {
		fmt.Printf("error: could not get payment/tx pay-in aggregation : %v\n", err) //TODO: async
		return err
	}
	reconciliations, err := UnmarshalPayin(ctx, paymentCursor)

	for _, recon := range reconciliations {
		status, err := rules.ReconciliationPayin(ctx, recon)
		if err != nil {
			return err
		}

		fullTxs, err := UpdatePayinStatus(ctx, db, recon, *status)
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
func UnmarshalPayin(ctx context.Context, paymentCursor *mongo.Cursor) ([]storage.PaymentReconciliation, error) {
	var payinReconciliations []storage.PaymentReconciliation

	for paymentCursor.Next(ctx) {
		var agg storage.PaymentReconciliation
		if err := bson.Unmarshal(paymentCursor.Current, &agg); err != nil {
			fmt.Println("error: could not unmarshal payment to bson")
			return []storage.PaymentReconciliation{}, err
		}

		payinReconciliations = append(payinReconciliations, agg)
	}

	if err := paymentCursor.Err(); err != nil {
		fmt.Println("error: something went wrong while going through mongo cursor")
		return nil, err
	}
	return payinReconciliations, nil
}

// Update the payment/tx object in db, and return an array of object enriched with status
func UpdatePayinStatus(ctx context.Context, db *mongo.Database, agg storage.PaymentReconciliation, status storage.ReconciliationStatus) ([]storage.FullReconTransaction, error) {

	var payins []storage.FullReconTransaction

	// update payment
	objID, err := primitive.ObjectIDFromHex(agg.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("updating payment...")
	// TODO: array ?
	status.LinkedID = strconv.Itoa(int(agg.Transactions[0].Txid))
	if _, err := db.
		Collection(storage.CollPayments).
		UpdateByID(ctx, objID, bson.M{"$set": bson.M{"reconciliation_status": status}}); err != nil {
		log.Fatal(err)
	}
	fmt.Println("update payment recon status : OK")

	// maybe not shadow external_id ?
	status.LinkedID = agg.ID // this may not be the best id to use (it's mongodb id)
	// update txledger
	//TODO: we could maybe do an updateMany with the psp_id filter and type = payin ?
	for _, tx := range agg.Transactions {
		fmt.Println("updating tx...")
		if _, err := db.
			Collection(storage.CollLedger).
			UpdateOne(ctx, bson.M{"txid": tx.Txid}, bson.M{"$set": bson.M{"reconciliation_status": status}}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("update ledger recon status : OK")

		payins = append(payins, storage.FullReconTransaction{
			Transaction:          tx,
			ReconciliationStatus: storage.Statuses{"pay-in": status},
		})
	}

	return payins, nil
}
