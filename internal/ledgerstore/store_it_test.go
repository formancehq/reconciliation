//go:build it

package ledgerstore

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// itLedgerAddr is the running ledger v3 gRPC address (override with RECON_LEDGER_ADDR).
func itLedgerAddr() string {
	if a := os.Getenv("RECON_LEDGER_ADDR"); a != "" {
		return a
	}

	return "127.0.0.1:8888"
}

// TestIntegration_RuleLifecycle exercises the provisioner + LedgerStore rule CRUD
// against a real Ledger v3 (insecure local dev). Validates the gRPC transport,
// CreateLedger + account types + typed metadata schema + prepared queries, and
// the typed metadata write/read round-trip end-to-end.
//
//	go test -tags it -run TestIntegration_RuleLifecycle ./internal/ledgerstore/...
func TestIntegration_RuleLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ledger.NewClient(itLedgerAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const control = "recon-it2"

	// Idempotent bootstrap in AUDIT so the chart is validated but not enforced.
	prov := ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	require.NoError(t, prov.Provision(ctx), "provision control-ledger")

	store := New(client, control)

	id := uuid.New()
	now := time.Now().Truncate(time.Microsecond).UTC()
	rule := &models.Rule{
		ID:           id,
		Name:         "it-rule",
		TemplateKind: models.TemplateLedgerInvariant,
		TemplateSpec: json.RawMessage(`{"tolerance":"0"}`),
		Enabled:      true,
		Severity:     models.SeverityHigh,
		Cadence:      models.CadenceContinuous,
		Labels:       map[string]string{"env": "it", "team": "recon"},
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	require.NoError(t, store.CreateRule(ctx, rule), "create rule")

	got, err := store.GetRule(ctx, id)
	require.NoError(t, err, "get rule")
	require.Equal(t, "it-rule", got.Name)
	require.True(t, got.Enabled)
	require.Equal(t, models.SeverityHigh, got.Severity)
	require.JSONEq(t, `{"tolerance":"0"}`, string(got.TemplateSpec))
	require.Equal(t, "it", got.Labels["env"])
	require.Equal(t, "recon", got.Labels["team"])

	// Patch: flip enabled, rename, drop the "team" label.
	require.NoError(t, store.PatchRule(ctx, id, storage.RulePatch{
		Enabled: ptr(false),
		Name:    ptr("it-renamed"),
		Labels:  ptr(map[string]string{"env": "it"}),
	}))

	got, err = store.GetRule(ctx, id)
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.Equal(t, "it-renamed", got.Name)
	require.Equal(t, map[string]string{"env": "it"}, got.Labels, "team label must be pruned")

	// Delete → gone.
	require.NoError(t, store.DeleteRule(ctx, id))
	_, err = store.GetRule(ctx, id)
	require.ErrorIs(t, err, storage.ErrNotFound)

	// Delete again → not found.
	require.ErrorIs(t, store.DeleteRule(ctx, id), storage.ErrNotFound)
}

// TestIntegration_OpenAlert exercises OpenOrUpdateAlert against a real Ledger v3.
// It proves the alert-lifecycle write end-to-end: the ALERT marker lands in
// st:open, the OCC counter and status mirror land on the item account, a
// same-evaluation replay is deduplicated by the batch idempotency key, and a
// fresh evaluation bumps the counter (a real repeat).
//
//	go test -tags it -run TestIntegration_OpenAlert ./internal/ledgerstore/...
func TestIntegration_OpenAlert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ledger.NewClient(itLedgerAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const control = "recon-it2"

	prov := ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	require.NoError(t, prov.Provision(ctx), "provision control-ledger")

	store := New(client, control)

	ruleID := uuid.New() // fresh rule → addresses unique to this run
	const period = "2026-03"
	fp := "asset:USD/2|account:merchant:m1:held"
	fpHash := schema.FingerprintHash(fp)

	itemAddr := schema.AlertItemAccount(ruleID.String(), period, fpHash)
	stOpenAddr := schema.AlertStateAccount(schema.StateOpen, ruleID.String(), period, fpHash)
	poolAddr := schema.PoolAccount(ruleID.String(), period)

	in := storage.OpenAlertInput{
		RuleID:       ruleID,
		Fingerprint:  fp,
		PeriodID:     period,
		Severity:     models.SeverityHigh,
		EvaluationID: uuid.New(),
		Evidence:     json.RawMessage(`{"drift":"42"}`),
		Labels:       map[string]string{"env": "it"},
		OccurredAt:   time.Now().Truncate(time.Microsecond).UTC(),
	}

	// First fail → open.
	res, err := store.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err, "open alert")
	require.True(t, res.Created)
	require.False(t, res.Reopened)
	require.Equal(t, models.AlertOpen, res.Alert.Status)

	// Marker sits in st:open; OCC counter is 1; status mirror is OPEN.
	require.Equal(t, "1", balance(ctx, t, client, control, stOpenAddr, schema.AssetAlert), "ALERT marker in st:open")
	require.Equal(t, "1", balance(ctx, t, client, control, itemAddr, schema.AssetOcc), "OCC counter")
	require.Equal(t, "-1", balance(ctx, t, client, control, poolAddr, schema.AssetAlert), "pool ALERT = -1 live alert")

	item, err := client.GetAccount(ctx, control, itemAddr, 0)
	require.NoError(t, err)
	require.Equal(t, "OPEN", item.GetMetadata()[schema.MetaStatus].GetStringValue(), "status mirror")
	require.Equal(t, res.Alert.ID.String(), item.GetMetadata()[schema.MetaID].GetStringValue(), "id mirror")

	// Fresh evaluation, same fingerprint/period → a real repeat: OCC → 2, and
	// still exactly one marker in st:open (no double-open).
	in.EvaluationID = uuid.New()
	res, err = store.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err, "repeat")
	require.False(t, res.Created)
	require.False(t, res.Reopened)
	require.Equal(t, int64(2), res.Alert.OccurrenceCount)
	require.Equal(t, "2", balance(ctx, t, client, control, itemAddr, schema.AssetOcc), "repeat bumps OCC")
	require.Equal(t, "1", balance(ctx, t, client, control, stOpenAddr, schema.AssetAlert), "still one marker in st:open")

	// Idempotency plumbing: an identical batch resubmitted under the same key is
	// deduplicated by the ledger (the at-least-once safety net for gRPC
	// retransmits on leader failover). Asserted at the client level on a
	// throwaway probe account, since a resubmit through the store would re-read
	// the advanced state and build a *different* batch — which the ledger rejects
	// as a key/content conflict rather than replaying (see migration log F17).
	probe := schema.AlertItemAccount(ruleID.String(), period, schema.FingerprintHash("idem-probe"))
	occMint := ledger.CreateTransactionInput{
		Ledger:         control,
		ScriptName:     schema.NumscriptAlertBump,
		ScriptVersion:  schema.NumscriptVersion,
		Vars:           map[string]string{schema.VarPool: poolAddr, schema.VarItem: probe},
		IdempotencyKey: "it-idem-" + ruleID.String(),
	}
	require.NoError(t, client.CreateTransaction(ctx, occMint))
	require.NoError(t, client.CreateTransaction(ctx, occMint), "identical batch under the same key")
	require.Equal(t, "1", balance(ctx, t, client, control, probe, schema.AssetOcc), "identical batch applied once")
}

// balance reads one asset's balance on an account (empty string if absent).
func balance(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, addr, asset string) string {
	t.Helper()

	acct, err := c.GetAccount(ctx, ledgerName, addr, 0)
	require.NoError(t, err, "get account %s", addr)

	return acct.GetVolumes()[asset].GetBalance()
}
