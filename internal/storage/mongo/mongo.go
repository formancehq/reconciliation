package mongo

import (
	"context"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/numary/go-libs/sharedlogging"
	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/internal/model"
	"github.com/numary/reconciliation/internal/rules"
	"github.com/numary/reconciliation/internal/storage"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

type Store struct {
	client *mongo.Client
}

func NewStore() (storage.Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoDBUri := viper.GetString(constants.StorageMongoConnStringFlag)
	sharedlogging.Infof("connecting to mongoDB URI: %s", mongoDBUri)
	sharedlogging.Infof("env: %+v", os.Environ())

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoDBUri))
	if err != nil {
		return Store{}, err
	}

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return Store{}, err
	}

	return Store{
		client: client,
	}, nil
}

func (s Store) GetTransactionsWithOrder(ctx context.Context, flowIdPath string) ([]model.LedgerTransactions, error) {
	coll := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.CollLedger)

	cursor, err := coll.Aggregate(ctx,
		[]any{
			bson.M{"$group": bson.M{
				"_id":          fmt.Sprintf("$%s", flowIdPath),
				"transactions": bson.M{"$push": "$$ROOT"}},
			},
		},
		options.Aggregate().SetAllowDiskUse(true)) //TODO: remove before prod lol
	if err != nil {
		return []model.LedgerTransactions{}, fmt.Errorf(
			"could not aggregate transactions by order_id: %w", err)
	}

	var txs []model.LedgerTransactions

	for cursor.Next(ctx) {
		var tx model.LedgerTransactions
		if err := bson.Unmarshal(cursor.Current, &tx); err != nil {
			return []model.LedgerTransactions{}, fmt.Errorf(
				"could not unmarshal transactions to bson: %w", err)
		}

		txs = append(txs, tx)
	}

	if err := cursor.Err(); err != nil {
		return []model.LedgerTransactions{}, fmt.Errorf(
			"something went wrong while going through mongo cursor: %w", err)
	}

	return txs, nil
}

func (s Store) UpdateEndToEndStatus(ctx context.Context, agg model.LedgerTransactions, badBalance map[string]int32) ([]model.FullReconTransaction, error) {
	coll := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.CollLedger)

	var fullTxs []model.FullReconTransaction

	// maybe not shadow external_id ?
	//status.LinkedID = agg. // this may not be the best id to use (it's mongodb id)

	// update txledger
	//TODO: we could maybe do an updateMany with the flow_id filter and type = XXX ?
	for _, tx := range agg.Transactions {
		fmt.Println("updating tx...")

		reconStatus := make(model.Statuses)
		reconStatus["end-to-end"] = rules.SuccessStatus
		// update txledger
		for _, txID := range badBalance { // do not like this loop
			if tx.Txid == txID {
				reconStatus["end-to-end"] = rules.EndToEndMismatchStatus
			}
		}

		if _, err := coll.UpdateOne(ctx, bson.M{"txid": tx.Txid}, bson.M{
			"$set": bson.M{"reconciliation_status": reconStatus},
		}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("update ledger recon status : OK")

		fullTxs = append(fullTxs, model.FullReconTransaction{ //TODO: this type is not good for this rule
			Transaction:          tx,
			ReconciliationStatus: reconStatus, // check if no shenanigans with the loop on reconStatus
		})
	}

	return fullTxs, nil
}

func (s Store) GetPaymentAndTransactionPayOut(ctx context.Context, pspIdPath string) ([]model.PaymentReconciliation, error) {
	coll := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.CollPayments)

	cursor, err := coll.Aggregate(ctx,
		[]any{
			bson.M{"$match": bson.M{"type": "payout"}},
			bson.M{
				"$lookup": bson.M{
					"from":         "LedgerStuff",
					"localField":   "reference",
					"foreignField": pspIdPath,
					"as":           "transaction_ledger",
				},
			},
			bson.M{
				"$match": bson.M{
					"transaction_ledger": []any{
						bson.M{"$exists": true},
						bson.M{"$ne": bson.M{}},
					},
				},
			},
			bson.M{"$match": bson.M{"transaction_ledger.metadata.type": "payout"}},
		})
	if err != nil {
		return []model.PaymentReconciliation{}, fmt.Errorf(
			"could not aggregate payments and transactions for the pay-in lookup: %w", err)
	}

	var res []model.PaymentReconciliation

	for cursor.Next(ctx) {
		var agg model.PaymentReconciliation
		if err := bson.Unmarshal(cursor.Current, &agg); err != nil {
			return []model.PaymentReconciliation{}, fmt.Errorf(
				"could not unmarshal payment to bson: %w", err)
		}

		res = append(res, agg)
	}

	if err := cursor.Err(); err != nil {
		return []model.PaymentReconciliation{}, fmt.Errorf(
			"something went wrong while going through mongo cursor: %w", err)
	}

	return res, nil
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

func (s Store) UpdatePayoutStatus(ctx context.Context, agg model.PaymentReconciliation, status model.ReconciliationStatus) ([]model.FullReconTransaction, error) {
	db := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag))
	collLedger := db.Collection(constants.CollLedger)
	collPayments := db.Collection(constants.CollPayments)

	var payouts []model.FullReconTransaction

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

		payouts = append(payouts, model.FullReconTransaction{
			Transaction:          tx,
			ReconciliationStatus: model.Statuses{"payout": status},
		})
	}

	return payouts, nil
}

func (s Store) Close(ctx context.Context) error {
	if s.client == nil {
		return nil
	}

	return s.client.Disconnect(ctx)
}

