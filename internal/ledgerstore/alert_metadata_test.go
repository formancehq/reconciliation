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

func TestAlertMetadataRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Now().Truncate(time.Microsecond).UTC()
	id, ruleID, evalID := uuid.New(), uuid.New(), uuid.New()

	orig := &models.Alert{
		ID:               id,
		RuleID:           ruleID,
		Fingerprint:      "asset:USD/2|account:merchant:m1:held",
		PeriodID:         "2026-03",
		Status:           models.AlertAcknowledged,
		Severity:         models.SeverityHigh,
		FirstSeenAt:      now.Add(-time.Hour),
		LastSeenAt:       now,
		OccurrenceCount:  3, // sourced from the OCC balance below
		LastEvaluationID: evalID,
		Evidence:         json.RawMessage(`{"drift":"12"}`),
		Ack:              &models.Ack{By: "ops", At: now, Note: "looking"},
		Resolution:       &models.Resolution{Kind: models.ResolutionAcceptedByBusiness, By: "cfo", At: now, Note: "known"},
		Snooze:           &models.Snooze{Until: now.Add(time.Hour), By: "ops", At: now},
		Labels:           map[string]string{"env": "prod"},
		CreatedAt:        now.Add(-time.Hour),
	}

	md, err := alertToMetadata(orig)
	require.NoError(t, err)

	// OccurrenceCount is NOT in metadata — it is the OCC balance.
	require.NotContains(t, md, "occurrence_count")

	acct := &commonpb.Account{
		Address:  schema.AlertItemAccount(ruleID.String(), orig.PeriodID, schema.FingerprintHash(orig.Fingerprint)),
		Metadata: md,
		Volumes:  []*commonpb.AccountVolume{{Asset: schema.AssetOcc, Volumes: &commonpb.VolumesWithBalance{Balance: "3"}}},
	}

	got, err := alertFromAccount(acct)
	require.NoError(t, err)

	require.Equal(t, orig.ID, got.ID)
	require.Equal(t, orig.RuleID, got.RuleID)
	require.Equal(t, orig.Fingerprint, got.Fingerprint)
	require.Equal(t, orig.PeriodID, got.PeriodID)
	require.Equal(t, orig.Status, got.Status)
	require.Equal(t, orig.Severity, got.Severity)
	require.Equal(t, orig.LastEvaluationID, got.LastEvaluationID)
	require.Equal(t, int64(3), got.OccurrenceCount)
	require.JSONEq(t, string(orig.Evidence), string(got.Evidence))
	require.Equal(t, orig.Ack, got.Ack)
	require.Equal(t, orig.Resolution, got.Resolution)
	require.Equal(t, orig.Snooze, got.Snooze)
	require.Equal(t, orig.Labels, got.Labels)
	require.True(t, orig.FirstSeenAt.Equal(got.FirstSeenAt))
	require.True(t, orig.LastSeenAt.Equal(got.LastSeenAt))
}

func TestAlertMetadataNoOccVolume(t *testing.T) {
	t.Parallel()

	id, ruleID := uuid.New(), uuid.New()
	orig := &models.Alert{
		ID: id, RuleID: ruleID, Fingerprint: "fp", PeriodID: "continuous",
		Status: models.AlertOpen, Severity: models.SeverityLow,
		FirstSeenAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}

	md, err := alertToMetadata(orig)
	require.NoError(t, err)

	got, err := alertFromAccount(&commonpb.Account{Metadata: md})
	require.NoError(t, err)
	require.Zero(t, got.OccurrenceCount) // no OCC volume → 0
	require.Nil(t, got.Ack)
	require.Nil(t, got.Resolution)
	require.Nil(t, got.Snooze)
	require.Empty(t, got.Labels)
}

func TestAlertMetadataContractVersionCompatibility(t *testing.T) {
	t.Parallel()

	base := &models.Alert{ID: uuid.New(), RuleID: uuid.New()}
	md, err := alertToMetadata(base)
	require.NoError(t, err)
	delete(md, schema.MetaContractVersion)
	got, err := alertFromAccount(&commonpb.Account{Metadata: md})
	require.NoError(t, err)
	// A record written before the stamp existed used to decode as V1. V1 is
	// retired, so it now resolves to the live contract and stays visible instead
	// of being filtered out of every list forever.
	require.Equal(t, models.ContractVersionV2, got.ContractVersion)

	base.ContractVersion = models.ContractVersionV2
	md, err = alertToMetadata(base)
	require.NoError(t, err)
	require.Equal(t, "2", md[schema.MetaContractVersion].GetStringValue())
	got, err = alertFromAccount(&commonpb.Account{Metadata: md})
	require.NoError(t, err)
	require.Equal(t, models.ContractVersionV2, got.ContractVersion)
}

func TestStatusStateMapping(t *testing.T) {
	t.Parallel()

	for _, st := range []models.AlertStatus{models.AlertOpen, models.AlertAcknowledged, models.AlertResolved} {
		require.Equal(t, st, stateToStatus(statusToState(st)), "round-trip status %s", st)
	}

	require.Equal(t, schema.StateResolved, statusToState(models.AlertResolved))
	require.Empty(t, statusToState(models.AlertStatus("BOGUS")))
}
