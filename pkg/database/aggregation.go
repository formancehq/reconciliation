package database

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func GetTransactionsWithOrder(ctx context.Context, db *mongo.Database, flowIdPath string) (*mongo.Cursor, error) {
	coll := db.Collection(CollLedger)
	cursor, err := coll.Aggregate(ctx,
		[]any{
			bson.M{"$group": bson.M{
				"_id":          fmt.Sprintf("$%s", flowIdPath),
				"transactions": bson.M{"$push": "$$ROOT"}},
			},
		},
		options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		fmt.Println("error: could not aggregate transactions by order_id")
	}
	return cursor, err
}

func GetPaymentAndTransactionPayIn(ctx context.Context, db *mongo.Database, pspIdPath string) (*mongo.Cursor, error) {
	coll := db.Collection(CollPayments)
	cursor, err := coll.Aggregate(ctx,
		[]any{
			bson.M{"$match": bson.M{"type": "pay-in"}},
			bson.M{"$match": bson.M{"reconciliation_status.pay-in.status": "failure"}},
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
		})
	if err != nil {
		fmt.Println("error: could not aggregate payments and transactions for the pay-in lookup")
	}
	return cursor, err
}

func GetPaymentAndTransactionPayOut(ctx context.Context, db *mongo.Database, pspIdPath string) (*mongo.Cursor, error) {
	coll := db.Collection(CollPayments)
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
		fmt.Println("error: could not aggregate payments and transactions for the pay-in lookup")
	}
	return cursor, err
}
