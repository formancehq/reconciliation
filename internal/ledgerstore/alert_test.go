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
	"github.com/formancehq/reconciliation/internal/storage"
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

// priorAccount builds the item-account snapshot GetAccount would return for an
// existing alert with the given status/occurrence and optional closure state.
func priorAccount(t *testing.T, a *models.Alert, occ string) *commonpb.Account {
	t.Helper()

	md, err := alertToMetadata(a)
	require.NoError(t, err)

	return &commonpb.Account{
		Address:  schema.AlertItemAccount(a.RuleID.String(), a.PeriodID, schema.FingerprintHash(a.Fingerprint)),
		Metadata: md,
		Volumes:  map[string]*commonpb.VolumesWithBalance{schema.AssetOcc: {Balance: occ}},
	}
}

func sampleInput() storage.OpenAlertInput {
	return storage.OpenAlertInput{
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
	issued := schema.IssuedPoolAccount(in.RuleID.String(), in.PeriodID)

	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddr, gomock.Any()).Return(nil, notFound())

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			require.Equal(t, testControl, tx.Ledger)
			require.Equal(t, alertBatchKey(in), tx.IdempotencyKey)
			// Mints the marker into st:open (from the issuance pool, overdraft)
			// and the first OCC unit.
			require.Contains(t, tx.Script, "[ALERT 1]")
			require.Contains(t, tx.Script, "@"+issued+" allowing unbounded overdraft")
			require.Contains(t, tx.Script, "@"+stOpen)
			require.Contains(t, tx.Script, "[OCC 1]")
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

	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddr, gomock.Any()).Return(priorAccount(t, prior, "2"), nil)

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			require.Equal(t, alertBatchKey(in), tx.IdempotencyKey)
			// A repeat only bumps OCC — the marker already sits at st:open.
			require.Contains(t, tx.Script, "[OCC 1]")
			require.NotContains(t, tx.Script, "[ALERT 1]")
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
	stResolved := schema.AlertStateAccount(schema.StateResolved, in.RuleID.String(), in.PeriodID, fpHash)
	stOpen := schema.AlertStateAccount(schema.StateOpen, in.RuleID.String(), in.PeriodID, fpHash)

	prior := &models.Alert{
		ID: uuid.New(), RuleID: in.RuleID, Fingerprint: in.Fingerprint, PeriodID: in.PeriodID,
		Status: models.AlertResolved, Severity: models.SeverityHigh,
		FirstSeenAt: in.OccurredAt.Add(-2 * time.Hour), LastSeenAt: in.OccurredAt.Add(-time.Hour),
		CreatedAt:  in.OccurredAt.Add(-2 * time.Hour),
		Resolution: &models.Resolution{Kind: models.ResolutionAuto, By: "system", At: in.OccurredAt.Add(-time.Hour)},
	}

	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddr, gomock.Any()).Return(priorAccount(t, prior, "5"), nil)

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			// Guarded move st:resolved → st:open (bare source = CAS) + OCC bump.
			require.Contains(t, tx.Script, "[ALERT 1]")
			require.Contains(t, tx.Script, "@"+stResolved)
			require.Contains(t, tx.Script, "@"+stOpen)
			require.NotContains(t, tx.Script, "@"+stResolved+" allowing unbounded overdraft")
			require.Contains(t, tx.Script, "[OCC 1]")
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

	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddr, gomock.Any()).Return(priorAccount(t, prior, "1"), nil)

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			require.Contains(t, tx.Script, "@"+stAck)
			require.Contains(t, tx.Script, "[OCC 1]")
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
