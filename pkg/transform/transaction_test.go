package transform

import (
	"testing"

	"github.com/numary/reconciliation/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestTransformFullTransactionToLightTransaction(t *testing.T) {

	tx := model.FullReconTransaction{
		Transaction:          LedgerTransaction,
		ID:                   TxMongoID,
		ReconciliationStatus: TxReconStatus,
	}

	t.Run("success", func(t *testing.T) {

		result, err := FullTxToPaymentReconciliation(tx)

		require.Nil(t, err)

		require.Equal(t, TxMetaType, result.Type)
		require.Equal(t, TxID, result.ID)
		require.Equal(t, TxCreateDate, result.CreationDate)
		require.Nil(t, result.PaymentIDs)
		require.Equal(t, TxReconStatus, result.ReconStatus)
		require.Equal(t, []model.ReconPosting{
			{
				Source:      TxSource,
				Destination: TxDest,
				Amount:      TxAmount,
				Asset:       TxAsset,
			},
		}, result.Postings)
		require.Equal(t, ReconOldBalance, result.OldBalances)
		require.Equal(t, ReconNewBalance, result.NewBalances)
	})

	t.Run("failures", func(t *testing.T) {

		newLedgerTx := LedgerTransaction
		newLedgerTx.PreCommitVolumes = nil

		tx := model.FullReconTransaction{
			Transaction:          newLedgerTx,
			ID:                   TxMongoID,
			ReconciliationStatus: TxReconStatus,
		}

		result, err := FullTxToPaymentReconciliation(tx)

		require.NotNil(t, err)
		require.Empty(t, result)

		newLedgerTx.PreCommitVolumes = &TxPreCommitVolumes
		newLedgerTx.PostCommitVolumes = nil

		result, err = FullTxToPaymentReconciliation(tx)

		require.NotNil(t, err)
		require.Empty(t, result)
	})
}
