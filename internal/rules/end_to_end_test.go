package rules

import (
	"context"
	"encoding/json"
	"github.com/numary/reconciliation/constants"
	"github.com/numary/reconciliation/internal/env"
	"github.com/numary/reconciliation/internal/storage/mongo"
	"github.com/spf13/pflag"
	"testing"
	"time"

	"github.com/numary/reconciliation/internal/model"
	"github.com/stretchr/testify/require"
)

func TestEndToEndAccept(t *testing.T) {

	rule := EndToEndRule{
		Rule: model.Rule{
			Name:          "RULE_TEST",
			Configuration: map[string]string{"flow_id_path": "metadata.flow_id"},
		},
	}

	t.Run("event ok", func(t *testing.T) {
		eventOK := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_TRANSACTION",
			Payload: map[string]any{"metadata": map[string]any{"flow_id": "FLOW_ID_TEST"}},
		}

		require.True(t, rule.Accept(context.Background(), eventOK))
	})

	t.Run("event ko", func(t *testing.T) {
		eventNOK := model.Event{
			Date:    time.Now(),
			Type:    "SAVED_TRANSACTION",
			Payload: map[string]any{"metadata": "PSP_ID_TEST_1234"},
		}

		require.False(t, rule.Accept(context.Background(), eventNOK))
	})

	t.Run("event empty", func(t *testing.T) {
		eventEmpty := model.Event{}

		require.False(t, rule.Accept(context.Background(), eventEmpty))
	})
}

func TestEndToEndReconciliate(t *testing.T) {

	flagSet := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, env.Init(flagSet))

	store, err := mongo.NewStore()
	if err != nil {
		panic(err)
	}

	require.NoError(t, store.RemoveAllForTests(context.Background(), constants.CollLedger))

	rule := EndToEndRule{
		Rule: model.Rule{
			Name:          "RULE_TEST",
			Configuration: map[string]string{"flow_id_path": "metadata.flow_id"},
		},
	}

	txPayload := make(map[string]any)
	require.NoError(t, json.Unmarshal([]byte(validTransactionFlowTest1), &txPayload))
	eventTx := model.Event{
		Date:    time.Now(),
		Type:    "SAVED_TRANSACTIONS",
		Payload: txPayload,
	}
	require.NoError(t, store.InsertObject(context.Background(), eventTx, constants.CollLedger))

	require.NoError(t, json.Unmarshal([]byte(validTransactionFlowTest2), &txPayload))
	eventTx.Payload = txPayload
	require.NoError(t, store.InsertObject(context.Background(), eventTx, constants.CollLedger))

	require.NoError(t, json.Unmarshal([]byte(validTransactionFlowTest3), &txPayload))
	eventTx.Payload = txPayload
	require.NoError(t, store.InsertObject(context.Background(), eventTx, constants.CollLedger))

	require.NoError(t, json.Unmarshal([]byte(validTransactionFlowTest4), &txPayload))
	eventTx.Payload = txPayload
	require.NoError(t, store.InsertObject(context.Background(), eventTx, constants.CollLedger))

	// at this tx, there is still some assets in the account:xxx account
	require.NoError(t, json.Unmarshal([]byte(validTransactionFlowTest5), &txPayload))
	eventTx.Payload = txPayload
	require.NoError(t, store.InsertObject(context.Background(), eventTx, constants.CollLedger))

	// reconciliation not ok
	t.Run("reconciliate pay-in NOT OK", func(t *testing.T) {

		status, err := rule.reconciliate(context.Background(), store, eventTx)
		require.Nil(t, err)
		require.Equal(t, "failure", status.Status)
	})

	// reconciliation ok
	t.Run("reconciliate pay-in NOK", func(t *testing.T) {

		// we add the last tx, which cleans the last assets
		require.NoError(t, json.Unmarshal([]byte(validTransactionFlowTest6), &txPayload))
		eventTx.Payload = txPayload
		require.NoError(t, store.InsertObject(context.Background(), eventTx, constants.CollLedger))

		status, err := rule.reconciliate(context.Background(), store, eventTx)
		require.Nil(t, err)
		require.Equal(t, "success", status.Status)
	})
}

