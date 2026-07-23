package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/stretchr/testify/require"
)

// When an ACKNOWLEDGED alert receives another failing evaluation it resurfaces
// to OPEN — and the stale ack must be cleared, so the row never reports an OPEN
// alert as still acknowledged by a superseded decision. The historical ack
// stays in the append-only alert_event log.
func TestOpenOrUpdateAlert_ClearsAckOnResurface(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	ruleID, evID := seedRuleAndEval(t, s)
	in := defaultOpenInput(t, ruleID, evID)

	res, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	alertID := res.Alert.ID

	_, err = s.AckAlert(ctx, alertID, &models.Ack{By: "ops@buildr.com", At: time.Now().UTC(), Note: "investigating"})
	require.NoError(t, err)

	// Fresh failing evaluation (changed evidence) resurfaces the alert.
	in.Evidence = json.RawMessage(`{"drift":"75"}`)
	in.OccurredAt = time.Now().UTC()
	res2, err := s.OpenOrUpdateAlert(ctx, in)
	require.NoError(t, err)
	require.Equal(t, models.AlertOpen, res2.Alert.Status, "resurfaced alert should be OPEN")
	require.Nil(t, res2.Alert.Ack, "ack must be cleared on ACKNOWLEDGED→OPEN resurface")

	// Persisted, not just cleared on the returned struct.
	got, err := s.GetAlert(ctx, alertID)
	require.NoError(t, err)
	require.Equal(t, models.AlertOpen, got.Status)
	require.Nil(t, got.Ack)

	// The ack is preserved in history — one ack event on the timeline.
	acks := 0
	for _, e := range allAlertEvents(t, s, alertID) {
		if e.Type == models.AlertEventAck {
			acks++
		}
	}
	require.Equal(t, 1, acks, "the original ack must remain in the append-only log")
}
