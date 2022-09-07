package model

import (
	"time"

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
	//ID                       string   `bson:"_id"`
	ReconciliationStatus Statuses `bson:"reconciliation_status,"`
}

type ReconTransaction struct {
	ID           int64
	Postings     []ReconPosting
	PaymentIDs   *[]string
	CreationDate time.Time
	ReconStatus  Statuses
	Type         string // Enum ? Pay-in Payout Internal Refund
	OldBalances  map[string]map[string]int64
	NewBalances  map[string]map[string]int64
}

type ReconPosting struct {
	Source      string
	Destination string
	Amount      int64
	Asset       string
}

type PayInReconciliation struct {
	Type      string // Enum ? Pay-in Payout Internal Refund
	PaymentID string
}

// End To End
type EndToEndReconciliation struct {
	Type         string
	Transactions []ReconTransaction
	Status       ReconciliationStatus
}
type EndToEndTransaction struct {
	TxID   string
	Amount int64
	Status Statuses
}

type Rules map[string]Rule[]

type Rule[CONFIG any] struct {
	Name          string
	Active        bool
	Configuration CONFIG
}

//type PayinRule struct {
//	Name      string
//	PspIDPath string `bson:"psp_id_path"`
//	Active    bool
//}
