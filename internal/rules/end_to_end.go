package rules

import (
	"context"
	"fmt"
	"sort"

	"github.com/numary/reconciliation/internal/model"
)

func ReconciliateEndToEnd(ctx context.Context, txs model.LedgerTransactions) (map[string]int32, error) {

	// sort transactions by timestamp so we are sure balances are coherents
	sort.Slice(txs.Transactions, func(i, j int) bool {
		return txs.Transactions[i].Timestamp.Before(txs.Transactions[j].Timestamp)
	})
	badBalance := make(map[string]int32)
	for _, tx := range txs.Transactions {
		for keyAccount, elemAccount := range *tx.PostCommitVolumes {
			if keyAccount == "world" { // need a list of SkippAccounts
				continue
			}
			for assetKey, elemVolume := range elemAccount {
				if *elemVolume.Balance != float32(0.0) {
					// if balance is not at 0, we create an entry on the map
					badBalance[assetKey] = tx.Txid //TODO: account ?
				} else {
					// if balance is at 0, we remove the entry from the map
					delete(badBalance, assetKey)
				}
			}
		}
	}

	if len(badBalance) > 0 {
		fmt.Printf("%v - %s\n", badBalance, txs.Transactions[0].Metadata["order_id"])
	} else {
		fmt.Println("success")
	}

	return badBalance, nil
}
