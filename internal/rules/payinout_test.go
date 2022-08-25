package rules

import (
	"context"
	"encoding/json"
	"github.com/numary/reconciliation/internal/env"
	"github.com/spf13/pflag"
	"testing"
	"time"

	"github.com/numary/reconciliation/internal/model"
	"github.com/numary/reconciliation/internal/storage/mongo"
	"github.com/stretchr/testify/require"
)

func TestAccept(t *testing.T) {

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

func TestReconciliate(t *testing.T) {

	flagSet := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, env.Init(flagSet))

	store, err := mongo.NewStore()
	if err != nil {
		panic(err)
	}

	rule := PayInOut{
		Rule: model.Rule{
			Name:          "pay-in",
			Configuration: map[string]string{"psp_id_path": "metadata.psp_id"},
		},
	}

	paymentPayload := make(map[string]any)
	err = json.Unmarshal([]byte(validPaymentTest), &paymentPayload)
	if err != nil {
		panic(err)
	}

	t.Run("reconciliate pay-in OK", func(t *testing.T) {
		eventPayment := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_PAYMENT",
			Payload: paymentPayload,
		}

		//err = store.InsertObject(context.Background(), eventPayment)
		//if err != nil {
		//	panic(err)
		//}

		require.Nil(t, rule.Reconciliate(context.Background(), store, eventPayment))
	})
}

var validPaymentTest = `{
    "provider": "stripe",
    "reference": "ch_3LIRpBFqW03ZYiNn1G7tFTgJ",
    "type": "pay-in",
    "asset": "EUR/2",
    "createdAt":1657101730000,
    "initialAmount":6295,
    "scheme": "visa",
    "status": "succeeded"
}`

var validTransactionTest = `{
  "transaction_ledger": [
    {
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
        "psp_id": "ch_3LIRpBFqW03ZYiNn1G7tFTgJ",
        "type": "charge",
        "user_id": "330596",
        "bo_link": "http://bo.brocantelab.com/#!/clients/orders/edit/ds2aJMo",
        "internal_fraud_scoring": "fraud_action_trust",
        "order_id": "ds2aJMo"
      },
      "timestamp": {
        "$date": {
          "$numberLong": "1658926336000"
        }
      },
      "txid": 0,
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
    }
  ]
}`
