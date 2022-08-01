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

func ReconciliationPayouts(ctx context.Context, paymentCursor *mongo.Cursor, db *mongo.Database) error {
	var success, failure int64
	var err error
	for paymentCursor.Next(ctx) {
		var agg payInReconciliation
		reconStatus := make(Statuses)

		if err := bson.Unmarshal(paymentCursor.Current, &agg); err != nil {
			fmt.Println("error: could not unmarshal payment to bson")
			log.Fatal(err)
		}

		// check if the reconciliation is OK
		// in production, we would have a dedicated method that would parse the rule configuration
		if len(agg.Transactions) <= 0 {
			fmt.Println(agg.Reference)
			panic(0)
		} else {
			var txAmount int64
			for _, tx := range agg.Transactions {
				txAmount += int64(tx.Postings[0].Amount) // what to do if multiples postings ? check world dest ?
			}

			fmt.Printf("total amount : %d -- payout amount : %d\n", agg.InitialAmount, txAmount)
			if txAmount == agg.InitialAmount {
				reconStatus["payout"] = SuccessStatus
				success++
			} else {
				reconStatus["payout"] = AmountMismatchStatus
				fmt.Printf("failure : %s with total : %d and payout %d\n", agg.Reference, agg.InitialAmount, txAmount)
				failure++
			}
		}

		// update payment
		objID, err := primitive.ObjectIDFromHex(agg.ID)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("updating payment...")
		if _, err := db.
			Collection(database.CollPayments).
			UpdateByID(ctx, objID, bson.D{{"$set", bson.D{{"reconciliation_status", reconStatus}}}}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("update payment recon status : OK")

		// update txledger
		fmt.Println("updating tx...")
		if _, err := db.
			Collection(database.CollLedger).
			UpdateOne(ctx, bson.D{{"txid", agg.Transactions[0].Txid}}, bson.D{{"$set", bson.D{{"reconciliation_status", reconStatus}}}}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("update ledger recon status : OK")
	}

	fmt.Printf("reconciliation payout ended with %d success and %d failures\n", success, failure)

	if err := paymentCursor.Err(); err != nil {
		log.Fatal(err)
	}
	return err
}
