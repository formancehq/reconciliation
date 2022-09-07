package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/numary/reconciliation/internal/storage"
	"github.com/pkg/errors"
	"github.com/tidwall/gjson"
	"sort"

	"github.com/numary/reconciliation/internal/model"
)

type EndToEndRule struct {
	FlowIdPath          string   `json:"flow_id_path"`
	BlacklistedAccounts []string `json:"blacklisted_accounts"`
}

func (p EndToEndRule) Accept(ctx context.Context, event model.Event) bool {
	//TODO get this value from the rule
	if ref, ok := event.Payload["flow_id"]; ok {
		if ref != "" {
			return true
		}
	}

	jsonStr, err := json.Marshal(event.Payload)
	if err != nil {
		fmt.Println("error during marshal")
		return false
	}

	return gjson.Get(string(jsonStr), p.FlowIdPath).Exists()
}

func (p EndToEndRule) reconciliate(ctx context.Context, store storage.Store, event model.Event) (model.ReconciliationStatus, error) {
	var status model.ReconciliationStatus

	jsonStr, err := json.Marshal(event.Payload)
	if err != nil {
		return status, errors.New("could not marshal event back into json")
	}

	flowID := gjson.Get(string(jsonStr), p.FlowIdPath).Str

	transactionFlow, err := store.GetTransactionsWithOrder(ctx, p.FlowIdPath, flowID)
	if err != nil {
		//TODO: log
		return status, err
	}

	for _, transactions := range transactionFlow {
		status, err = p.reconciliateEndToEnd(ctx, transactions)
		if err != nil {
			//TODO update status before returning so we know we tried something
			//TODO log
			return status, err
		}
	}

	//TODO: here we return the flow status, but we still need to send the TX only objet+status to search...
	return status, err
}

func (p EndToEndRule) Reconciliate(ctx context.Context, store storage.Store, event model.Event) error {
	_, err := p.reconciliate(ctx, store, event)

	return err
}

func (p EndToEndRule) reconciliateEndToEnd(ctx context.Context, txs model.LedgerTransactions) (model.ReconciliationStatus, error) {

	// sort transactions by timestamp so we are sure balances are coherents
	sort.Slice(txs.Transactions, func(i, j int) bool {
		return txs.Transactions[i].Timestamp.Before(txs.Transactions[j].Timestamp)
	})

	badBalance := make(map[string]map[string]int32)
	for _, tx := range txs.Transactions {
		for keyAccount, elemAccount := range *tx.PostCommitVolumes {

			if keyAccount == "world" { //TODO need a list of SkippAccounts
				continue
			}
			if _, ok := badBalance[keyAccount]; !ok {
				badBalance[keyAccount] = make(map[string]int32)
			}
			for assetKey, elemVolume := range elemAccount {
				if *elemVolume.Balance != float32(0.0) {
					// if balance is not at 0, we create an entry on the map
					badBalance[keyAccount][assetKey] = tx.Txid //TODO: account ?
				} else {
					// if balance is at 0, we remove the entry from the map
					delete(badBalance[keyAccount], assetKey)
					if len(badBalance[keyAccount]) == 0 {
						delete(badBalance, keyAccount)
					}
				}
			}
		}
	}

	if len(badBalance) > 0 {
		fmt.Printf("%v - %s\n", badBalance, txs.Transactions[0].Metadata["order_id"])
		//TODO: put this in a static var
		return model.EndToEndMismatchStatus, nil
	}
	return model.SuccessStatus, nil
}
