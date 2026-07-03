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

func TestRuleMetadataRoundTrip(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	// datetime metadata is stored at microsecond precision.
	now := time.Now().Truncate(time.Microsecond).UTC()

	orig := &models.Rule{
		ID:            id,
		Name:          "trust-invariant",
		TemplateKind:  models.TemplateLedgerInvariant,
		TemplateSpec:  json.RawMessage(`{"tolerance":"0"}`),
		CompiledCEL:   "sum(x) == 0",
		Enabled:       true,
		Severity:      models.SeverityHigh,
		Cadence:       models.CadenceContinuous,
		Schedule:      &models.Schedule{Kind: models.ScheduleKind("cron"), Expr: "0 0 * * *", TZ: "UTC", SafetyMargin: 30 * time.Second},
		Notifications: []string{"webhook-1", "email-ops"},
		Labels:        map[string]string{"env": "prod", "team": "treasury"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	acct := &commonpb.Account{
		Address:  schema.RuleAccount(id.String()),
		Metadata: ruleToMetadata(orig),
	}

	got, err := ruleFromAccount(acct)
	require.NoError(t, err)

	require.Equal(t, orig.ID, got.ID)
	require.Equal(t, orig.Name, got.Name)
	require.Equal(t, orig.TemplateKind, got.TemplateKind)
	require.JSONEq(t, string(orig.TemplateSpec), string(got.TemplateSpec))
	require.Equal(t, orig.CompiledCEL, got.CompiledCEL)
	require.Equal(t, orig.Enabled, got.Enabled)
	require.Equal(t, orig.Severity, got.Severity)
	require.Equal(t, orig.Cadence, got.Cadence)
	require.Equal(t, orig.Schedule, got.Schedule)
	require.Equal(t, orig.Notifications, got.Notifications)
	require.Equal(t, orig.Labels, got.Labels)
	require.True(t, orig.CreatedAt.Equal(got.CreatedAt), "createdAt: %s != %s", orig.CreatedAt, got.CreatedAt)
	require.True(t, orig.UpdatedAt.Equal(got.UpdatedAt))

	// enabled must be a typed BOOL value, not a stringified "true".
	require.True(t, acct.Metadata[schema.MetaEnabled].GetBoolValue())
}

func TestRuleMetadataMinimal(t *testing.T) {
	t.Parallel()

	// A rule with no schedule / notifications / labels omits those keys entirely.
	id := uuid.New()
	orig := &models.Rule{
		ID:           id,
		Name:         "minimal",
		TemplateKind: models.TemplateAccountThreshold,
		TemplateSpec: json.RawMessage(`{}`),
		Enabled:      false,
		Severity:     models.SeverityLow,
		Cadence:      models.CadenceContinuous,
		CreatedAt:    time.Now().Truncate(time.Microsecond).UTC(),
		UpdatedAt:    time.Now().Truncate(time.Microsecond).UTC(),
	}

	md := ruleToMetadata(orig)
	require.NotContains(t, md, schema.MetaSchedule)
	require.NotContains(t, md, schema.MetaNotifications)
	require.NotContains(t, md, schema.MetaCompiledCEL)

	got, err := ruleFromAccount(&commonpb.Account{Address: schema.RuleAccount(id.String()), Metadata: md})
	require.NoError(t, err)
	require.Nil(t, got.Schedule)
	require.Empty(t, got.Notifications)
	require.Empty(t, got.Labels)
	require.False(t, got.Enabled)
}
