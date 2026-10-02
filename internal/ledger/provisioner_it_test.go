//go:build it

package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_ProvisionReconcilesExistingLedger is the F8 proof: an
// already-created ledger with a *stale* chart is brought up to the current chart
// by Provision, without recreating it. We seed a ledger holding only the `rule`
// account type and an empty metadata schema, then Provision and assert every
// account type and metadata field the chart declares is now present. Re-running
// Provision must stay a clean no-op (idempotent boot).
//
//	go test -tags it -run TestIntegration_ProvisionReconcilesExistingLedger ./internal/ledger/...
func TestIntegration_ProvisionReconcilesExistingLedger(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	control := "recon-it-f8-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, control) }()

	// Seed a stale ledger: only the `rule` account type, no metadata schema —
	// simulating a ledger created before the chart grew.
	partial := map[string]*commonpb.AccountType{
		schema.AccountTypeRule: schema.AccountTypes()[schema.AccountTypeRule],
	}
	require.NoError(t, client.CreateLedger(ctx, control, nil, partial, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))

	// Reconcile pass.
	prov := NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	require.NoError(t, prov.Provision(ctx), "provision must reconcile the existing ledger")

	// Every account type and metadata field the chart declares is now present.
	// GetLedger reads FSM config, refreshed after the reconcile commits; retry to
	// absorb any read-side lag (never time.Sleep).
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		info, gerr := client.Service().GetLedger(ctx, &servicepb.GetLedgerRequest{Ledger: control})
		if !assert.NoError(c, gerr) {
			return
		}

		gotTypes := info.GetAccountTypes()
		for wantType := range schema.AccountTypes() {
			assert.Contains(c, gotTypes, wantType, "account type reconciled")
		}

		gotFields := info.GetMetadataSchema().GetAccountFields()
		for _, cmd := range schema.MetadataSchema() {
			assert.Contains(c, gotFields, cmd.GetKey(), "metadata field reconciled")
		}
	}, 10*time.Second, 50*time.Millisecond)

	// Idempotent re-provision on the now-current ledger: no error, no drift.
	require.NoError(t, prov.Provision(ctx), "re-provision must be a clean no-op")
}

// TestIntegration_ReprovisionRecordsNoRejectedWrite proves a boot on an already
// provisioned control ledger writes nothing the ledger refuses. A refused
// re-create (ledger, index, numscript) is still a signed audit entry, so every
// restart used to add one rejected entry per index and numscript to the trail.
//
//	go test -tags it -run TestIntegration_ReprovisionRecordsNoRejectedWrite ./internal/ledger/...
func TestIntegration_ReprovisionRecordsNoRejectedWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client, err := NewClient(itAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	control := "recon-it-reboot-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, control) }()

	prov := NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT)
	require.NoError(t, prov.Provision(ctx), "first boot")
	require.NoError(t, prov.Provision(ctx), "second boot")

	entries, err := client.ListAuditEntries(ctx, control, AuditScopeAll, 500)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the first boot is audited")

	var rejected []string
	for _, e := range entries {
		if e.Outcome == "failure" {
			rejected = append(rejected, e.FailureReason)
		}
	}
	require.Empty(t, rejected, "a boot must not write what the ledger refuses")
}