var validTransactionFlowTest1 = `{
            "postings":
            [
                {
                    "amount": 6295,
                    "asset": "EUR/2",
                    "destination": "order:ds2aJMo",
                    "source": "world"
                }
            ],
            "reference": "txn_3LIRpBFqW03ZYiNn1qxuWtFT",
            "metadata":
            {
                "provider": "stripe",
                "psp_id": "ch_3LIRpBFqW03ZYiNn1G7tFTgJ",
                "type": "charge",
                "user_id": "330596",
                "bo_link": "http://bo.brocantelab.com/#!/clients/orders/edit/ds2aJMo",
                "internal_fraud_scoring": "fraud_action_trust",
                "flow_id": "ds2aJMo"
            },
      		"timestamp": "2022-07-27T12:52:16.000+00:00",
            "txid": 0,
            "precommitvolumes":
            {
                "order:ds2aJMo":
                {
                    "EUR/2":
                    {
                        "input": 0,
                        "output": 0,
                        "balance": 0
                    }
                },
                "world":
                {
                    "EUR/2":
                    {
                        "input": 0,
                        "output": 0,
                        "balance": 0
                    }
                }
            },
            "postcommitvolumes":
            {
                "order:ds2aJMo":
                {
                    "EUR/2":
                    {
                        "input": 6295,
                        "output": 0,
                        "balance": 6295
                    }
                },
                "world":
                {
                    "EUR/2":
                    {
                        "input": 0,
                        "output": 6295,
                        "balance": -6295
                    }
                }
            }
        }`

var validTransactionFlowTest2 = `{
            "postings":
            [
                {
                    "amount": 795,
                    "asset": "EUR/2",
                    "destination": "order:ds2aJMo:product:6TKUDV7S",
                    "source": "order:ds2aJMo"
                },
                {
                    "amount": 795,
                    "asset": "EUR/2",
                    "destination": "order:ds2aJMo:product:6TKUDV7S:delivery",
                    "source": "order:ds2aJMo:product:6TKUDV7S"
                }
            ],
            "reference": "",
            "metadata":
            {
                "cashoutPayment": "62d94b38110c0069408b4567",
                "description": "livraison produit #6TKUDV7S",
                "orderProduct": "62c52d153fa8ee825a8b4637",
                "flow_id": "ds2aJMo",
                "psp_id": "tr_1LOG5vFqW03ZYiNnTxbPGPf2",
                "type": "transfer.shipping"
            },
      		"timestamp": "2022-07-27T12:52:17.000+00:00",
            "txid": 1,
            "precommitvolumes":
            {
                "order:ds2aJMo":
                {
                    "EUR/2":
                    {
                        "input": 6295,
                        "output": 0,
                        "balance": 6295
                    }
                },
                "order:ds2aJMo:product:6TKUDV7S":
                {
                    "EUR/2":
                    {
                        "input": 0,
                        "output": 0,
                        "balance": 0
                    }
                },
                "order:ds2aJMo:product:6TKUDV7S:delivery":
                {
                    "EUR/2":
                    {
                        "input": 0,
                        "output": 0,
                        "balance": 0
                    }
                }
            },
            "postcommitvolumes":
            {
                "order:ds2aJMo:product:6TKUDV7S:delivery":
                {
                    "EUR/2":
                    {
                        "input": 795,
                        "output": 0,
                        "balance": 795
                    }
                },
                "order:ds2aJMo":
                {
                    "EUR/2":
                    {
                        "input": 6295,
                        "output": 795,
                        "balance": 5500
                    }
                },
                "order:ds2aJMo:product:6TKUDV7S":
                {
                    "EUR/2":
                    {
                        "input": 795,
                        "output": 795,
                        "balance": 0
                    }
                }
            }
        }`

var validTransactionFlowTest3 = `{
            "postings":
            [
                {
                    "amount": 795,
                    "asset": "EUR/2",
                    "destination": "world",
                    "source": "order:ds2aJMo:product:6TKUDV7S:delivery"
                }
            ],
            "reference": "",
            "metadata":
            {
                "flow_id": "ds2aJMo",
                "psp_id": "tr_1LOG5vFqW03ZYiNnTxbPGPf2",
                "subtype": "shipping",
                "transfer_group": "order_ds2aJMo",
                "type": "payout"
            },
      		"timestamp": "2022-07-27T12:52:18.000+00:00",
            "txid": 2,
            "precommitvolumes":
            {
                "order:ds2aJMo:product:6TKUDV7S:delivery":
                {
                    "EUR/2":
                    {
                        "input": 795,
                        "output": 0,
                        "balance": 795
                    }
                },
                "world":
                {
                    "EUR/2":
                    {
                        "input": 0,
                        "output": 6295,
                        "balance": -6295
                    }
                }
            },
            "postcommitvolumes":
            {
                "order:ds2aJMo:product:6TKUDV7S:delivery":
                {
                    "EUR/2":
                    {
                        "input": 795,
                        "output": 795,
                        "balance": 0
                    }
                },
                "world":
                {
                    "EUR/2":
                    {
                        "input": 795,
                        "output": 6295,
                        "balance": -5500
                    }
                }
            }
        }`

