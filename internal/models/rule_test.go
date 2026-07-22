package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSchedule_JSON_WireFormat the OpenAPI spec documents safetyMargin as a
// Go-duration string ("30s", "1m"). The default encoder for time.Duration
// emits nanoseconds, and the default decoder cannot read the string form —
// custom Marshal/Unmarshal closes that gap.
func TestSchedule_JSON_WireFormat(t *testing.T) {
	t.Run("marshal emits duration string", func(t *testing.T) {
		s := Schedule{
			Kind:         ScheduleCron,
			Expr:         "*/5 * * * *",
			TZ:           "UTC",
			SafetyMargin: 30 * time.Second,
		}
		b, err := json.Marshal(s)
		require.NoError(t, err)
		require.JSONEq(t, `{"kind":"cron","expr":"*/5 * * * *","tz":"UTC","safetyMargin":"30s"}`, string(b))
	})

	t.Run("unmarshal accepts duration string", func(t *testing.T) {
		var s Schedule
		err := json.Unmarshal([]byte(`{"kind":"cron","expr":"@hourly","safetyMargin":"2m"}`), &s)
		require.NoError(t, err)
		require.Equal(t, ScheduleCron, s.Kind)
		require.Equal(t, "@hourly", s.Expr)
		require.Equal(t, 2*time.Minute, s.SafetyMargin)
	})

	t.Run("zero safety margin omits the field", func(t *testing.T) {
		s := Schedule{Kind: ScheduleOnDemand}
		b, err := json.Marshal(s)
		require.NoError(t, err)
		require.JSONEq(t, `{"kind":"on_demand"}`, string(b))
	})

	t.Run("missing safety margin decodes as zero", func(t *testing.T) {
		var s Schedule
		err := json.Unmarshal([]byte(`{"kind":"on_demand"}`), &s)
		require.NoError(t, err)
		require.Equal(t, time.Duration(0), s.SafetyMargin)
		require.False(t, s.SafetyMarginWasProvided)
	})

	t.Run("explicit zero safety margin survives round trip", func(t *testing.T) {
		var s Schedule
		require.NoError(t, json.Unmarshal([]byte(`{"kind":"cron","expr":"@daily","safetyMargin":"0s"}`), &s))
		require.Zero(t, s.SafetyMargin)
		require.True(t, s.SafetyMarginWasProvided)

		b, err := json.Marshal(s)
		require.NoError(t, err)
		require.JSONEq(t, `{"kind":"cron","expr":"@daily","safetyMargin":"0s"}`, string(b))
	})

	t.Run("malformed safety margin returns a clear error", func(t *testing.T) {
		var s Schedule
		err := json.Unmarshal([]byte(`{"kind":"cron","safetyMargin":"not-a-duration"}`), &s)
		require.Error(t, err)
		require.Contains(t, err.Error(), "schedule.safetyMargin")
	})

	t.Run("round-trip preserves the duration", func(t *testing.T) {
		original := Schedule{Kind: ScheduleCron, SafetyMargin: 90 * time.Second}
		b, err := json.Marshal(original)
		require.NoError(t, err)
		var decoded Schedule
		require.NoError(t, json.Unmarshal(b, &decoded))
		require.Equal(t, original.Kind, decoded.Kind)
		require.Equal(t, original.SafetyMargin, decoded.SafetyMargin)
		require.True(t, decoded.SafetyMarginWasProvided)
	})
}
