package reconciliation

import (
	ledgerclient "github.com/numary/numary-sdk-go"
	payments "github.com/numary/payments/pkg"
)

type payInReconciliation struct {
	ID               string `bson:"_id"`
	payments.Payment `bson:",inline"`
	Transactions     []ledgerclient.Transaction `bson:"transaction_ledger"`
}
