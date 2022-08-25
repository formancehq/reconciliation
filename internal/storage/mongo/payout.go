package mongo

import (
	"context"
	"fmt"
	"github.com/numary/reconciliation/internal/model"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func unmarshalPaymentsFromMongo(ctx context.Context, cursor *mongo.Cursor) (model.PaymentReconciliation, error) {
	var res []model.PaymentReconciliation
	fmt.Println("WHAAAAAT")
	for cursor.Next(ctx) {
		fmt.Println("WHAAAAAT 2")
		var agg model.PaymentReconciliation
		if err := bson.Unmarshal(cursor.Current, &agg); err != nil {
			return model.PaymentReconciliation{}, fmt.Errorf(
				"could not unmarshal payment to bson: %w", err)
		}

		res = append(res, agg)
	}

	if err := cursor.Err(); err != nil {
		return model.PaymentReconciliation{}, fmt.Errorf(
			"something went wrong while going through mongo cursor: %w", err)
	}

	if len(res) > 1 {
		return model.PaymentReconciliation{}, errors.New("should not return more than one payment")
	}

	if res == nil {
		return model.PaymentReconciliation{}, errors.New("no result returned")
	}

	return res[0], nil
}
