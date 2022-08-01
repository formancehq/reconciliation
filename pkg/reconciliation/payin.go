package reconciliation

import (
	"context"
	"fmt"
	"log"

	"github.com/numary/reconciliation/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

func ReconciliationPayIn(ctx context.Context, paymentCursor *mongo.Cursor, db *mongo.Database) error {
	var success, failure int64
	var err error
	for paymentCursor.Next(ctx) {
		var agg payInReconciliation
		reconStatus := make(Statuses)

		if err := bson.Unmarshal(paymentCursor.Current, &agg); err != nil {
			fmt.Println("error: could not unmarshal payment to bson")
			log.Fatal(err)
		}

		if len(agg.Transactions) <= 0 {
			// generate reconciliation error
			panic(0)
		} else {
			var txAmount int64

			for _, chargePosting := range agg.Transactions[0].Postings {
				txAmount += int64(chargePosting.Amount) // what to do if multiples postings ? check world dest ?
			}

			if agg.InitialAmount == txAmount {
				fmt.Printf("reconciliation successful for pay-in : payment %s and ledger_tx %d\n", agg.Reference, agg.Transactions[0].Txid)
				reconStatus["pay-in"] = SuccessStatus
				success++
			} else {
				fmt.Printf("reconciliation failed for pay-in : payment %s and ledger_tx %d : amount mismatch (%d vs %d)\n", agg.Reference, agg.Transactions[0].Txid, agg.InitialAmount, int64(agg.Transactions[0].Postings[0].Amount))
				reconStatus["pay-in"] = AmountMismatchStatus
				failure++
			}
		}

		// update payment
		// use UpdateOne wiht a filter
		objID, err := primitive.ObjectIDFromHex(agg.ID)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := db.
			Collection(database.CollPayments).
			UpdateByID(ctx, objID, bson.D{{"$set", bson.D{{"reconciliation_status", reconStatus}}}}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("update payment recon status : OK")

		// update txledger
		if _, err := db.
			Collection(database.CollLedger).
			UpdateOne(ctx, bson.D{{"txid", agg.Transactions[0].Txid}}, bson.D{{"$set", bson.D{{"reconciliation_status", reconStatus}}}}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("update ledger recon status : OK")
	}

	fmt.Printf("reconciliation pay-in ended with %d success and %d failures\n", success, failure)

	if err := paymentCursor.Err(); err != nil {
		log.Fatal(err)
	}
	return err
}
