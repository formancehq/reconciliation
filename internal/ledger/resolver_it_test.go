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

	const (
		ledgerA = "recon-it-src-a"
		ledgerB = "recon-it-src-b"
		asset   = "USD/2"
	)

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

	const (
		ledgerName = "recon-it-src-la"
		asset      = "USD/2"
	)
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
							}},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err, "write %d %s to %s@%s", amount, asset, account, ledgerName)
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
