package mongo

import (
	"context"
	"fmt"
	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/internal/model"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"log"
	"strconv"
)

func (s Store) GetPaymentAndTransactionPayInOut(ctx context.Context, paymentType string, pspIdPath string, psp_id string) (model.PaymentReconciliation, error) {
	coll := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.CollPayments)

	//spew.Dump(paymentType)
	//spew.Dump(pspIdPath)
	//
	//result := coll.FindOne(ctx, bson.M{"reference": "ch_3LIRpBFqW03ZYiNn1G7tFTgJ"})
	//var agg map[string]any
	//err := result.Decode(&agg)
	//if err != nil {
	//	return model.PaymentReconciliation{}, err
	//}
	//
	//spew.Dump(agg)

	//for cursorr.Next(ctx) {
	//	if err := bson.Unmarshal(cursorr.Current, &agg); err != nil {
	//		panic(err)
	//	}
	//	//spew.Dump(agg)
	//}

	cursor, err := coll.Aggregate(ctx,
		bson.A{
			bson.D{{"$match", bson.D{{"type", "pay-in"}}}},
			bson.D{{"$match", bson.D{{"reference", "ch_3LIRpBFqW03ZYiNn1G7tFTgJ"}}}},
			bson.D{
				{"$lookup",
					bson.D{
						{"from", "LedgerStuff"},
						{"localField", "reference"},
						{"foreignField", "metadata.psp_id"},
						{"as", "transaction_ledger"},
					},
				},
			},
			bson.D{
				{"$match",
					bson.D{
						{"transaction_ledger",
							bson.D{
								{"$exists", true},
								{"$ne", bson.A{}},
							},
						},
					},
				},
			},
		})
	if err != nil {
		return model.PaymentReconciliation{}, fmt.Errorf(
			"could not aggregate payments and transactions for the pay-out lookup: %w", err)
	}

	return unmarshalPaymentsFromMongo(ctx, cursor)
}

func (s Store) UpdatePayinStatus(ctx context.Context, agg model.PaymentReconciliation, status model.ReconciliationStatus) ([]model.FullReconTransaction, error) {
	db := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag))
	collLedger := db.Collection(constants.CollLedger)
	collPayments := db.Collection(constants.CollPayments)

	var payins []model.FullReconTransaction

	// update payment
	objID, err := primitive.ObjectIDFromHex(agg.ID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("updating payment...")
	// TODO: array ?
	status.LinkedID = strconv.Itoa(int(agg.Transactions[0].Txid))
	if _, err := collPayments.
		UpdateByID(ctx, objID,
			bson.M{"$set": bson.M{"reconciliation_status": status}},
		); err != nil {
		log.Fatal(err)
	}
	fmt.Println("update payment recon status : OK")

	// maybe not shadow external_id ?
	status.LinkedID = agg.ID // this may not be the best id to use (it's mongodb id)
	// update txledger
	//TODO: we could maybe do an updateMany with the psp_id filter and type = payin ?
	for _, tx := range agg.Transactions {
		fmt.Println("updating tx...")
		if _, err := collLedger.
			UpdateOne(ctx,
				bson.M{"txid": tx.Txid},
				bson.M{"$set": bson.M{"reconciliation_status": status}},
			); err != nil {
			log.Fatal(err)
		}
		fmt.Println("update ledger recon status : OK")

		payins = append(payins, model.FullReconTransaction{
			Transaction:          tx,
			ReconciliationStatus: model.Statuses{"pay-in": status},
		})
	}

	return payins, nil
}

func (s Store) InsertObject(ctx context.Context, event model.Event) error {
	coll := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.CollPayments)

	_, err := coll.InsertOne(ctx, event.Payload)
	return err
}
