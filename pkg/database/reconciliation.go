package database

import (
	ledgerclient "github.com/numary/numary-sdk-go"
)

//// for now we only get ledger tx, might need to improve it to also get payments if we want to do a better job
//func GetReconciliationFailures(ctx context.Context, db *mongo.Database) (*mongo.Cursor, error) {
//	cursor, err := db.Collection(CollLedger).Find(ctx, bson.M{"reconciliation_status.end-to-end.status": "failure"})
//	if err != nil {
//		fmt.Println("could not fetch end-to-end failures")
//	}
//
//	return cursor, err
//}

type Status struct {
	Status     string `bson:"status"`
	Message    string `bson:"message"`
	Code       int64  `bson:"code"`
	ExternalID string `bson:"external_id"`
}

type Statuses map[string]Status

type FullReconTransaction struct {
	ledgerclient.Transaction `bson:",inline"`
	ID                       string   `bson:"_id"`
	ReconciliationStatus     Statuses `bson:"reconciliation_status,"`
}
