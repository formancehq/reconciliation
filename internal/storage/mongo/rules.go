package mongo

import (
	"context"
	"fmt"
	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/internal/model"
	"github.com/numary/reconciliation/internal/rules"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// see if we'd rather have a struct for each struct ?
func (s Store) GetRules(ctx context.Context) (model.Rules, error) {

	coll := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.CollRules)

	cursor, err := coll.Find(ctx, bson.D{})
	if err != nil {
		return model.Rules{}, fmt.Errorf(
			"could not find rules with error: %w", err)
	}

	return unmarshalRulesFromMongo(ctx, cursor)
}

func (s Store) GetRule(ctx context.Context, name string) (model.Rule[any], error) {

	coll := s.client.
		Database(viper.GetString(constants.StorageMongoDatabaseNameFlag)).
		Collection(constants.CollRules)

	result := coll.FindOne(ctx, bson.M{"name": name})

	switch name {
	case "end-to-end":
		var rule model.Rule[rules.EndToEndRule]
		if err := result.Decode(&rule); err != nil {
			fmt.Println("could not decode mongodb result for end-to-end rule")
			return model.Rule[any]{}, err
		}
		return rule, nil
	case "payin":
	case "payout":
	default:
		return model.Rule[any]{}, errors.New("this rule does not exist")
	}

	if err := result.Decode(&rule); err != nil {
		return model.Rule{}, fmt.Errorf(
			"could not unmarshal payment to bson: %w", err)
	}
}

func unmarshalRulesFromMongo(ctx context.Context, cursor *mongo.Cursor) (model.Rules, error) {
	var res model.Rules

	for cursor.Next(ctx) {
		var rule model.Rule
		if err := bson.Unmarshal(cursor.Current, &rule); err != nil {
			return model.Rules{}, fmt.Errorf(
				"could not unmarshal payment to bson: %w", err)
		}

		// this is not sexy and i know it, but it makes it easier to handle rules in the worker
		if _, ok := res[rule.Name]; !ok {
			res[rule.Name] = rule
		}
	}

	if err := cursor.Err(); err != nil {
		return model.Rules{}, fmt.Errorf(
			"something went wrong while going through mongo cursor: %w", err)
	}

	return res, nil
}
