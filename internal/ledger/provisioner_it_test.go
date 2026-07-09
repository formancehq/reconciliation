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
