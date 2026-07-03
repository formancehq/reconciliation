package ledgerstore

import (
	"context"
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
)

// activeAlert builds a decoded alert in the given status for transition tests.
func activeAlert(status models.AlertStatus) *models.Alert {
	now := time.Now().Truncate(time.Microsecond).UTC()

	return &models.Alert{
		ID: uuid.New(), RuleID: uuid.New(), Fingerprint: "asset:USD/2|acct:x", PeriodID: "2026-03",
		Status: status, Severity: models.SeverityHigh,
		FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now, CreatedAt: now.Add(-time.Hour),
	}
}

// expectFindByID stubs the id→item resolution to return a for its id.
func expectFindByID(t *testing.T, client *MockledgerClient, a *models.Alert, occ string) {
	t.Helper()

	client.EXPECT().
		QueryAccounts(gomock.Any(), testControl, gomock.Any(), uint64(0)).
		Return([]*commonpb.Account{priorAccount(t, a, occ)}, nil)
}

func itemAddrOf(a *models.Alert) string {
	return schema.AlertItemAccount(a.RuleID.String(), a.PeriodID, schema.FingerprintHash(a.Fingerprint))
}

func stAddrOf(a *models.Alert, state string) string {
	return schema.AlertStateAccount(state, a.RuleID.String(), a.PeriodID, schema.FingerprintHash(a.Fingerprint))
}

func TestAckAlert_OpenToAck(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertOpen)
	expectFindByID(t, client, a, "1")

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			require.Equal(t, schema.NumscriptAlertMove, tx.ScriptName)
			require.Equal(t, stAddrOf(a, schema.StateOpen), tx.Vars[schema.VarStFrom])
			require.Equal(t, stAddrOf(a, schema.StateAck), tx.Vars[schema.VarStTo])
			item := tx.AccountMetadata[itemAddrOf(a)]
			require.Equal(t, "ACKNOWLEDGED", item.Values[schema.MetaStatus].GetStringValue())
			require.Contains(t, item.Values, schema.MetaAck)
			require.Empty(t, tx.DeleteMetadata)

			return nil
		})

	got, err := store.AckAlert(context.Background(), a.ID, &models.Ack{By: "ops", At: time.Now().UTC()})
	require.NoError(t, err)
	require.Equal(t, models.AlertAcknowledged, got.Status)
}

func TestAckAlert_AlreadyAcked_NoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertAcknowledged)
	expectFindByID(t, client, a, "1")
	// No CreateTransaction expected — a re-ack writes nothing.

	got, err := store.AckAlert(context.Background(), a.ID, &models.Ack{By: "ops", At: time.Now().UTC()})
	require.NoError(t, err)
	require.Equal(t, models.AlertAcknowledged, got.Status)
}

func TestAckAlert_Resolved_NotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertResolved)
	expectFindByID(t, client, a, "1")

	_, err := store.AckAlert(context.Background(), a.ID, &models.Ack{By: "ops", At: time.Now().UTC()})
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestResolveAlertManual_ClearsSnooze(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertAcknowledged)
	a.Snooze = &models.Snooze{Until: time.Now().Add(time.Hour), By: "ops", At: time.Now().UTC()}
	expectFindByID(t, client, a, "3")

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			// Guarded move from the current state (ACK) to resolved.
			require.Equal(t, schema.NumscriptAlertMove, tx.ScriptName)
			require.Equal(t, stAddrOf(a, schema.StateAck), tx.Vars[schema.VarStFrom])
			require.Equal(t, stAddrOf(a, schema.StateResolved), tx.Vars[schema.VarStTo])
			item := tx.AccountMetadata[itemAddrOf(a)]
			require.Equal(t, "RESOLVED", item.Values[schema.MetaStatus].GetStringValue())
			require.Contains(t, item.Values, schema.MetaResolution)
			// The live snooze is cleared on close.
			require.Equal(t, []string{schema.MetaSnooze}, tx.DeleteMetadata[itemAddrOf(a)])

			return nil
		})

	got, err := store.ResolveAlertManual(context.Background(), a.ID,
		&models.Resolution{Kind: models.ResolutionFixedByBooking, By: "ops", At: time.Now().UTC()})
	require.NoError(t, err)
	require.Equal(t, models.AlertResolved, got.Status)
	require.Nil(t, got.Snooze)
}

