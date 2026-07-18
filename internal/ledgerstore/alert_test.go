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
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testControl = "recon-test"

// notFound returns a gRPC NotFound error, as the ledger reports for a missing
// account (so readAlertItem treats it as "no prior alert").
func notFound() error { return status.Error(codes.NotFound, "account not found") }

func TestUniqueActionKeyDoesNotReuseBusinessIdentity(t *testing.T) {
	t.Parallel()

	first := uniqueActionKey("rule-patch", "rule", "revision")
	second := uniqueActionKey("rule-patch", "rule", "revision")
	require.NotEqual(t, first, second)
}

// priorAccount builds the item-account snapshot GetAccount would return for an
// existing alert with the given status/occurrence and optional closure state.
func priorAccount(t *testing.T, a *models.Alert, occ string) *commonpb.Account {
	t.Helper()

	md, err := alertToMetadata(a)
	require.NoError(t, err)

	return &commonpb.Account{
		Address:  schema.AlertItemAccount(a.RuleID.String(), a.PeriodID, schema.FingerprintHash(a.Fingerprint)),
		Metadata: md,
		Volumes: []*commonpb.AccountVolume{
			{Asset: schema.AssetOcc, Volumes: &commonpb.VolumesWithBalance{Balance: occ}},
		},
	}
}

func sampleInput() recstore.OpenAlertInput {
	return recstore.OpenAlertInput{
		RuleID:       uuid.New(),
		Fingerprint:  "asset:USD/2|account:merchant:m1:held",
		PeriodID:     "2026-03",
		Severity:     models.SeverityHigh,
		EvaluationID: uuid.New(),
		Evidence:     json.RawMessage(`{"drift":"42"}`),
		Labels:       map[string]string{"env": "prod"},
		OccurredAt:   time.Now().Truncate(time.Microsecond).UTC(),
	}
}

func TestOpenOrUpdateAlert_NewOpen(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	in := sampleInput()
	fpHash := schema.FingerprintHash(in.Fingerprint)
	itemAddr := schema.AlertItemAccount(in.RuleID.String(), in.PeriodID, fpHash)
	stOpen := schema.AlertStateAccount(schema.StateOpen, in.RuleID.String(), in.PeriodID, fpHash)
	pool := schema.PoolAccount(in.RuleID.String())

	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddr).Return(nil, notFound())

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			require.Equal(t, testControl, tx.Ledger)
			require.Equal(t, alertBatchKey(in), tx.IdempotencyKey)
			// Opens via the alert_open library script: pool → st:open marker + item OCC.
			require.Equal(t, schema.NumscriptAlertOpen, tx.ScriptName)
			require.Equal(t, schema.NumscriptVersion, tx.ScriptVersion)
			require.Equal(t, pool, tx.Vars[schema.VarPool])
			require.Equal(t, stOpen, tx.Vars[schema.VarStOpen])
			require.Equal(t, itemAddr, tx.Vars[schema.VarItem])
			require.Empty(t, tx.DeleteMetadata)
			// Status mirror + descriptive metadata land on the item account.
			item := tx.AccountMetadata[itemAddr]
			require.NotNil(t, item)
			require.Equal(t, "OPEN", item.Values[schema.MetaStatus].GetStringValue())
			require.NotContains(t, item.Values, "occurrence_count")

			return nil
		})

	res, err := store.OpenOrUpdateAlert(context.Background(), in)
	require.NoError(t, err)
	require.True(t, res.Created)
	require.False(t, res.Reopened)
	require.Nil(t, res.Event) // history rides the ledger event stream
	require.Equal(t, models.AlertOpen, res.Alert.Status)
	require.Equal(t, int64(1), res.Alert.OccurrenceCount)
	require.Equal(t, in.Fingerprint, res.Alert.Fingerprint)
}

func TestOpenOrUpdateAlert_Repeat(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	in := sampleInput()
	fpHash := schema.FingerprintHash(in.Fingerprint)
	itemAddr := schema.AlertItemAccount(in.RuleID.String(), in.PeriodID, fpHash)

	prior := &models.Alert{
		ID: uuid.New(), RuleID: in.RuleID, Fingerprint: in.Fingerprint, PeriodID: in.PeriodID,
		Status: models.AlertOpen, Severity: models.SeverityHigh,
		FirstSeenAt: in.OccurredAt.Add(-time.Hour), LastSeenAt: in.OccurredAt.Add(-time.Hour),
		CreatedAt: in.OccurredAt.Add(-time.Hour),
	}

	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddr).Return(priorAccount(t, prior, "2"), nil)

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			require.Equal(t, alertBatchKey(in), tx.IdempotencyKey)
			// A repeat uses alert_bump (OCC only) — the marker already sits at st:open.
			require.Equal(t, schema.NumscriptAlertBump, tx.ScriptName)
			require.Equal(t, schema.PoolAccount(in.RuleID.String()), tx.Vars[schema.VarPool])
			require.Equal(t, itemAddr, tx.Vars[schema.VarItem])
			require.NotContains(t, tx.Vars, schema.VarStFrom)
			require.Empty(t, tx.DeleteMetadata)
			require.Equal(t, "OPEN", tx.AccountMetadata[itemAddr].Values[schema.MetaStatus].GetStringValue())

			return nil
		})

	res, err := store.OpenOrUpdateAlert(context.Background(), in)
	require.NoError(t, err)
	require.False(t, res.Created)
	require.False(t, res.Reopened)
	require.Equal(t, int64(3), res.Alert.OccurrenceCount) // 2 (prior OCC balance) + 1
	require.Equal(t, prior.ID, res.Alert.ID)              // identity preserved
	require.Equal(t, in.OccurredAt, res.Alert.LastSeenAt)
}

