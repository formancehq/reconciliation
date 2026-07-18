//go:build it

package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func itAddr() string {
	if a := os.Getenv("RECON_LEDGER_ADDR"); a != "" {
		return a
	}

	return "127.0.0.1:8888"
}

// TestIntegration_LiveReads proves the live read path (ADR-003): AggregateBalance
// reads current committed state across ledgers. A single aggregate is internally
// consistent (one server-side snapshot); the read index is eventually consistent
// with writes, so reads are retried until visible.
//
//	go test -tags it -run TestIntegration_LiveReads ./internal/ledger/...
func TestIntegration_LiveReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const asset = "USD/2"
	// Unique per run so the test is isolated on the shared dev ledgers — a fixed
	// name that was soft-deleted by an earlier run stays deleted (CreateLedger
	// returns FailedPrecondition, not AlreadyExists).
	ledgerA := "recon-it-src-a-" + uuid.NewString()
	ledgerB := "recon-it-src-b-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, ledgerA) }()
	defer func() { _ = client.DeleteLedger(ctx, ledgerB) }()

	for _, l := range []string{ledgerA, ledgerB} {
		require.NoError(t, client.CreateLedger(ctx, l, nil, nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT), "create %s", l)
	}

	// Fresh accounts per run so the test is isolated on the shared dev ledgers.
	acct := "acct:" + uuid.NewString()
	q := addressQuery(acct)
	reader := NewReader(client)

	writeBalance(ctx, t, client, ledgerA, acct, asset, 100)
	writeBalance(ctx, t, client, ledgerB, acct, asset, 100)

	// Both sides read 100 live (the read index is eventually consistent).
	requireEventualBalance(ctx, t, reader, ledgerA, q, asset, "100")
	requireEventualBalance(ctx, t, reader, ledgerB, q, asset, "100")

	// A later write is reflected by the live read.
	writeBalance(ctx, t, client, ledgerA, acct, asset, 50)
	requireEventualBalance(ctx, t, reader, ledgerA, q, asset, "150")
}

// TestIntegration_LiveListAccounts proves the per-account live read path:
// ListAccounts returns each matched account's current balance and aborts on the
// accounts budget (never truncates).
//
//	go test -tags it -run TestIntegration_LiveListAccounts ./internal/ledger/...
func TestIntegration_LiveListAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const asset = "USD/2"
	// Unique per run so a soft-deleted fixed name can't wedge the test (see
	// TestIntegration_LiveReads).
	ledgerName := "recon-it-src-la-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, ledgerName) }()
	require.NoError(t, client.CreateLedger(ctx, ledgerName, nil, nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))

	// Fresh per-run prefix so the test is isolated on the shared dev ledger.
	prefix := "la:" + uuid.NewString() + ":"
	acctA, acctB := prefix+"a", prefix+"b"
	q := json.RawMessage(fmt.Sprintf(`{"$match":{"address":%q}}`, prefix+"*"))
	reader := NewReader(client)

	writeBalance(ctx, t, client, ledgerName, acctA, asset, 100)
	writeBalance(ctx, t, client, ledgerName, acctB, asset, 200)

	// Both accounts are visible to a live per-account read (eventually consistent).
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		accts, lerr := reader.ListAccounts(ctx, ledgerName, q, 10)
		if !assert.NoError(c, lerr) {
			return
		}
		assert.Len(c, accts, 2)
	}, 5*time.Second, 25*time.Millisecond)

	byAddr := listByAddress(ctx, t, reader, ledgerName, q, 10)
	require.Len(t, byAddr, 2)
	require.Equal(t, "100", byAddr[acctA].Balances[asset].String())
	require.Equal(t, "200", byAddr[acctB].Balances[asset].String())
	require.Equal(t, ledgerName, byAddr[acctA].Ledger)

	// Budget: limit below the match count aborts with an error, never truncates.
	_, err = reader.ListAccounts(ctx, ledgerName, q, 1)
	require.Error(t, err, "accounts budget must abort")

	// A later write is reflected by the live per-account read.
	writeBalance(ctx, t, client, ledgerName, acctA, asset, 50)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		live := listByAddress(ctx, t, reader, ledgerName, q, 10)
		if !assert.Contains(c, live, acctA) {
			return
		}
		assert.Equal(c, "150", live[acctA].Balances[asset].String())
	}, 5*time.Second, 25*time.Millisecond)
}

