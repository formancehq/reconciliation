//go:build it

package ledgerstore

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/formancehq/go-libs/bun/bunpaginate"
	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	recstore "github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// eventuallyConsistent retries fn until it passes: the ledger's read-side
// metadata index is eventually consistent with writes, so a metadata-filtered
// read (GetAlert-by-id, ListRules/ListAlerts) right after a write may briefly not
// see it (migration log F25). Structural GetAccount reads are unaffected.
func eventuallyConsistent(t *testing.T, fn func(c *assert.CollectT)) {
	t.Helper()

	require.EventuallyWithT(t, fn, 5*time.Second, 25*time.Millisecond)
}

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

	const control = "recon-it5"

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
	require.NoError(t, store.PatchRule(ctx, id, recstore.RulePatch{
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
	require.ErrorIs(t, err, recstore.ErrNotFound)

	// Delete again → not found.
	require.ErrorIs(t, store.DeleteRule(ctx, id), recstore.ErrNotFound)
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

	const control = "recon-it5"

	prov := ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	require.NoError(t, prov.Provision(ctx), "provision control-ledger")

	store := New(client, control)

	ruleID := uuid.New() // fresh rule → addresses unique to this run
	const period = "2026-03"
	fp := "asset:USD/2|account:merchant:m1:held"
	fpHash := schema.FingerprintHash(fp)

	itemAddr := schema.AlertItemAccount(ruleID.String(), period, fpHash)
	stOpenAddr := schema.AlertStateAccount(schema.StateOpen, ruleID.String(), period, fpHash)
	poolAddr := schema.PoolAccount(ruleID.String())

	in := recstore.OpenAlertInput{
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

	item, err := client.GetAccount(ctx, control, itemAddr)
	require.NoError(t, err)
	require.Equal(t, "OPEN", item.GetMetadata()[schema.MetaStatus].GetStringValue(), "status mirror")
	require.Equal(t, res.Alert.ID.String(), item.GetMetadata()[schema.MetaID].GetStringValue(), "id mirror")

	// Resolve the alert by its id via the indexed `id` metadata field. The index
	// is eventually consistent with the open, so retry until it reflects it.
	eventuallyConsistent(t, func(c *assert.CollectT) {
		byID, gerr := store.GetAlert(ctx, res.Alert.ID)
		if !assert.NoError(c, gerr, "get alert by id") {
			return
		}

		assert.Equal(c, res.Alert.ID, byID.ID)
		assert.Equal(c, models.AlertOpen, byID.Status)
		assert.Equal(c, fp, byID.Fingerprint)
	})

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

// TestIntegration_AlertTransitions exercises the guarded lifecycle transitions
// (ack / resolve / auto-resolve) + ListActiveAlertFingerprints against a real
// Ledger v3: the ALERT marker moves between state accounts (guarded by the bare
// Numscript source), the status mirror follows, and the guard rejects an illegal
// transition (re-resolve).
//
//	go test -tags it -run TestIntegration_AlertTransitions ./internal/ledgerstore/...
func TestIntegration_AlertTransitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ledger.NewClient(itLedgerAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const control = "recon-it5"

	prov := ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	require.NoError(t, prov.Provision(ctx), "provision control-ledger")

	store := New(client, control)

	ruleID := uuid.New()
	const period = "2026-04"

	open := func(fp string) *models.Alert {
		res, oerr := store.OpenOrUpdateAlert(ctx, recstore.OpenAlertInput{
			RuleID: ruleID, Fingerprint: fp, PeriodID: period, Severity: models.SeverityHigh,
			EvaluationID: uuid.New(), Evidence: json.RawMessage(`{"drift":"1"}`), OccurredAt: time.Now().UTC(),
		})
		require.NoError(t, oerr, "open %s", fp)

		return res.Alert
	}

	// Ack → burn-on-close → reopen lifecycle.
	a := open("fp-lifecycle")
	fpHash := schema.FingerprintHash("fp-lifecycle")
	stOpen := schema.AlertStateAccount(schema.StateOpen, ruleID.String(), period, fpHash)
	stAck := schema.AlertStateAccount(schema.StateAck, ruleID.String(), period, fpHash)

	// AckAlert is id-addressed (metadata index, eventually consistent) — wait
	// until the index reflects the open, then ack. The rest reads structurally.
	eventuallyConsistent(t, func(c *assert.CollectT) {
		_, gerr := store.GetAlert(ctx, a.ID)
		assert.NoError(c, gerr)
	})

	acked, err := store.AckAlert(ctx, a.ID, &models.Ack{By: "ops", At: time.Now().UTC(), Note: "looking"})
	require.NoError(t, err, "ack")
	require.Equal(t, models.AlertAcknowledged, acked.Status)
	require.Equal(t, "1", balance(ctx, t, client, control, stAck, schema.AssetAlert), "marker at st:ack")
	require.Equal(t, "0", balanceOrZero(ctx, t, client, control, stOpen, schema.AssetAlert), "st:open drained on ack")

	// Burn-on-close: the marker is burned back to the pool and st:ack purges —
	// no marker lingers for a closed alert (auto-resolve reads structurally, so
	// no index wait needed).
	closed, err := store.AutoResolveAlert(ctx, ruleID, "fp-lifecycle", period, uuid.New(), time.Now().UTC())
	require.NoError(t, err, "close")
	require.Equal(t, models.AlertResolved, closed.Status)
	require.Equal(t, "0", balanceOrZero(ctx, t, client, control, stAck, schema.AssetAlert), "marker burned → st:ack purged")

	// The item persists as the durable record; its status mirror reads RESOLVED.
	item, err := client.GetAccount(ctx, control, schema.AlertItemAccount(ruleID.String(), period, fpHash))
	require.NoError(t, err)
	require.Equal(t, "RESOLVED", item.GetMetadata()[schema.MetaStatus].GetStringValue(), "status mirror on item")

	// Reopen: a fresh failure re-mints the marker (nothing to move — it was burned).
	reopened, err := store.OpenOrUpdateAlert(ctx, recstore.OpenAlertInput{
		RuleID: ruleID, Fingerprint: "fp-lifecycle", PeriodID: period, Severity: models.SeverityHigh,
		EvaluationID: uuid.New(), Evidence: json.RawMessage(`{"drift":"2"}`), OccurredAt: time.Now().UTC(),
	})
	require.NoError(t, err, "reopen")
	require.True(t, reopened.Reopened)
	require.Equal(t, models.AlertOpen, reopened.Alert.Status)
	require.Equal(t, "1", balance(ctx, t, client, control, stOpen, schema.AssetAlert), "marker re-minted at st:open")

	// Close again so fp-lifecycle is not "active" for the sweep assertion below.
	_, err = store.AutoResolveAlert(ctx, ruleID, "fp-lifecycle", period, uuid.New(), time.Now().UTC())
	require.NoError(t, err, "re-close")

	// Auto-resolve by (rule, fingerprint, period).
	open("fp-auto")
	ar, err := store.AutoResolveAlert(ctx, ruleID, "fp-auto", period, uuid.New(), time.Now().UTC())
	require.NoError(t, err, "auto-resolve")
	require.Equal(t, models.AlertResolved, ar.Status)
	require.Equal(t, models.ResolutionAuto, ar.Resolution.Kind)

	// Auto-resolve with no active alert → no-op.
	none, err := store.AutoResolveAlert(ctx, ruleID, "fp-never", period, uuid.New(), time.Now().UTC())
	require.NoError(t, err)
	require.Nil(t, none, "no active alert → no-op")

	// ListActiveAlertFingerprints returns only the still-active fingerprints
	// (index eventually consistent → retry until fp-active shows up).
	open("fp-active")
	eventuallyConsistent(t, func(c *assert.CollectT) {
		fps, lerr := store.ListActiveAlertFingerprints(ctx, ruleID, period)
		if !assert.NoError(c, lerr) {
			return
		}

		assert.Contains(c, fps, "fp-active")
		assert.NotContains(c, fps, "fp-lifecycle", "resolved is not active")
		assert.NotContains(c, fps, "fp-auto", "auto-resolved is not active")
	})

	// Snooze / unsnooze — metadata-only, status-neutral (no marker move).
	sn := open("fp-snooze")
	snItem := schema.AlertItemAccount(ruleID.String(), period, schema.FingerprintHash("fp-snooze"))

	_, err = store.SnoozeAlert(ctx, sn.ID, time.Now().Add(time.Hour), "ops", "muting")
	require.NoError(t, err, "snooze")

	snAcct, err := client.GetAccount(ctx, control, snItem)
	require.NoError(t, err)
	require.Contains(t, snAcct.GetMetadata(), schema.MetaSnooze, "snooze metadata set")
	require.Equal(t, "OPEN", snAcct.GetMetadata()[schema.MetaStatus].GetStringValue(), "snooze is status-neutral")

	_, err = store.UnsnoozeAlert(ctx, sn.ID, "ops")
	require.NoError(t, err, "unsnooze")

	snAcct, err = client.GetAccount(ctx, control, snItem)
	require.NoError(t, err)
	require.NotContains(t, snAcct.GetMetadata(), schema.MetaSnooze, "snooze metadata cleared")

	// Unsnooze again → idempotent no-op.
	_, err = store.UnsnoozeAlert(ctx, sn.ID, "ops")
	require.NoError(t, err, "unsnooze is idempotent")
}

// TestIntegration_Lists exercises ListRules / ListAlerts (filter translation +
// metadata indexes + fetch-all + sort + offset paging) against a real Ledger v3.
//
//	go test -tags it -run TestIntegration_Lists ./internal/ledgerstore/...
func TestIntegration_Lists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ledger.NewClient(itLedgerAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const control = "recon-it5"

	prov := ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	require.NoError(t, prov.Provision(ctx), "provision control-ledger")

	store := New(client, control)

	// --- ListRules, isolated by a unique name (indexed metadata filter). ---
	name := "it-list-" + uuid.NewString()
	now := time.Now().Truncate(time.Microsecond).UTC()
	require.NoError(t, store.CreateRule(ctx, &models.Rule{
		ID: uuid.New(), Name: name, TemplateKind: models.TemplateLedgerInvariant,
		Enabled: true, Severity: models.SeverityHigh, Cadence: models.CadenceContinuous,
		CreatedAt: now, UpdatedAt: now,
	}))

	ruleOpts := recstore.PaginatedQueryOptions[recstore.RulesFilters]{PageSize: 10}.WithQueryBuilder(query.Match("name", name))
	eventuallyConsistent(t, func(c *assert.CollectT) {
		rules, lerr := store.ListRules(ctx, recstore.NewGetRulesQuery(ruleOpts))
		if !assert.NoError(c, lerr, "list rules by name") {
			return
		}

		if assert.Len(c, rules.Data, 1) {
			assert.Equal(c, name, rules.Data[0].Name)
		}
	})

	// --- ListAlerts, isolated by a fresh ruleID (indexed metadata filter). ---
	ruleID := uuid.New()
	const period = "2026-05"

	open := func(fp string, at time.Time) {
		_, oerr := store.OpenOrUpdateAlert(ctx, recstore.OpenAlertInput{
			RuleID: ruleID, Fingerprint: fp, PeriodID: period, Severity: models.SeverityHigh,
			EvaluationID: uuid.New(), Evidence: json.RawMessage(`{}`), OccurredAt: at,
		})
		require.NoError(t, oerr, "open %s", fp)
	}
	// Explicit, distinct last_seen_at so the DESC sort assertion is deterministic.
	open("fp-a", now.Add(-time.Hour))
	open("fp-b", now)

	byRule := query.Match("ruleID", ruleID.String())
	listByRule := func(qb query.Builder) *bunpaginate.Cursor[models.Alert] {
		page, lerr := store.ListAlerts(ctx, recstore.NewGetAlertsQuery(
			recstore.PaginatedQueryOptions[recstore.AlertsFilters]{PageSize: 10}.WithQueryBuilder(qb)))
		require.NoError(t, lerr, "list alerts")

		return page
	}

	// Both alerts, most-recently-seen first (index eventually consistent → retry).
	eventuallyConsistent(t, func(c *assert.CollectT) {
		all := listByRule(byRule)
		if assert.Len(c, all.Data, 2) {
			assert.Equal(c, "fp-b", all.Data[0].Fingerprint, "most-recent first")
		}
	})

	// Combined filter: ruleID AND status==OPEN → both open.
	openOnly := query.And(byRule, query.Match("status", string(models.AlertOpen)))
	eventuallyConsistent(t, func(c *assert.CollectT) {
		assert.Len(c, listByRule(openOnly).Data, 2)
	})

	// Resolve one; the OPEN-filtered list drops it.
	_, err = store.AutoResolveAlert(ctx, ruleID, "fp-a", period, uuid.New(), time.Now().UTC())
	require.NoError(t, err)

	eventuallyConsistent(t, func(c *assert.CollectT) {
		opened := listByRule(openOnly)
		if assert.Len(c, opened.Data, 1) {
			assert.Equal(c, "fp-b", opened.Data[0].Fingerprint)
		}
	})
}

// balance reads one asset's balance on an account (empty string if absent).
// TestIntegration_RecordCapture proves the audit-grade capture (ADR-003): an
// evaluation's capture is written as an immutable transaction on the control
// ledger — the CAPTURE counter on the (rule, period) bucket increments per
// evaluation, and a replay of the same evaluation is idempotent (same batch key).
//
//	go test -tags it -run TestIntegration_RecordCapture ./internal/ledgerstore/...
func TestIntegration_RecordCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ledger.NewClient(itLedgerAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const control = "recon-it5"
	require.NoError(t, ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT).Provision(ctx), "provision control-ledger")

	store := New(client, control)

	ruleID := uuid.New() // fresh rule → capture bucket unique to this run
	const period = "2026-03"
	captureAddr := schema.CaptureAccount(ruleID.String(), period)

	in := recstore.CaptureInput{
		RuleID:       ruleID,
		TemplateKind: string(models.TemplateSourceParity),
		PeriodID:     period,
		EvaluationID: uuid.New(),
		CapturedAt:   time.Now().Truncate(time.Microsecond).UTC(),
		Verdict:      "pass",
		Trigger:      "manual",
		Evidence:     json.RawMessage(`[]`),
	}

	// First capture → the CAPTURE counter on the bucket is 1.
	require.NoError(t, store.RecordCapture(ctx, in))
	require.Equal(t, "1", balance(ctx, t, client, control, captureAddr, schema.AssetCapture), "one capture recorded")

	// Same evaluation replayed → idempotent (same batch key + content): counter stays 1.
	require.NoError(t, store.RecordCapture(ctx, in))
	require.Equal(t, "1", balance(ctx, t, client, control, captureAddr, schema.AssetCapture), "replay of the same evaluation is idempotent")

	// A fresh evaluation → a distinct immutable capture: counter is 2.
	in.EvaluationID = uuid.New()
	in.Verdict = "fail"
	require.NoError(t, store.RecordCapture(ctx, in))
	require.Equal(t, "2", balance(ctx, t, client, control, captureAddr, schema.AssetCapture), "a new evaluation records a new capture")
}

func balance(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, addr, asset string) string {
	t.Helper()

	acct, err := c.GetAccount(ctx, ledgerName, addr)
	require.NoError(t, err, "get account %s", addr)

	return acct.GetVolumes()[asset].GetBalance()
}

// balanceOrZero reads an asset balance, treating a purged (NotFound) account or
// an absent volume as "0" — used to assert an EPHEMERAL marker was drained.
func balanceOrZero(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, addr, asset string) string {
	t.Helper()

	acct, err := c.GetAccount(ctx, ledgerName, addr)
	if status.Code(err) == codes.NotFound {
		return "0"
	}

	require.NoError(t, err, "get account %s", addr)

	if bal := acct.GetVolumes()[asset].GetBalance(); bal != "" {
		return bal
	}

	return "0"
}
