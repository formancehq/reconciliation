package transform

import (
	"github.com/numary/reconciliation/pkg/storage"
	"time"
)

type ReconTransaction struct {
	ID           int64
	Postings     []ReconPosting
	PaymentIDs   *[]string
	CreationDate time.Time
	ReconStatus  storage.Statuses
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
	Status       storage.Statuses
}
type EndToEndTransaction struct {
	TxID   string
	Amount int64
	Status storage.Statuses
}
