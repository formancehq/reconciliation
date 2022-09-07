package mongo

import (
	"context"
	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/internal/model"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/bson"
)

func (s Store) GetTransaction(ctx context.Context, id int64) (model.FullReconTransaction, error) {
	var tx model.FullReconTransaction

	coll := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.CollLedger)

	result := coll.FindOne(ctx, bson.M{"txid": id})

	err := result.Decode(tx)
	if err != nil {
		//TODO: log
		return model.FullReconTransaction{}, err
	}

	return tx, nil
}
