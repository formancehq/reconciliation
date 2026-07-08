//go:build it

package ledgerstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	recstore "github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestIntegration_TransitionEventStamped proves ED-1 on a live ledger: each
// transition stamps a self-describing last_transition envelope on the item
// account, so the ledger log event for that write carries what happened.
func TestIntegration_TransitionEventStamped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ledger.NewClient(itLedgerAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const control = "recon-it5"
	require.NoError(t, ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT).Provision(ctx))

	store := New(client, control)

	ruleID := uuid.New()
	const period = "2026-03"
	fp := "asset:USD/2|acct:x"
	itemAddr := schema.AlertItemAccount(ruleID.String(), period, schema.FingerprintHash(fp))
	evalID := uuid.New()

	// Open → opened, correlated to the evaluation.
	res, err := store.OpenOrUpdateAlert(ctx, recstore.OpenAlertInput{
		RuleID:       ruleID,
		Fingerprint:  fp,
		PeriodID:     period,
		Severity:     models.SeverityHigh,
		EvaluationID: evalID,
		OccurredAt:   time.Now().Truncate(time.Microsecond).UTC(),
	})
	require.NoError(t, err)

	env := lastTransition(ctx, t, client, control, itemAddr)
	require.Equal(t, "reconciliation.alert.opened", env.Type)
	require.Equal(t, "alert:"+ruleID.String()+":"+fp, env.Subject)
	require.Equal(t, res.Alert.ID.String(), env.AlertID)
	require.Equal(t, "OPEN", env.NewStatus)
	require.Equal(t, evalID.String(), env.CorrelationID)

	// Ack → acknowledged, an operator action (no correlation).
	_, err = store.AckAlert(ctx, res.Alert.ID, &models.Ack{By: "ops", At: time.Now().UTC()})
	require.NoError(t, err)

	env = lastTransition(ctx, t, client, control, itemAddr)
	require.Equal(t, "reconciliation.alert.acknowledged", env.Type)
	require.Equal(t, "OPEN", env.PrevStatus)
	require.Equal(t, "ACKNOWLEDGED", env.NewStatus)
	require.Empty(t, env.CorrelationID)
}

// lastTransition reads and decodes the item account's last_transition envelope
// (a structural read — immediately consistent, no index lag).
func lastTransition(ctx context.Context, t *testing.T, c *ledger.Client, control, itemAddr string) transitionEvent {
	t.Helper()

	acct, err := c.GetAccount(ctx, control, itemAddr)
	require.NoError(t, err)

	raw := acct.GetMetadata()[schema.MetaLastTransition].GetStringValue()
	require.NotEmpty(t, raw, "last_transition must be stamped")

	var env transitionEvent
	require.NoError(t, json.Unmarshal([]byte(raw), &env))

	return env
}
