package ledgerstore

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTransitionEventType(t *testing.T) {
	t.Parallel()
	cases := map[string]models.AlertEventType{
		"opened":        models.AlertEventFail,
		"occurred":      models.AlertEventFail,
		"reopened":      models.AlertEventFail,
		"auto_resolved": models.AlertEventPass,
		"acknowledged":  models.AlertEventAck,
		"resolved":      models.AlertEventResolve,
		"accepted":      models.AlertEventAccept,
		"snoozed":       models.AlertEventSnooze,
		"unsnoozed":     models.AlertEventUnsnooze,
	}
	for transition, want := range cases {
		got, ok := transitionEventType(transition)
		require.True(t, ok, transition)
		require.Equal(t, want, got, transition)
	}
	_, ok := transitionEventType("teleported")
	require.False(t, ok, "unknown transition is not projected")
}

// activityFor builds a rule activity carrying a transition envelope, as
// activityFromTransaction would produce from a committed transition transaction.
func activityFor(t *testing.T, env transitionEvent, sequence string) models.RuleActivity {
	t.Helper()
	b, err := json.Marshal(env)
	require.NoError(t, err)
	return models.RuleActivity{
		Category:   "alert",
		Sequence:   sequence,
		OccurredAt: env.OccurredAt,
		RecordedAt: env.OccurredAt,
		Payload:    json.RawMessage(b),
	}
}

func TestAlertEventFromActivity(t *testing.T) {
	t.Parallel()
	alertID := uuid.New()
	evalID := uuid.New()
	at := time.Now().UTC().Truncate(time.Second)

	t.Run("maps a failing open, links the evaluation, carries the sequence", func(t *testing.T) {
		t.Parallel()
		act := activityFor(t, transitionEvent{
			Type:          transitionEventTypePrefix + string(transitionOpened),
			AlertID:       alertID.String(),
			NewStatus:     "OPEN",
			OccurredAt:    at,
			CorrelationID: evalID.String(),
			Payload:       map[string]any{"observed": "1"},
		}, "554")

		ev, ok := alertEventFromActivity(act, alertID)
		require.True(t, ok)
		require.Equal(t, models.AlertEventFail, ev.Type)
		require.Equal(t, alertID, ev.AlertID)
		require.Equal(t, "554", ev.TransactionID)
		require.NotNil(t, ev.EvaluationID)
		require.Equal(t, evalID, *ev.EvaluationID)
		require.Nil(t, ev.PrevStatus, "an inaugural open has no prevStatus")
		require.False(t, ev.IsReopen())
		require.True(t, ev.Notify)
		require.JSONEq(t, `{"observed":"1"}`, string(ev.Payload))
		// Id is deterministic in (alertID, sequence).
		again, _ := alertEventFromActivity(act, alertID)
		require.Equal(t, ev.ID, again.ID)
	})

	t.Run("a fail landing on a resolved alert is a reopen", func(t *testing.T) {
		t.Parallel()
		act := activityFor(t, transitionEvent{
			Type:       transitionEventTypePrefix + string(transitionReopened),
			AlertID:    alertID.String(),
			PrevStatus: "RESOLVED",
			NewStatus:  "OPEN",
			OccurredAt: at,
		}, "560")

		ev, ok := alertEventFromActivity(act, alertID)
		require.True(t, ok)
		require.Equal(t, models.AlertEventFail, ev.Type)
		require.True(t, ev.IsReopen())
	})

	t.Run("skips another alert's transition", func(t *testing.T) {
		t.Parallel()
		act := activityFor(t, transitionEvent{
			Type:       transitionEventTypePrefix + string(transitionOpened),
			AlertID:    uuid.New().String(),
			NewStatus:  "OPEN",
			OccurredAt: at,
		}, "561")

		_, ok := alertEventFromActivity(act, alertID)
		require.False(t, ok)
	})

	t.Run("skips a non-alert activity (an evaluation)", func(t *testing.T) {
		t.Parallel()
		act := models.RuleActivity{Category: "evaluation", Sequence: "562", Payload: json.RawMessage(`{}`)}
		_, ok := alertEventFromActivity(act, alertID)
		require.False(t, ok)
	})
}
