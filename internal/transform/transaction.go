package transform

import (
	"fmt"

	"github.com/numary/reconciliation/internal/model"
	"github.com/pkg/errors"
)

func FullTxToPaymentReconciliation(tx model.FullReconTransaction) (model.ReconTransaction, error) {
	var reconPostings []model.ReconPosting

	oldBalances := make(map[string]map[string]int64)
	newBalances := make(map[string]map[string]int64)

	for _, posting := range tx.Postings {
		reconPostings = append(reconPostings, model.ReconPosting{
			Source:      posting.Source,
			Destination: posting.Destination,
			Amount:      int64(posting.Amount),
			Asset:       posting.Asset,
		})

		if tx.PreCommitVolumes == nil || tx.PostCommitVolumes == nil {
			return model.ReconTransaction{}, errors.New("missing pre/post commit volumes")
		}

		for accountKey, account := range *tx.PreCommitVolumes {
			for assetKey, volume := range account {
				oldBalances[accountKey] = make(map[string]int64)
				if volume.Balance != nil {
					oldBalances[accountKey][assetKey] = int64(*volume.Balance)
				} else {
					oldBalances[accountKey][assetKey] = int64(0)
				}
			}
		}

		for accountKey, account := range *tx.PostCommitVolumes {
			for assetKey, volume := range account {
				newBalances[accountKey] = make(map[string]int64)
				if volume.Balance != nil {
					newBalances[accountKey][assetKey] = int64(*volume.Balance)
				} else {
					newBalances[accountKey][assetKey] = int64(0)
				}
			}
		}
	}

	return model.ReconTransaction{
		ID:           int64(tx.Txid),
		Postings:     reconPostings,
		PaymentIDs:   nil, // no payments in end_to_end rules
		CreationDate: tx.Timestamp,
		ReconStatus:  tx.ReconciliationStatus,
		Type:         fmt.Sprintf("%s", tx.Metadata["type"]),
		OldBalances:  oldBalances,
		NewBalances:  newBalances,
	}, nil
}
