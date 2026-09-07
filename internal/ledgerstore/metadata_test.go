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
		TemplateKind:  models.TemplateBalanceEquation,
		TemplateSpec:  json.RawMessage(`{"tolerance":"0"}`),
		CompiledCEL:   "sum(x) == 0",
		Enabled:       true,
		Severity:      models.SeverityHigh,
		PeriodType:    models.PeriodTypeContinuous,
		Schedule:      &models.Schedule{Kind: models.ScheduleKind("cron"), Expr: "0 0 * * *", TZ: "UTC"},
		Notifications: []string{"webhook-1", "email-ops"},
		Labels:        map[string]string{"env": "prod", "team": "treasury"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	md, err := ruleToMetadata(orig)
	require.NoError(t, err)

	acct := &commonpb.Account{Address: schema.RuleAccount(id.String()), Metadata: md}

	got, err := ruleFromAccount(acct)
	require.NoError(t, err)

	require.Equal(t, orig.ID, got.ID)
	require.Equal(t, orig.Name, got.Name)
	require.Equal(t, orig.TemplateKind, got.TemplateKind)
	require.JSONEq(t, string(orig.TemplateSpec), string(got.TemplateSpec))
	require.Equal(t, orig.CompiledCEL, got.CompiledCEL)
	require.Equal(t, orig.Enabled, got.Enabled)
	require.Equal(t, orig.Severity, got.Severity)
	require.Equal(t, orig.PeriodType, got.PeriodType)
	require.Equal(t, orig.Schedule, got.Schedule)
	require.Equal(t, orig.Notifications, got.Notifications)
	require.Equal(t, orig.Labels, got.Labels)
	require.True(t, orig.CreatedAt.Equal(got.CreatedAt), "createdAt: %s != %s", orig.CreatedAt, got.CreatedAt)
	require.True(t, orig.UpdatedAt.Equal(got.UpdatedAt))

	// enabled must be a typed BOOL value, not a stringified "true".
	require.True(t, md[schema.MetaEnabled].GetBoolValue())
}

func TestRuleMetadataMinimal(t *testing.T) {
	t.Parallel()

	// A rule with no schedule / notifications / labels omits those keys entirely.
	id := uuid.New()
	orig := &models.Rule{
		ID:           id,
		Name:         "minimal",
		TemplateKind: models.TemplateBalanceBounds,
		TemplateSpec: json.RawMessage(`{}`),
		Enabled:      false,
		Severity:     models.SeverityLow,
		PeriodType:   models.PeriodTypeContinuous,
		CreatedAt:    time.Now().Truncate(time.Microsecond).UTC(),
		UpdatedAt:    time.Now().Truncate(time.Microsecond).UTC(),
	}

	md, err := ruleToMetadata(orig)
	require.NoError(t, err)
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

func TestRuleMetadataContractVersionCompatibility(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	legacy := &commonpb.Account{
		Address:  schema.RuleAccount(id.String()),
		Metadata: map[string]*commonpb.MetadataValue{},
	}
	got, err := ruleFromAccount(legacy)
	require.NoError(t, err)
	// A record written before the stamp existed used to decode as V1. V1 is
	// retired, so it now resolves to the live contract and stays visible instead
	// of being filtered out of every list forever.
	require.Equal(t, models.ContractVersionV2, got.ContractVersion)

	rule := &models.Rule{ID: id, ContractVersion: models.ContractVersionV2}
	md, err := ruleToMetadata(rule)
	require.NoError(t, err)
	require.Equal(t, "2", md[schema.MetaContractVersion].GetStringValue())

	got, err = ruleFromAccount(&commonpb.Account{Address: schema.RuleAccount(id.String()), Metadata: md})
	require.NoError(t, err)
	require.Equal(t, models.ContractVersionV2, got.ContractVersion)
}
