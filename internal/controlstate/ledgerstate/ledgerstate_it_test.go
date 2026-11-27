//go:build it

package ledgerstate_test

import (
	"context"
	"os"
	"testing"

	"github.com/formancehq/reconciliation/internal/controlstate"
	"github.com/formancehq/reconciliation/internal/controlstate/controlstatetest"
	"github.com/formancehq/reconciliation/internal/controlstate/ledgerstate"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ledgerAddr() string {
	if addr := os.Getenv("RECON_LEDGER_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:8888"
}

// newStore creates a ledger of its own, deleted afterwards, on the layout.
func newStore(t testing.TB, layout ledgerstate.Layout, enforcement commonpb.ChartEnforcementMode) (*ledgerstate.Store, *ledger.Client, string) {
	t.Helper()
	client, err := ledger.NewClient(ledgerAddr(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	name := "recon-it-cs2-" + uuid.NewString()[:8]
	require.NoError(t, client.CreateLedger(context.Background(), name, nil, layout.AccountTypes(), enforcement))
	t.Cleanup(func() { _ = client.DeleteLedger(context.Background(), name) })

	store := ledgerstate.New(client, name, layout)
	require.NoError(t, store.Provision(context.Background()))
	return store, client, name
}

// TestIntegration_ControlStateContract runs the controlstate contract suite
// against a real Ledger v3 on the generic layout.
func TestIntegration_ControlStateContract(t *testing.T) {
	store, _, _ := newStore(t, ledgerstate.Generic{NS: "cs"}, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	controlstatetest.Run(t, func(*testing.T) controlstatetest.Store { return store })
}

// TestIntegration_ReconChart runs one alert's life on the reconciliation chart
// in STRICT mode and checks what the gauge and the alert hold at each step.
func TestIntegration_ReconChart(t *testing.T) {
	store, client, name := newStore(t, ledgerstate.Recon{}, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_STRICT)
	ctx := t.Context()
	rid := uuid.NewString()
	rule := controlstate.Key{Kind: ledgerstate.KindRule, ID: rid}
	eval := controlstate.Key{Kind: ledgerstate.KindEval, ID: rid}
	alert := controlstate.Key{Kind: ledgerstate.KindAlert, ID: ledgerstate.AlertID(rid, "2026-11", "9c4e1a7b3f20d5e8")}
	commit := func(uow controlstate.UnitOfWork) {
		t.Helper()
		uow.IdempotencyKey = uuid.NewString()
		_, err := store.Commit(ctx, uow)
		require.NoError(t, err)
	}
	gauge := func() map[string]string {
		t.Helper()
		acct, err := client.GetAccount(ctx, name, "pool:rule:"+rid)
		require.NoError(t, err)
		out := map[string]string{}
		for _, v := range acct.GetVolumes() {
			b, err := v.GetVolumes().GetBalance().ToBigInt()
			require.NoError(t, err)
			out[v.GetAsset()] = b.String()
		}
		return out
	}
	load := func(k controlstate.Key) controlstate.Entry {
		t.Helper()
		snap, err := store.Load(ctx, k)
		require.NoError(t, err)
		return snap.Get(k)
	}

	commit(controlstate.UnitOfWork{
		Expect: []controlstate.Precondition{controlstate.ExpectAbsent(rule), controlstate.ExpectAbsent(eval)},
		Put: []controlstate.Write{
			{Key: rule, Fields: map[string]string{"template": "balance_bounds", "enabled": "true"}},
			{Key: eval, Fields: map[string]string{"lastVerdict": "none"}},
		},
	})
	commit(controlstate.UnitOfWork{
		Expect:  []controlstate.Precondition{controlstate.Expect(load(eval)), controlstate.ExpectAbsent(alert)},
		Put:     []controlstate.Write{{Key: eval, Fields: map[string]string{"lastVerdict": "fail"}}, {Key: alert, Fields: map[string]string{"status": "open", "rule": rid, "period": "2026-11"}, Move: map[string]int64{"OPEN": 1, "OCC": 1}}},
		Records: []controlstate.Record{{Kind: "capture", Subject: eval, Fields: map[string]string{"verdict": "fail"}}},
	})
	assert.Equal(t, map[string]string{"V": "-4", "OPEN": "-1", "OCC": "-1"}, gauge())

	commit(controlstate.UnitOfWork{Bumps: []controlstate.Bump{{Key: alert, Add: map[string]int64{"OCC": 1}, Live: map[string]string{"lastSeenAt": "t2"}}}})
	a := load(alert)
	assert.Equal(t, controlstate.Version(1), a.Version, "a bump keeps the version")
	assert.Equal(t, map[string]int64{"OPEN": 1, "OCC": 2}, a.Balances)
	assert.Equal(t, "t2", a.Live["lastSeenAt"])

	commit(controlstate.UnitOfWork{
		Expect: []controlstate.Precondition{controlstate.Expect(a)},
		Put:    []controlstate.Write{{Key: alert, Fields: map[string]string{"status": "ack"}, Move: map[string]int64{"OPEN": -1, "ACK": 1}}},
	})
	assert.Equal(t, map[string]string{"V": "-5", "OPEN": "0", "ACK": "-1", "OCC": "-2"}, gauge())

	acct, err := client.GetAccount(ctx, name, "alert:rule:"+rid+":per:2026-11:fp:9c4e1a7b3f20d5e8")
	require.NoError(t, err)
	md := commonpb.MetadataToMap(acct.GetMetadata())
	assert.Equal(t, "ack", md["status"], "status is projected for list filters")
	assert.Equal(t, "", md["period"], "a projected field the write dropped is cleared, not left stale")

	// STRICT: an address outside the chart is refused.
	bad := controlstate.Key{Kind: ledgerstate.KindAlert, ID: ledgerstate.AlertID(rid, "2026_11", "9c4e")}
	_, err = store.Commit(ctx, controlstate.UnitOfWork{
		IdempotencyKey: uuid.NewString(),
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(bad)},
		Put:            []controlstate.Write{{Key: bad, Fields: map[string]string{"status": "open"}}},
	})
	require.ErrorIs(t, err, controlstate.ErrInvalid, "the layout refuses a key outside the chart before the ledger sees it")

	// The ledger's STRICT chart is the second line: an address the layout let
	// through by mistake is refused, and not reported as a conflict.
	_, err = client.ApplyIdempotent(ctx, uuid.NewString(), &servicepb.Request{Type: &servicepb.Request_Apply{Apply: &servicepb.LedgerApplyRequest{
		Ledger: name,
		Action: &servicepb.LedgerAction{Data: &servicepb.LedgerAction_CreateTransaction{CreateTransaction: &servicepb.CreateTransactionPayload{
			Script: &commonpb.Script{Plain: "send [V 1] (\n\tsource = @pool:rule:" + rid + " allowing unbounded overdraft\n\tdestination = @alert:rule:" + rid + ":per:2026_11:fp:9c4e\n)\n"},
		}}},
	}}})
	require.ErrorContains(t, err, "account not matching type")
	t.Logf("refused: %v", err)
}

// TestIntegration_RejectedBatchFreesItsReferences checks that a batch the
// ledger rejects leaves none of its transaction references taken: the write
// on A sits before the stale write on B in the same batch, and A's reference
// is free again once the batch is rejected.
func TestIntegration_RejectedBatchFreesItsReferences(t *testing.T) {
	store, client, name := newStore(t, ledgerstate.Recon{}, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_STRICT)
	ctx := t.Context()

	// The adapter orders the batch by key, so A's transaction comes first.
	ids := []string{uuid.NewString(), uuid.NewString()}
	if ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	a := controlstate.Key{Kind: ledgerstate.KindRule, ID: ids[0]}
	b := controlstate.Key{Kind: ledgerstate.KindRule, ID: ids[1]}
	fields := map[string]string{"template": "balance_bounds", "enabled": "true"}

	_, err := store.Commit(ctx, controlstate.UnitOfWork{
		IdempotencyKey: uuid.NewString(),
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(b)},
		Put:            []controlstate.Write{{Key: b, Fields: fields}},
	})
	require.NoError(t, err)

	// A is new, B is stale: B's reference rule:{b}:v1 is already taken.
	rejectedKey := uuid.NewString()
	_, err = store.Commit(ctx, controlstate.UnitOfWork{
		IdempotencyKey: rejectedKey,
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(a), controlstate.ExpectAbsent(b)},
		Put:            []controlstate.Write{{Key: a, Fields: fields}, {Key: b, Fields: fields}},
	})
	require.ErrorIs(t, err, controlstate.ErrConflict)
	t.Logf("rejected: %v", err)

	snap, err := store.Load(ctx, a)
	require.NoError(t, err)
	assert.False(t, snap.Get(a).Exists(), "nothing of the rejected batch was applied")
	var refs []string
	require.NoError(t, client.ListTransactionsFunc(ctx, name, nil, func(tx *commonpb.Transaction) error {
		refs = append(refs, tx.GetReference())
		return nil
	}))
	assert.NotContains(t, refs, "rule:"+ids[0]+":v1", "no transaction holds A's reference")
	assert.Contains(t, refs, "rule:"+ids[1]+":v1", "the listing sees committed references")

	// A's reference is free: the same write, re-planned under a new key, commits.
	versions, err := store.Commit(ctx, controlstate.UnitOfWork{
		IdempotencyKey: uuid.NewString(),
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(a)},
		Put:            []controlstate.Write{{Key: a, Fields: fields}},
	})
	require.NoError(t, err, "a rejected batch must leave its references free")
	assert.Equal(t, controlstate.Version(1), versions[a])

	// The rejected batch's idempotency key stays consumed.
	_, err = store.Commit(ctx, controlstate.UnitOfWork{
		IdempotencyKey: rejectedKey,
		Expect:         []controlstate.Precondition{controlstate.ExpectAbsent(a), controlstate.ExpectAbsent(b)},
		Put:            []controlstate.Write{{Key: a, Fields: fields}, {Key: b, Fields: fields}},
	})
	require.Error(t, err, "replaying the rejected key returns its first outcome")
	t.Logf("replay of the rejected key: %v", err)
}
