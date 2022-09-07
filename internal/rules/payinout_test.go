package rules

import (
	"context"
	"encoding/json"
	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/internal/env"
	"github.com/spf13/pflag"
	"testing"
	"time"

	"github.com/numary/reconciliation/internal/model"
	"github.com/numary/reconciliation/internal/storage/mongo"
	"github.com/stretchr/testify/require"
)

func TestPayinoutAccept(t *testing.T) {

	rule := PayInOut{
		Rule: model.Rule{
			Name:          "RULE_TEST",
			Configuration: map[string]string{"psp_id_path": "metadata.psp_id"},
		},
	}

	t.Run("event ok", func(t *testing.T) {
		eventOK := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_PAYMENT",
			Payload: map[string]any{"reference": "PSP_ID_TEST_1234"},
		}

		require.True(t, rule.Accept(context.Background(), eventOK))
	})

	t.Run("event ko", func(t *testing.T) {
		eventNOK := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_PAYMENT",
			Payload: map[string]any{"metadata": "PSP_ID_TEST_1234"},
		}

		require.False(t, rule.Accept(context.Background(), eventNOK))
	})

	t.Run("event empty", func(t *testing.T) {
		eventEmpty := model.Event{}

		require.False(t, rule.Accept(context.Background(), eventEmpty))
	})
}

func TestPayinoutReconciliate(t *testing.T) {

	flagSet := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, env.Init(flagSet))

	store, err := mongo.NewStore()
	if err != nil {
		panic(err)
	}

	require.NoError(t, store.RemoveAllForTests(context.Background(), constants.CollLedger))
	require.NoError(t, store.RemoveAllForTests(context.Background(), constants.CollPayments))

	rule := PayInOut{
		Rule: model.Rule{
			Name:          "pay-in",
			Configuration: map[string]string{"psp_id_path": "metadata.psp_id"},
		},
	}

	// reconciliation ok
	t.Run("reconciliate pay-in OK", func(t *testing.T) {
		txPayload := make(map[string]any)
		require.NoError(t, json.Unmarshal([]byte(validTransactionTest), &txPayload))

		paymentPayload := make(map[string]any)
		require.NoError(t, json.Unmarshal([]byte(validPaymentTest), &paymentPayload))

		eventPayment := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_PAYMENT",
			Payload: paymentPayload,
		}
		require.NoError(t, store.InsertObject(context.Background(), eventPayment, constants.CollPayments))

		eventTx := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_TRANSACTIONS",
			Payload: txPayload,
		}

		require.NoError(t, store.InsertObject(context.Background(), eventTx, constants.CollLedger))

		status, err := rule.reconciliate(context.Background(), store, eventPayment)
		require.Nil(t, err)
		require.Equal(t, "success", status.Status)
	})

	// no matching reference
	t.Run("reconciliate pay-in no result found", func(t *testing.T) {
		paymentPayload := make(map[string]any)
		require.NoError(t, json.Unmarshal([]byte(wrongPaymentTest), &paymentPayload))

		eventPayment := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_PAYMENT",
			Payload: paymentPayload,
		}

		require.NoError(t, store.InsertObject(context.Background(), eventPayment, constants.CollPayments))
		_, err := rule.reconciliate(context.Background(), store, eventPayment)
		require.Error(t, err)
	})

	// bad amount
	t.Run("reconciliate pay-in bad amount", func(t *testing.T) {
		paymentPayload := make(map[string]any)
		require.NoError(t, json.Unmarshal([]byte(badAmountPaymentTest), &paymentPayload))

		txPayload := make(map[string]any)
		require.NoError(t, json.Unmarshal([]byte(badAmountTransactionTest), &txPayload))

		eventPayment := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_PAYMENT",
			Payload: paymentPayload,
		}
		require.NoError(t, store.InsertObject(context.Background(), eventPayment, constants.CollPayments))

		eventTx := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_TRANSACTIONS",
			Payload: txPayload,
		}

		require.NoError(t, store.InsertObject(context.Background(), eventTx, constants.CollLedger))
		status, err := rule.reconciliate(context.Background(), store, eventPayment)
		require.Nil(t, err)
		require.Equal(t, "amount mismatch", status.Message)
	})
}

