package reconciliation

import (
	"context"
	"fmt"
	"log"
	"sort"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	ledgerclient "github.com/numary/numary-sdk-go"
)

type EndToEndReconciliation struct {
	Transactions []ledgerclient.Transaction `bson:"transactions"`
}

func ReconciliateEndToEnd(ctx context.Context, paymentCursor *mongo.Cursor, db *mongo.Database) error {
	var success, failure int64
	var err error
	for paymentCursor.Next(ctx) {
		var txs EndToEndReconciliation

		if err := bson.Unmarshal(paymentCursor.Current, &txs); err != nil {
			fmt.Println("error: could not unmarshal transactions to bson")
			log.Fatal(err)
		}

		// sort transactions by timestamp so we are sure balances are coherents
		sort.Slice(txs.Transactions[:], func(i, j int) bool {
			return txs.Transactions[i].Timestamp.Before(txs.Transactions[j].Timestamp)
		})

		badBalance := make(map[string]int32)
		for _, tx := range txs.Transactions {
			for keyAccount, elemAccount := range *tx.PostCommitVolumes {
				if keyAccount == "world" { // need a list of SkippAccounts
					continue
				}
				for assetKey, elemVolume := range elemAccount {
					if *elemVolume.Balance != float32(0.0) {
						// if balance is not at 0, we create an entry on the map
						badBalance[assetKey] = tx.Txid
					} else {
						// if balance is at 0, we remove the entry from the map
						delete(badBalance, assetKey)
					}
				}

			}
		}

		if len(badBalance) > 0 {
			failure++
			fmt.Printf("%v - %s\n", badBalance, txs.Transactions[0].Metadata["order_id"])
		} else {
			success++
			//fmt.Println("success")
			//TODO: add status to tx like we do in payin/payout
		}
	}

	fmt.Printf("end-to-end reconciliation ended with %d success and %d failures\n", success, failure)
	return err
}
