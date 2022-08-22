package transform

import (
	"github.com/stretchr/testify/require"
	"testing"

	"github.com/numary/reconciliation/pkg/storage"
	"github.com/numary/reconciliation/pkg/testutils"
)

func TestTransformFullTransactionToLightTransaction(t *testing.T) {

	tx := storage.FullReconTransaction{
		Transaction:          testutils.LedgerTransaction,
		ID:                   testutils.TxMongoID,
		ReconciliationStatus: testutils.TxReconStatus,
	}

	t.Run("success", func(t *testing.T) {

		result, err := FullTxToPaymentReconciliation(tx)

		require.Nil(t, err)

		require.Equal(t, testutils.TxMetaType, result.Type)
		require.Equal(t, testutils.TxID, result.ID)
		require.Equal(t, testutils.TxCreateDate, result.CreationDate)
		require.Nil(t, result.PaymentIDs)
		require.Equal(t, testutils.TxReconStatus, result.ReconStatus)
		require.Equal(t, []ReconPosting{
			{
				Source:      testutils.TxSource,
				Destination: testutils.TxDest,
				Amount:      testutils.TxAmount,
				Asset:       testutils.TxAsset,
			},
		}, result.Postings)
		require.Equal(t, testutils.ReconOldBalance, result.OldBalances)
		require.Equal(t, testutils.ReconNewBalance, result.NewBalances)
	})

	t.Run("failures", func(t *testing.T) {

		newLedgerTx := testutils.LedgerTransaction
		newLedgerTx.PreCommitVolumes = nil

		tx := storage.FullReconTransaction{
			Transaction:          newLedgerTx,
			ID:                   testutils.TxMongoID,
			ReconciliationStatus: testutils.TxReconStatus,
		}

		result, err := FullTxToPaymentReconciliation(tx)

		require.NotNil(t, err)
		require.Empty(t, result)

		newLedgerTx.PreCommitVolumes = &testutils.TxPreCommitVolumes
		newLedgerTx.PostCommitVolumes = nil

		result, err = FullTxToPaymentReconciliation(tx)

		require.NotNil(t, err)
		require.Empty(t, result)
	})
}