func TestResolveAlertManual_AlreadyResolved_NotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertResolved)
	expectFindByID(t, client, a, "1")

	_, err := store.ResolveAlertManual(context.Background(), a.ID,
		&models.Resolution{Kind: models.ResolutionFixedByBooking, By: "ops", At: time.Now().UTC()})
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestResolveAlertManual_RejectsWrongKind(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	store := New(NewMockledgerClient(ctrl), testControl)

	_, err := store.ResolveAlertManual(context.Background(), uuid.New(),
		&models.Resolution{Kind: models.ResolutionAcceptedByBusiness})
	require.Error(t, err) // wrong kind is rejected before any ledger call
}

func TestAcceptAlert_RequiresNote(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	store := New(NewMockledgerClient(ctrl), testControl)

	_, err := store.AcceptAlert(context.Background(), uuid.New(),
		&models.Resolution{Kind: models.ResolutionAcceptedByBusiness, By: "cfo", At: time.Now().UTC()})
	require.Error(t, err)
}

func TestAutoResolveAlert_OpenToResolved(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertOpen)
	evalID := uuid.New()

	// Auto-resolve is addressed structurally (GetAccount), not by id lookup.
	client.EXPECT().
		GetAccount(gomock.Any(), testControl, itemAddrOf(a), gomock.Any()).
		Return(priorAccount(t, a, "2"), nil)

	client.EXPECT().CreateTransaction(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tx ledger.CreateTransactionInput) error {
			require.Equal(t, schema.NumscriptAlertMove, tx.ScriptName)
			require.Equal(t, stAddrOf(a, schema.StateOpen), tx.Vars[schema.VarStFrom])
			require.Equal(t, stAddrOf(a, schema.StateResolved), tx.Vars[schema.VarStTo])
			require.Equal(t, "RESOLVED", tx.AccountMetadata[itemAddrOf(a)].Values[schema.MetaStatus].GetStringValue())

			return nil
		})

	got, err := store.AutoResolveAlert(context.Background(), a.RuleID, a.Fingerprint, a.PeriodID, evalID, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, models.AlertResolved, got.Status)
	require.Equal(t, models.ResolutionAuto, got.Resolution.Kind)
	require.Equal(t, evalID, got.LastEvaluationID)
}

func TestAutoResolveAlert_NoActiveAlert_NoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	ruleID := uuid.New()

	// No item account → nothing to resolve, no transaction.
	client.EXPECT().GetAccount(gomock.Any(), testControl, gomock.Any(), gomock.Any()).Return(nil, notFound())

	got, err := store.AutoResolveAlert(context.Background(), ruleID, "fp", "2026-03", uuid.New(), time.Now().UTC())
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestAutoResolveAlert_AlreadyResolved_NoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertResolved)
	client.EXPECT().GetAccount(gomock.Any(), testControl, itemAddrOf(a), gomock.Any()).Return(priorAccount(t, a, "1"), nil)
	// No CreateTransaction — already closed.

	got, err := store.AutoResolveAlert(context.Background(), a.RuleID, a.Fingerprint, a.PeriodID, uuid.New(), time.Now().UTC())
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestListActiveAlertFingerprints(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	ruleID := uuid.New()
	const period = "2026-03"

	mk := func(fp string, status models.AlertStatus) *commonpb.Account {
		a := &models.Alert{
			ID: uuid.New(), RuleID: ruleID, Fingerprint: fp, PeriodID: period,
			Status: status, Severity: models.SeverityLow,
			FirstSeenAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
		}

		return priorAccount(t, a, "1")
	}

	client.EXPECT().
		QueryAccounts(gomock.Any(), testControl, gomock.Any(), uint64(0)).
		Return([]*commonpb.Account{
			mk("fp-open", models.AlertOpen),
			mk("fp-ack", models.AlertAcknowledged),
			mk("fp-resolved", models.AlertResolved),
		}, nil)

	fps, err := store.ListActiveAlertFingerprints(context.Background(), ruleID, period)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"fp-open", "fp-ack"}, fps, "only OPEN/ACK are active")
}
