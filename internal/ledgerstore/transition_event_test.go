package ledgerstore

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStampTransition(t *testing.T) {
	t.Parallel()

	rule := uuid.New()
	a := &models.Alert{
		ID:          uuid.New(),
		RuleID:      rule,
		Fingerprint: "asset:USD/2",
		Status:      models.AlertResolved,
	}
	at := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

	md := map[string]*commonpb.MetadataValue{}
	require.NoError(t, stampTransition(md, transitionResolved, a, models.AlertOpen, "eval-123", at,
		map[string]any{"kind": "fixed_by_booking"}))

	require.Contains(t, md, schema.MetaLastTransition)

	var env transitionEvent
	require.NoError(t, json.Unmarshal([]byte(md[schema.MetaLastTransition].GetStringValue()), &env))

	require.Equal(t, "reconciliation.alert.resolved", env.Type)
	require.Equal(t, "alert:"+rule.String()+":asset:USD/2", env.Subject)
	require.Equal(t, a.ID.String(), env.AlertID)
	require.Equal(t, "OPEN", env.PrevStatus)
	require.Equal(t, "RESOLVED", env.NewStatus)
	require.True(t, env.OccurredAt.Equal(at))
	require.Equal(t, "eval-123", env.CorrelationID)
	require.Equal(t, "fixed_by_booking", env.Payload["kind"])
}

func TestStampTransition_OperatorAction_NoCorrelation(t *testing.T) {
	t.Parallel()

	a := &models.Alert{ID: uuid.New(), RuleID: uuid.New(), Fingerprint: "fp", Status: models.AlertAcknowledged}

	md := map[string]*commonpb.MetadataValue{}
	require.NoError(t, stampTransition(md, transitionAcknowledged, a, models.AlertOpen, "", time.Now().UTC(), nil))

	var env transitionEvent
	require.NoError(t, json.Unmarshal([]byte(md[schema.MetaLastTransition].GetStringValue()), &env))

	require.Equal(t, "reconciliation.alert.acknowledged", env.Type)
	require.Empty(t, env.CorrelationID, "operator actions carry no evaluation correlation")
	require.Empty(t, env.Payload)
}
