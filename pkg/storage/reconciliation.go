package storage

import (
	ledgerclient "github.com/numary/numary-sdk-go"
)

//TODO: think about internal ?
type ReconciliationStatus struct {
	Status     string `bson:"status"`
	Message    string `bson:"message"`
	Code       int64  `bson:"code"`
	ExternalID string `bson:"external_id"`
}

type Statuses map[string]ReconciliationStatus

type FullReconTransaction struct {
	ledgerclient.Transaction `bson:",inline"`
	ID                       string   `bson:"_id"`
	ReconciliationStatus     Statuses `bson:"reconciliation_status,"`
}