var validTransactionFlowTest4 = `{
            "postings":
            [
                {
                    "amount": 4510,
                    "asset": "EUR/2",
                    "destination": "order:ds2aJMo:product:6TKUDV7S",
                    "source": "order:ds2aJMo"
                },
                {
                    "amount": 4510,
                    "asset": "EUR/2",
                    "destination": "cashoutPayments:ds2aJMo:6TKUDV7S",
                    "source": "order:ds2aJMo:product:6TKUDV7S"
                }
            ],
            "reference": "",
            "metadata":
            {
                "flow_id": "ds2aJMo",
                "product": "6TKUDV7S"
            },
      		"timestamp": "2022-07-27T12:52:19.000+00:00",
            "txid": 3,
            "precommitvolumes":
            {
                "cashoutPayments:ds2aJMo:6TKUDV7S":
                {
                    "EUR/2":
                    {
                        "input": 0,
                        "output": 0,
                        "balance": 0
                    }
                },
                "order:ds2aJMo":
                {
                    "EUR/2":
                    {
                        "input": 6295,
                        "output": 795,
                        "balance": 5500
                    }
                },
                "order:ds2aJMo:product:6TKUDV7S":
                {
                    "EUR/2":
                    {
                        "input": 795,
                        "output": 795,
                        "balance": 0
                    }
                }
            },
            "postcommitvolumes":
            {
                "cashoutPayments:ds2aJMo:6TKUDV7S":
                {
                    "EUR/2":
                    {
                        "input": 4510,
                        "output": 0,
                        "balance": 4510
                    }
                },
                "order:ds2aJMo":
                {
                    "EUR/2":
                    {
                        "input": 6295,
                        "output": 5305,
                        "balance": 990
                    }
                },
                "order:ds2aJMo:product:6TKUDV7S":
                {
                    "EUR/2":
                    {
                        "input": 5305,
                        "output": 5305,
                        "balance": 0
                    }
                }
            }
        }`

var validTransactionFlowTest5 = `{
            "postings":
            [
                {
                    "amount": 4510,
                    "asset": "EUR/2",
                    "destination": "world",
                    "source": "cashoutPayments:ds2aJMo:6TKUDV7S"
                }
            ],
            "reference": "tr_1LOG5uFqW03ZYiNneJBPw3rp",
            "metadata":
            {
                "type": "payout",
                "flow_id": "ds2aJMo",
                "psp_id": "tr_1LOG5uFqW03ZYiNneJBPw3rp",
                "subtype": "merchant",
                "transfer_group": "order_ds2aJMo"
            },
      		"timestamp": "2022-07-27T12:52:20.000+00:00",
            "txid": 4,
            "precommitvolumes":
            {
                "cashoutPayments:ds2aJMo:6TKUDV7S":
                {
                    "EUR/2":
                    {
                        "input": 4510,
                        "output": 0,
                        "balance": 4510
                    }
                },
                "world":
                {
                    "EUR/2":
                    {
                        "input": 795,
                        "output": 6295,
                        "balance": -5500
                    }
                }
            },
            "postcommitvolumes":
            {
                "world":
                {
                    "EUR/2":
                    {
                        "input": 5305,
                        "output": 6295,
                        "balance": -990
                    }
                },
                "cashoutPayments:ds2aJMo:6TKUDV7S":
                {
                    "EUR/2":
                    {
                        "input": 4510,
                        "output": 4510,
                        "balance": 0
                    }
                }
            }
        }`

var validTransactionFlowTest6 = `{
            "postings":
            [
                {
                    "amount": 990,
                    "asset": "EUR/2",
                    "destination": "plateform:fees",
                    "source": "order:ds2aJMo"
                },
                {
                    "amount": 990,
                    "asset": "EUR/2",
                    "destination": "world",
                    "source": "plateform:fees"
                }
            ],
            "reference": "",
            "metadata":
            {
                "flow_id": "ds2aJMo",
                "type": "fees"
            },
      		"timestamp": "2022-07-27T12:52:21.000+00:00",
            "txid": 5,
            "precommitvolumes":
            {
                "order:ds2aJMo":
                {
                    "EUR/2":
                    {
                        "input": 6295,
                        "output": 5305,
                        "balance": 990
                    }
                },
                "plateform:fees":
                {
                    "EUR/2":
                    {
                        "input": 0,
                        "output": 0,
                        "balance": 0
                    }
                },
                "world":
                {
                    "EUR/2":
                    {
                        "input": 5305,
                        "output": 6295,
                        "balance": -990
                    }
                }
            },
            "postcommitvolumes":
            {
                "order:ds2aJMo":
                {
                    "EUR/2":
                    {
                        "input": 6295,
                        "output": 6295,
                        "balance": 0
                    }
                },
                "plateform:fees":
                {
                    "EUR/2":
                    {
                        "input": 990,
                        "output": 990,
                        "balance": 0
                    }
                },
                "world":
                {
                    "EUR/2":
                    {
                        "input": 6295,
                        "output": 6295,
                        "balance": 0
                    }
                }
            }
        }`
