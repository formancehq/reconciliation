package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchedule_JSON_WireFormat pins the schedule wire shape. Since SafetyMargin
// was removed (step 6b-2b), Schedule uses the default encoder — plain fields,
// omitempty on the optional ones.
func TestSchedule_JSON_WireFormat(t *testing.T) {
	t.Run("cron marshals its fields", func(t *testing.T) {
		s := Schedule{Kind: ScheduleCron, Expr: "*/5 * * * *", TZ: "UTC"}
		b, err := json.Marshal(s)
		require.NoError(t, err)
		require.JSONEq(t, `{"kind":"cron","expr":"*/5 * * * *","tz":"UTC"}`, string(b))
	})

	t.Run("on_demand omits optional fields", func(t *testing.T) {
		b, err := json.Marshal(Schedule{Kind: ScheduleOnDemand})
		require.NoError(t, err)
		require.JSONEq(t, `{"kind":"on_demand"}`, string(b))
	})

	t.Run("round-trip preserves fields", func(t *testing.T) {
		original := Schedule{Kind: ScheduleCron, Expr: "@hourly", TZ: "Europe/Paris"}
		b, err := json.Marshal(original)
		require.NoError(t, err)
		var decoded Schedule
		require.NoError(t, json.Unmarshal(b, &decoded))
		require.Equal(t, original, decoded)
	})
}
