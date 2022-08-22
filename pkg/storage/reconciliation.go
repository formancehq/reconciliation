package storage

import (
	ledgerclient "github.com/numary/numary-sdk-go"
	payments "github.com/numary/payments/pkg"
)

type LedgerTransactions struct {
	Transactions []ledgerclient.Transaction `bson:"transactions"`
}

type PaymentReconciliation struct {
	ID               string `bson:"_id"`
	payments.Payment `bson:",inline"`
	Transactions     []ledgerclient.Transaction `bson:"transaction_ledger"`
}

// TODO: think about internal ?
type ReconciliationStatus struct {
	Status   string `bson:"status"`
	Message  string `bson:"message"`
	Code     int64  `bson:"code"`
	LinkedID string `bson:"linked_id"`
}

type Statuses map[string]ReconciliationStatus

type FullReconTransaction struct {
	ledgerclient.Transaction `bson:",inline"`
	ID                       string   `bson:"_id"`
	ReconciliationStatus     Statuses `bson:"reconciliation_status,"`
}
