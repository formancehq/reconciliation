//go:build it

package ledgerstate_test

import (
	"context"
	"os"
	"testing"

	"github.com/formancehq/reconciliation/internal/controlstate/controlstatetest"
	"github.com/formancehq/reconciliation/internal/controlstate/ledgerstate"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestIntegration_ControlStateContract runs the controlstate contract suite
// against a real Ledger v3, on a ledger of its own deleted afterwards.
func TestIntegration_ControlStateContract(t *testing.T) {
	addr := os.Getenv("RECON_LEDGER_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8888"
	}
	client, err := ledger.NewClient(addr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	name := "recon-it-cs-" + uuid.NewString()
	require.NoError(t, client.CreateLedger(t.Context(), name, nil, nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))
	t.Cleanup(func() { _ = client.DeleteLedger(context.Background(), name) })

	store := ledgerstate.New(client, name)
	require.NoError(t, store.Provision(t.Context()))
	controlstatetest.Run(t, func(*testing.T) controlstatetest.Store { return store })
}
