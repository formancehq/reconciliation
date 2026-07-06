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

// TestIntegration_CheckpointConsistentReads proves the ADR-002 core: a query
// checkpoint is a globally consistent cross-ledger cut. It reads two ledgers at
// one checkpoint, mutates one afterwards, and shows the checkpoint read stays a
// frozen snapshot while the live read diverges.
//
//	go test -tags it -run TestIntegration_CheckpointConsistentReads ./internal/ledger/...
func TestIntegration_CheckpointConsistentReads(t *testing.T) {
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
	reader := NewCheckpointReader(client)

	writeBalance(ctx, t, client, ledgerA, acct, asset, 100)
	writeBalance(ctx, t, client, ledgerB, acct, asset, 100)

	// Wait until both writes are visible to live aggregation, so the checkpoint
	// taken next is guaranteed to include them (the read index is eventually
	// consistent — see migration log F25).
	requireEventualBalance(ctx, t, reader, ledgerA, q, asset, 0, "100")
	requireEventualBalance(ctx, t, reader, ledgerB, q, asset, 0, "100")

	cp, err := client.AcquireCheckpoint(ctx)
	require.NoError(t, err, "acquire checkpoint")

	defer func() { _ = cp.Release(ctx) }()

	require.NotZero(t, cp.ID)

	// The checkpoint is a consistent cut: A and B both read 100 at it.
	require.Equal(t, "100", balanceAt(ctx, t, reader, ledgerA, q, asset, cp.ID), "A at checkpoint")
	require.Equal(t, "100", balanceAt(ctx, t, reader, ledgerB, q, asset, cp.ID), "B at checkpoint")

	// Mutate A AFTER the checkpoint.
	writeBalance(ctx, t, client, ledgerA, acct, asset, 50)

	// Live read eventually reflects the +50; the checkpoint read never does.
	requireEventualBalance(ctx, t, reader, ledgerA, q, asset, 0, "150")
	require.Equal(t, "100", balanceAt(ctx, t, reader, ledgerA, q, asset, cp.ID), "checkpoint read is a frozen snapshot")
}

// TestIntegration_CheckpointListAccounts proves the per-account read side of the
// checkpoint reader: ListAccounts returns each matched account's frozen balance
// at a checkpoint, aborts on the accounts budget, and never reflects writes made
// after the checkpoint.
//
//	go test -tags it -run TestIntegration_CheckpointListAccounts ./internal/ledger/...
func TestIntegration_CheckpointListAccounts(t *testing.T) {
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
	reader := NewCheckpointReader(client)

	writeBalance(ctx, t, client, ledgerName, acctA, asset, 100)
	writeBalance(ctx, t, client, ledgerName, acctB, asset, 200)

	// Wait until both accounts are visible to a live per-account read, so the
	// checkpoint taken next includes them (read index is eventually consistent).
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		accts, lerr := reader.ListAccounts(ctx, ledgerName, q, 0, 10)
		if !assert.NoError(c, lerr) {
			return
		}
		assert.Len(c, accts, 2)
	}, 5*time.Second, 25*time.Millisecond)

	cp, err := client.AcquireCheckpoint(ctx)
	require.NoError(t, err, "acquire checkpoint")

	defer func() { _ = cp.Release(ctx) }()

	byAddr := listByAddress(ctx, t, reader, ledgerName, q, cp.ID, 10)
	require.Len(t, byAddr, 2)
	require.Equal(t, "100", byAddr[acctA].Balances[asset].String())
	require.Equal(t, "200", byAddr[acctB].Balances[asset].String())
	require.Equal(t, ledgerName, byAddr[acctA].Ledger)

	// Budget: limit below the match count aborts with an error, never truncates.
	_, err = reader.ListAccounts(ctx, ledgerName, q, cp.ID, 1)
	require.Error(t, err, "accounts budget must abort")

	// Mutate acctA AFTER the checkpoint; the checkpoint read stays frozen.
	writeBalance(ctx, t, client, ledgerName, acctA, asset, 50)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		live := listByAddress(ctx, t, reader, ledgerName, q, 0, 10)
		if !assert.Contains(c, live, acctA) {
			return
		}
		assert.Equal(c, "150", live[acctA].Balances[asset].String())
	}, 5*time.Second, 25*time.Millisecond)
	frozen := listByAddress(ctx, t, reader, ledgerName, q, cp.ID, 10)
	require.Equal(t, "100", frozen[acctA].Balances[asset].String(), "checkpoint read is a frozen snapshot")
}

// listByAddress reads the matched accounts at checkpointID and indexes them by
// address for assertion.
func listByAddress(ctx context.Context, t *testing.T, r *CheckpointReader, ledgerName string, q json.RawMessage, checkpointID uint64, limit int) map[string]Account {
	t.Helper()

	accts, err := r.ListAccounts(ctx, ledgerName, q, checkpointID, limit)
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

// balanceAt reads the asset balance at checkpointID (0 = live) as a decimal string.
func balanceAt(ctx context.Context, t *testing.T, r *CheckpointReader, ledgerName string, q json.RawMessage, asset string, checkpointID uint64) string {
	t.Helper()

	bal, err := r.AggregateBalance(ctx, ledgerName, q, checkpointID)
	require.NoError(t, err, "aggregate %s@%s", asset, ledgerName)

	if bal[asset] == nil {
		return "0"
	}

	return bal[asset].String()
}

// requireEventualBalance retries the aggregate read until it equals want (the
// live read index is eventually consistent with writes).
func requireEventualBalance(ctx context.Context, t *testing.T, r *CheckpointReader, ledgerName string, q json.RawMessage, asset string, checkpointID uint64, want string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		bal, err := r.AggregateBalance(ctx, ledgerName, q, checkpointID)
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
