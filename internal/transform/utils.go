package transform

import (
	"time"

	ledgerclient "github.com/numary/numary-sdk-go"
	"github.com/numary/reconciliation/internal/model"
)

var TxMongoID = "TX_MONGO_ID"
var TxRef = "TX_REFERENCE"
var TxID = int64(12345)
var TxAmount = int64(4200)
var FloatTxAmount = float32(TxAmount)
var NegFloatTxAmount = float32(-TxAmount)
var TxAsset = "EUR"
var TxDest = "DEST"
var TxSource = "SOURCE"
var PreTxBalance = float32(0.0)
var PostTxBalance = float32(0.0)
var TxMetaType = "PAYOUT"
var TxCreateDate = time.Now()

var txMetadata = map[string]interface{}{
	"test_1": "value_1",
	"test_2": "value_2",
	"type":   TxMetaType,
}

var txPostings = []ledgerclient.Posting{
	{
		Amount:      int32(TxAmount),
		Asset:       TxAsset,
		Destination: TxDest,
		Source:      TxSource,
	},
}

var TxPreCommitVolumes = map[string]map[string]ledgerclient.Volume{
	TxSource: {
		TxAsset: ledgerclient.Volume{
			Input:   PreTxBalance,
			Output:  PostTxBalance,
			Balance: nil,
		},
	},
	TxDest: {
		TxAsset: ledgerclient.Volume{
			Input:   PreTxBalance,
			Output:  PreTxBalance,
			Balance: &PreTxBalance,
		},
	},
}

var TxPostCommitVolumes = map[string]map[string]ledgerclient.Volume{
	TxSource: {
		TxAsset: ledgerclient.Volume{
			Input:   PreTxBalance,
			Output:  PostTxBalance,
			Balance: &NegFloatTxAmount,
		},
	},
	TxDest: {
		TxAsset: ledgerclient.Volume{
			Input:   float32(TxAmount),
			Output:  PostTxBalance,
			Balance: &FloatTxAmount,
		},
	},
}

var LedgerTransaction = ledgerclient.Transaction{
	Postings:          txPostings,
	Reference:         &TxRef,
	Metadata:          txMetadata,
	Timestamp:         TxCreateDate,
	Txid:              int32(TxID),
	PreCommitVolumes:  &TxPreCommitVolumes,
	PostCommitVolumes: &TxPostCommitVolumes,
}

var TxStatus = model.ReconciliationStatus{
	Status:   "TEST_STATUS",
	Message:  "TEST_MESSAGE",
	Code:     0,
	LinkedID: "EXTERNAL_ID",
}

var TxReconStatus = model.Statuses{
	"test": TxStatus,
}

var ReconOldBalance = map[string]map[string]int64{
	TxSource: {TxAsset: 0},
	TxDest:   {TxAsset: 0},
}

var ReconNewBalance = map[string]map[string]int64{
	TxSource: {TxAsset: -TxAmount},
	TxDest:   {TxAsset: TxAmount},
}
