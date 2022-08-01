package reconciliation

import (
	ledgerclient "github.com/numary/numary-sdk-go"
	payments "github.com/numary/payments/pkg"
)

type Statuses map[string]Status

type Status struct {
	Status  string `bson:"status"`
	Message string `bson:"message"`
	Code    int64  `bson:"code"`
}

type payInReconciliation struct {
	ID               string `bson:"_id"`
	payments.Payment `bson:",inline"`
	Transactions     []ledgerclient.Transaction `bson:"transaction_ledger"`
}