var validPaymentTest = `{
    "provider": "stripe",
    "reference": "ch_formancebg1234wtflol2ez4rtz",
    "type": "pay-in",
    "asset": "EUR/2",
    "createdAt":"2022-07-06T10:02:10.000+00:00",
    "initialAmount":6295,
    "scheme": "visa",
    "status": "succeeded"
}`

var wrongPaymentTest = `{
    "provider": "stripe",
    "reference": "xxxxxxxx",
    "type": "pay-in",
    "asset": "EUR/2",
    "createdAt":"2022-07-06T10:02:10.000+00:00",
    "initialAmount":6295,
    "scheme": "visa",
    "status": "succeeded"
}`

var badAmountPaymentTest = `{
    "provider": "stripe",
    "reference": "ch_formancebg1234wtflol2ez4rtz2",
    "type": "pay-in",
    "asset": "EUR/2",
    "createdAt":"2022-07-06T10:02:10.000+00:00",
    "initialAmount":6294,
    "scheme": "visa",
    "status": "succeeded"
}`

var validTransactionTest = `{
      "postings": [
        {
          "amount": 6295,
          "asset": "EUR/2",
          "destination": "order:ds2aJMo",
          "source": "world"
        }
      ],
      "reference": "txn_3LIRpBFqW03ZYiNn1qxuWtFT",
      "metadata": {
        "provider": "stripe",
        "psp_id": "ch_formancebg1234wtflol2ez4rtz",
        "type": "charge",
        "user_id": "330596",
        "bo_link": "http://bo.xxxxx.com/#!/clients/orders/edit/xxxxx",
        "internal_fraud_scoring": "fraud_action_trust",
        "order_id": "ds2aJMo"
      },
      "timestamp": "2022-07-27T12:52:16.000+00:00",
      "txid": 1,
      "precommitvolumes": {
        "order:ds2aJMo": {
          "EUR/2": {
            "input": 0,
            "output": 0,
            "balance": 0
          }
        },
        "world": {
          "EUR/2": {
            "input": 0,
            "output": 0,
            "balance": 0
          }
        }
      },
      "postcommitvolumes": {
        "order:ds2aJMo": {
          "EUR/2": {
            "input": 6295,
            "output": 0,
            "balance": 6295
          }
        },
        "world": {
          "EUR/2": {
            "input": 0,
            "output": 6295,
            "balance": -6295
          }
        }
      }
}`

var badAmountTransactionTest = `{
      "postings": [
        {
          "amount": 6295,
          "asset": "EUR/2",
          "destination": "order:ds2aJMo",
          "source": "world"
        }
      ],
      "reference": "txn_3LIRpBFqW03ZYiNn1qxuWtFT",
      "metadata": {
        "provider": "stripe",
        "psp_id": "ch_formancebg1234wtflol2ez4rtz2",
        "type": "charge",
        "user_id": "330596",
        "bo_link": "http://bo.xxxxx.com/#!/clients/orders/edit/xxxxx",
        "internal_fraud_scoring": "fraud_action_trust",
        "order_id": "ds2aJMo"
      },
      "timestamp": "2022-07-27T12:52:16.000+00:00",
      "txid": 1,
      "precommitvolumes": {
        "order:ds2aJMo": {
          "EUR/2": {
            "input": 0,
            "output": 0,
            "balance": 0
          }
        },
        "world": {
          "EUR/2": {
            "input": 0,
            "output": 0,
            "balance": 0
          }
        }
      },
      "postcommitvolumes": {
        "order:ds2aJMo": {
          "EUR/2": {
            "input": 6295,
            "output": 0,
            "balance": 6295
          }
        },
        "world": {
          "EUR/2": {
            "input": 0,
            "output": 6295,
            "balance": -6295
          }
        }
      }
}`