func TestOpenOrUpdateAlert_Reopen(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	in := sampleInput()
	fpHash := schema.FingerprintHash(in.Fingerprint)
	itemAddr := schema.AlertItemAccount(in.RuleID.String(), in.PeriodID, fpHash)
	stOpen := schema.AlertStateAccount(schema.StateOpen, in.RuleID.String(), in.PeriodID, fpHash)

	prior := &models.Alert{
		ID: uuid.New(), RuleID: in.RuleID, Fingerprint: in.Fingerprint, PeriodID: in.PeriodID,
		Status: models.AlertResolved, Severity: models.SeverityHigh,
		FirstSeenAt: in.OccurredAt.Add(-2 * time.Hour), LastSeenAt: in.OccurredAt.Add(-time.Hour),
		CreatedAt:  in.OccurredAt.Add(-2 * time.Hour),
		Resolution: &models.Resolution{Kind: models.ResolutionAuto, By: "system", At: in.OccurredAt.Add(-time.Hour)},
	}

	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddr).Return(priorAccount(t, prior, "5"), nil)

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			// Reopen re-mints the marker (it was burned on close) via alert_open —
			// no st:resolved to move from.
			require.Equal(t, schema.NumscriptAlertOpen, tx.ScriptName)
			require.Equal(t, stOpen, tx.Vars[schema.VarStOpen])
			require.Equal(t, itemAddr, tx.Vars[schema.VarItem])
			require.NotContains(t, tx.Vars, schema.VarStFrom, "no marker to move from")
			// The prior resolution is dropped (present → deleted).
			require.Equal(t, []string{schema.MetaResolution}, tx.DeleteMetadata[itemAddr])
			item := tx.AccountMetadata[itemAddr]
			require.Equal(t, "OPEN", item.Values[schema.MetaStatus].GetStringValue())
			require.NotContains(t, item.Values, schema.MetaResolution)

			return nil
		})

	res, err := store.OpenOrUpdateAlert(context.Background(), in)
	require.NoError(t, err)
	require.False(t, res.Created)
	require.True(t, res.Reopened)
	require.Equal(t, models.AlertOpen, res.Alert.Status)
	require.Nil(t, res.Alert.Resolution)
	require.Equal(t, int64(6), res.Alert.OccurrenceCount) // 5 + 1
}

// TestOpenOrUpdateAlert_ResurfaceFromAck covers the ACK→OPEN path: a guarded
// marker move like reopen, but Reopened stays false and the ack is preserved
// (matches the Postgres store). Not reachable until AckAlert lands (step 3c-3),
// but exercised here so the branch is correct when it does.
func TestOpenOrUpdateAlert_ResurfaceFromAck(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	in := sampleInput()
	fpHash := schema.FingerprintHash(in.Fingerprint)
	itemAddr := schema.AlertItemAccount(in.RuleID.String(), in.PeriodID, fpHash)
	stAck := schema.AlertStateAccount(schema.StateAck, in.RuleID.String(), in.PeriodID, fpHash)

	prior := &models.Alert{
		ID: uuid.New(), RuleID: in.RuleID, Fingerprint: in.Fingerprint, PeriodID: in.PeriodID,
		Status: models.AlertAcknowledged, Severity: models.SeverityHigh,
		FirstSeenAt: in.OccurredAt.Add(-time.Hour), LastSeenAt: in.OccurredAt.Add(-time.Hour),
		CreatedAt: in.OccurredAt.Add(-time.Hour),
		Ack:       &models.Ack{By: "ops", At: in.OccurredAt.Add(-time.Hour), Note: "looking"},
	}

	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddr).Return(priorAccount(t, prior, "1"), nil)

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			// alert_reopen guarded move from st:ack (resurface).
			require.Equal(t, schema.NumscriptAlertReopen, tx.ScriptName)
			require.Equal(t, stAck, tx.Vars[schema.VarStFrom])
			// Ack is NOT cleared on a resurface.
			require.Empty(t, tx.DeleteMetadata)
			require.Contains(t, tx.AccountMetadata[itemAddr].Values, schema.MetaAck)

			return nil
		})

	res, err := store.OpenOrUpdateAlert(context.Background(), in)
	require.NoError(t, err)
	require.False(t, res.Reopened)
	require.Equal(t, models.AlertOpen, res.Alert.Status)
	require.NotNil(t, res.Alert.Ack)
}

// TestAlertBatchKey_Deterministic asserts the idempotency key is stable across
// calls and independent of OccurredAt (so a replay produces the same key) but
// varies with the evaluation identity.
func TestAlertBatchKey_Deterministic(t *testing.T) {
	t.Parallel()

	in := sampleInput()
	k1 := alertBatchKey(in)

	in2 := in
	in2.OccurredAt = in.OccurredAt.Add(time.Hour)
	require.Equal(t, k1, alertBatchKey(in2), "key must not depend on OccurredAt")

	in3 := in
	in3.EvaluationID = uuid.New()
	require.NotEqual(t, k1, alertBatchKey(in3), "key must vary with evaluationID")
}
