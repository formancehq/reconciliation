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

	const control = "recon-it"

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