var indexes = map[string][]mongo.IndexModel{
	constants.CollPayments: {
		{
			Keys: bsonx.Doc{
				bsonx.Elem{
					Key:   "provider",
					Value: bsonx.Int32(1),
				},
				bsonx.Elem{
					Key:   "reference",
					Value: bsonx.Int32(1),
				},
				bsonx.Elem{
					Key:   "type",
					Value: bsonx.Int32(1),
				},
			},
			Options: options.Index().SetUnique(true).SetName("identifier"),
		},
		{
			Keys: bsonx.Doc{
				bsonx.Elem{
					Key:   "provider",
					Value: bsonx.Int32(1),
				},
			},
			Options: options.Index().SetName("provider"),
		},
		{
			Keys: bsonx.Doc{
				bsonx.Elem{
					Key:   "type",
					Value: bsonx.Int32(1),
				},
			},
			Options: options.Index().SetName("payment-type"),
		},
		{
			Keys: bsonx.Doc{
				bsonx.Elem{
					Key:   "reference",
					Value: bsonx.Int32(1),
				},
			},
			Options: options.Index().SetName("payment-reference"),
		},
	},
	// TODO: add indexes for ledger collection (mostly meta stuff)
}

type StoredIndex struct {
	Unique                  bool               `bson:"unique"`
	Key                     bsonx.Doc          `bson:"key"`
	Name                    string             `bson:"name"`
	PartialFilterExpression interface{}        `bson:"partialFilterExpression"`
	ExpireAfterSeconds      int32              `bson:"expireAfterSeconds"`
	Collation               *options.Collation `bson:"collation"`
}

func (s Store) CreateIndexes(ctx context.Context) error {
	db := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag))

	for entity, indexes := range indexes {
		c := db.Collection(entity)
		listCursor, err := c.Indexes().List(ctx)
		if err != nil {
			return err
		}

		storedIndexes := make([]StoredIndex, 0)
		err = listCursor.All(ctx, &storedIndexes)
		if err != nil {
			return err
		}

	l:
		for _, storedIndex := range storedIndexes {
			if storedIndex.Name == "_id_" {
				continue l
			}
			for _, index := range indexes {
				if *index.Options.Name != storedIndex.Name {
					continue
				}
				var modified bool
				if !reflect.DeepEqual(index.Keys, storedIndex.Key) {
					fmt.Printf("Keys of index %s of collection %s modified\r\n", *index.Options.Name, entity)
					modified = true
				}
				if (index.Options.PartialFilterExpression == nil && storedIndex.PartialFilterExpression != nil) ||
					(index.Options.PartialFilterExpression != nil && storedIndex.PartialFilterExpression == nil) ||
					!reflect.DeepEqual(index.Options.PartialFilterExpression, storedIndex.PartialFilterExpression) {
					fmt.Printf("PartialFilterExpression of index %s of collection %s modified\r\n", *index.Options.Name, entity)
					modified = true
				}
				if (index.Options.Unique == nil && storedIndex.Unique) || (index.Options.Unique != nil && *index.Options.Unique != storedIndex.Unique) {
					fmt.Printf("Uniqueness of index %s of collection %s modified\r\n", *index.Options.Name, entity)
					modified = true
				}
				if (index.Options.ExpireAfterSeconds == nil && storedIndex.ExpireAfterSeconds > 0) || (index.Options.ExpireAfterSeconds != nil && *index.Options.ExpireAfterSeconds != storedIndex.ExpireAfterSeconds) {
					fmt.Printf("ExpireAfterSeconds of index %s of collection %s modified\r\n", *index.Options.Name, entity)
					modified = true
				}
				if (index.Options.Collation == nil && storedIndex.Collation != nil) ||
					(index.Options.Collation != nil && storedIndex.Collation == nil) ||
					!reflect.DeepEqual(index.Options.Collation, storedIndex.Collation) {
					fmt.Printf("Collation of index %s of collection %s modified\r\n", *index.Options.Name, entity)
					modified = true
				}
				if !modified {
					fmt.Printf("Index %s of collection %s not modified\r\n", *index.Options.Name, entity)
					continue l
				}

				fmt.Printf("Recreate index %s on collection %s\r\n", *index.Options.Name, entity)
				_, err = c.Indexes().DropOne(ctx, storedIndex.Name)
				if err != nil {
					fmt.Printf("Unable to drop index %s of collection %s: %s\r\n", *index.Options.Name, entity, err)
					continue l
				}

				_, err = c.Indexes().CreateOne(ctx, index)
				if err != nil {
					fmt.Printf("Unable to create index %s of collection %s: %s\r\n", *index.Options.Name, entity, err)
					continue l
				}
			}
		}

		// Check for deleted index
	l3:
		for _, storedIndex := range storedIndexes {
			if storedIndex.Name == "_id_" {
				continue l3
			}
			for _, index := range indexes {
				if *index.Options.Name == storedIndex.Name {
					continue l3
				}
			}
			fmt.Printf("Detected deleted index %s on collection %s\r\n", storedIndex.Name, entity)
			_, err = c.Indexes().DropOne(ctx, storedIndex.Name)
			if err != nil {
				fmt.Printf("Unable to drop index %s of collection %s: %s\r\n", storedIndex.Name, entity, err)
			}
		}

		// Check for new indexes to create
	l2:
		for _, index := range indexes {
			for _, storedIndex := range storedIndexes {
				if *index.Options.Name == storedIndex.Name {
					continue l2
				}
			}
			fmt.Printf("Create new index %s on collection %s\r\n", *index.Options.Name, entity)
			_, err = c.Indexes().CreateOne(ctx, index)
			if err != nil {
				fmt.Printf("Unable to create index %s of collection %s: %s\r\n", *index.Options.Name, entity, err)
			}
		}
	}

	return nil
}