// TestIntegration_ColorAwareBalances proves compatibility with Ledger v3's
// repeated (asset, color) volume rows while preserving Reconciliation's current
// per-asset source semantics.
func TestIntegration_ColorAwareBalances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ledgerName := "recon-it-color-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, ledgerName) }()
	require.NoError(t, client.CreateLedger(ctx, ledgerName, nil, nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))

	const asset = "USD/2"
	account := "colored:" + uuid.NewString()
	query := addressQuery(account)
	reader := NewReader(client)

	writeBalance(ctx, t, client, ledgerName, account, asset, 100)
	writeColoredBalance(ctx, t, client, ledgerName, account, asset, "RESERVED", 25)

	requireEventualBalance(ctx, t, reader, ledgerName, query, asset, "125")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		accounts, listErr := reader.ListAccounts(ctx, ledgerName, query, 1)
		if !assert.NoError(c, listErr) || !assert.Len(c, accounts, 1) {
			return
		}
		assert.Equal(c, "125", accounts[0].Balances[asset].String())
	}, 5*time.Second, 25*time.Millisecond)

	protoAccount, err := client.GetAccount(ctx, ledgerName, account)
	require.NoError(t, err)
	require.Equal(t, "125", commonpb.BalanceByAsset(protoAccount, asset).String())
}

// TestIntegration_MetadataOperators proves the extended query DSL end-to-end
// against a typed, indexed metadata field: a numeric comparison ($gt) and
// existence ($exists) select the right account set (AC#1), and ValidateQuery
// rejects a query on an unindexed key while accepting the indexed one (AC#2).
//
//	go test -tags it -run TestIntegration_MetadataOperators ./internal/ledger/...
func TestIntegration_MetadataOperators(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	ledgerName := "recon-it-md-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, ledgerName) }()

	// Declare `tier` as INT64 so the query engine interprets it numerically, and
	// build its accounts index (a metadata filter needs both — declaring a type
	// does not by itself make the field queryable).
	require.NoError(t, client.CreateLedger(ctx, ledgerName,
		[]*commonpb.SetMetadataFieldTypeCommand{{
			TargetType: commonpb.TargetType_TARGET_TYPE_ACCOUNT,
			Key:        "tier",
			Type:       commonpb.MetadataType_METADATA_TYPE_INT64,
		}},
		nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))
	require.NoError(t, client.CreateIndex(ctx, ledgerName,
		&servicepb.CreateIndexRequest{Id: commonpb.AccountMetadataIndexID("tier")}))

	const asset = "USD/2"
	prefix := "md:" + uuid.NewString() + ":"
	acctLow, acctHigh, acctNone := prefix+"low", prefix+"high", prefix+"none"
	reader := NewReader(client)

	// Three accounts: two carry a typed `tier`, one carries none.
	for addr, amount := range map[string]uint64{acctLow: 100, acctHigh: 200, acctNone: 300} {
		writeBalance(ctx, t, client, ledgerName, addr, asset, amount)
	}
	require.NoError(t, client.SaveAccountMetadataValues(ctx, ledgerName, acctLow,
		map[string]*commonpb.MetadataValue{"tier": {Type: &commonpb.MetadataValue_IntValue{IntValue: 1}}}))
	require.NoError(t, client.SaveAccountMetadataValues(ctx, ledgerName, acctHigh,
		map[string]*commonpb.MetadataValue{"tier": {Type: &commonpb.MetadataValue_IntValue{IntValue: 5}}}))

	// $gt: only tier=5 (200) is above 3; scoped to this run's prefix. Retried
	// until the index has absorbed the writes (eventually consistent).
	gtQuery := json.RawMessage(fmt.Sprintf(
		`{"$and":[{"$match":{"address":%q}},{"$gt":{"metadata[tier]":3}}]}`, prefix+"*"))
	requireEventualBalance(ctx, t, reader, ledgerName, gtQuery, asset, "200")

	// $exists: the two tier-bearing accounts, not acctNone.
	existsQuery := json.RawMessage(fmt.Sprintf(
		`{"$and":[{"$match":{"address":%q}},{"$exists":{"metadata[tier]":true}}]}`, prefix+"*"))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		byAddr := map[string]Account{}
		accts, lerr := reader.ListAccounts(ctx, ledgerName, existsQuery, 10)
		if !assert.NoError(c, lerr) {
			return
		}
		for _, a := range accts {
			byAddr[a.Address] = a
		}
		assert.Len(c, byAddr, 2)
		assert.Contains(c, byAddr, acctLow)
		assert.Contains(c, byAddr, acctHigh)
		assert.NotContains(c, byAddr, acctNone)
	}, 10*time.Second, 50*time.Millisecond)

	// ValidateQuery: the indexed key + an address-only query are accepted; an
	// unindexed metadata key is rejected as ErrQueryIndex (the create-time guard).
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, reader.ValidateQuery(ctx, ledgerName, gtQuery), "indexed key accepted")
	}, 10*time.Second, 50*time.Millisecond)
	require.NoError(t, reader.ValidateQuery(ctx, ledgerName,
		json.RawMessage(fmt.Sprintf(`{"$match":{"address":%q}}`, prefix+"*"))), "address-only needs no index")

	unindexed := json.RawMessage(`{"$match":{"metadata[recon-it-unindexed]":"x"}}`)
	require.ErrorIs(t, reader.ValidateQuery(ctx, ledgerName, unindexed), ErrQueryIndex,
		"a query on an unindexed metadata key must be rejected at create time")
}

