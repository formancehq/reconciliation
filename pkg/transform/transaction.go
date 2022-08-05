package transform

import (
	"fmt"
	"github.com/numary/reconciliation/pkg/database"
	"time"
)

type ReconTransaction struct {
	ID           int64
	Postings     []ReconPosting
	PaymentIDs   []string
	CreationDate time.Time
	ReconStatus  database.Statuses
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

func MongoTxToReconciliation(tx database.FullReconTransaction) ReconTransaction {
	var reconPostings []ReconPosting

	oldBalances := make(map[string]map[string]int64)
	newBalances := make(map[string]map[string]int64)

	for _, posting := range tx.Postings {
		reconPostings = append(reconPostings, ReconPosting{
			Source:      posting.Source,
			Destination: posting.Destination,
			Amount:      int64(posting.Amount),
			Asset:       posting.Asset,
		})

		// TODO: test this
		for accountKey, account := range *tx.PreCommitVolumes {
			for assetKey, volume := range account {
				oldBalances[accountKey] = make(map[string]int64)
				oldBalances[accountKey][assetKey] = int64(*volume.Balance)
			}
		}

		// TODO: test that
		for accountKey, account := range *tx.PostCommitVolumes {
			for assetKey, volume := range account {
				newBalances[accountKey] = make(map[string]int64)
				newBalances[accountKey][assetKey] = int64(*volume.Balance)
			}
		}
	}

	return ReconTransaction{
		ID:           int64(tx.Txid),
		Postings:     reconPostings,
		PaymentIDs:   nil,
		CreationDate: tx.Timestamp,
		ReconStatus:  tx.ReconciliationStatus,
		Type:         fmt.Sprintf("%s", tx.Metadata["type"]),
		OldBalances:  oldBalances,
		NewBalances:  newBalances,
	}
}
