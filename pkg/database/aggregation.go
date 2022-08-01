package database

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func GetTransactionsWithOrder(ctx context.Context, db *mongo.Database) (*mongo.Cursor, error) {
	coll := db.Collection(CollLedger)
	cursor, err := coll.Aggregate(ctx,
		bson.A{
			bson.D{
				{"$group",
					bson.D{
						{"_id", "$metadata.order_id"},
						{"transactions", bson.D{{"$push", "$$ROOT"}}},
					},
				},
			},
		},
		options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		fmt.Println("error: could not aggregate transactions by order_id")
	}
	return cursor, err
}

func GetPaymentAndTransactionPayIn(ctx context.Context, db *mongo.Database) (*mongo.Cursor, error) {
	coll := db.Collection(CollPayments)
	cursor, err := coll.Aggregate(ctx,
		bson.A{
			bson.D{{"$match", bson.D{{"type", "pay-in"}}}},
			bson.D{{"$match", bson.D{{"reconciliation_status.pay-in.status", "failure"}}}},
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
		fmt.Println("error: could not aggregate payments and transactions for the pay-in lookup")
	}
	return cursor, err
}

func GetPaymentAndTransactionPayOut(ctx context.Context, db *mongo.Database) (*mongo.Cursor, error) {
	coll := db.Collection(CollPayments)
	cursor, err := coll.Aggregate(ctx,
		bson.A{
			bson.D{{"$match", bson.D{{"type", "payout"}}}},
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
			bson.D{{"$match", bson.D{{"transaction_ledger.metadata.type", "payout"}}}},
		})
	if err != nil {
		fmt.Println("error: could not aggregate payments and transactions for the pay-in lookup")
	}
	return cursor, err
}