// listByAddress reads the matched accounts live and indexes them by address.
func listByAddress(ctx context.Context, t *testing.T, r *Reader, ledgerName string, q json.RawMessage, limit int) map[string]Account {
	t.Helper()

	accts, err := r.ListAccounts(ctx, ledgerName, q, limit)
	require.NoError(t, err, "list accounts on %s", ledgerName)

	out := make(map[string]Account, len(accts))
	for _, a := range accts {
		out[a.Address] = a
	}

	return out
}

// addressQuery is the data-ledger source query for one exact account.
func addressQuery(account string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"$match": map[string]any{"address": account}})

	return b
}

// writeBalance mints `amount` of asset to account in ledger (world → account).
func writeBalance(ctx context.Context, t *testing.T, c *Client, ledgerName, account, asset string, amount uint64) {
	t.Helper()
	writeColoredBalance(ctx, t, c, ledgerName, account, asset, "", amount)
}

// writeColoredBalance mints amount into one Ledger color bucket.
func writeColoredBalance(ctx context.Context, t *testing.T, c *Client, ledgerName, account, asset, color string, amount uint64) {
	t.Helper()

	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_Apply{
			Apply: &servicepb.LedgerApplyRequest{
				Ledger: ledgerName,
				Action: &servicepb.LedgerAction{
					Data: &servicepb.LedgerAction_CreateTransaction{
						CreateTransaction: &servicepb.CreateTransactionPayload{
							Postings: []*commonpb.Posting{{
								Source:      "world",
								Destination: account,
								Amount:      commonpb.NewUint256FromUint64(amount),
								Asset:       asset,
								Color:       color,
							}},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err, "write %d %s (color %q) to %s@%s", amount, asset, color, account, ledgerName)
}

// requireEventualBalance retries the live aggregate read until it equals want
// (the read index is eventually consistent with writes).
func requireEventualBalance(ctx context.Context, t *testing.T, r *Reader, ledgerName string, q json.RawMessage, asset string, want string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		bal, err := r.AggregateBalance(ctx, ledgerName, q)
		if !assert.NoError(c, err) {
			return
		}

		got := "0"
		if bal[asset] != nil {
			got = bal[asset].String()
		}

		assert.Equal(c, want, got, fmt.Sprintf("%s balance on %s", asset, ledgerName))
	}, 5*time.Second, 25*time.Millisecond)
}
