package ledgerstore

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	recstore "github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestSnoozeAlert_SetsSnoozeMetadata(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertOpen)
	expectFindByID(t, client, a, "1")

	client.EXPECT().
		SaveAccountMetadataValues(gomock.Any(), testControl, itemAddrOf(a), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, md map[string]*commonpb.MetadataValue) error {
			// Only the snooze key is written (status-neutral, no marker move).
			require.Contains(t, md, schema.MetaSnooze)
			require.Len(t, md, 1)

			return nil
		})

	got, err := store.SnoozeAlert(context.Background(), a.ID, time.Now().Add(time.Hour), "ops", "on it")
	require.NoError(t, err)
	require.NotNil(t, got.Snooze)
	require.Equal(t, "ops", got.Snooze.By)
}

func TestSnoozeAlert_RejectsPastUntil(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	// No ledger call — the past-until guard fires before any lookup.
	store := New(NewMockledgerClient(ctrl), testControl)

	_, err := store.SnoozeAlert(context.Background(), uuid.New(), time.Now().Add(-time.Hour), "ops", "")
	require.Error(t, err)
}

func TestSnoozeAlert_Resolved_NotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertResolved)
	expectFindByID(t, client, a, "1")

	_, err := store.SnoozeAlert(context.Background(), a.ID, time.Now().Add(time.Hour), "ops", "")
	require.ErrorIs(t, err, recstore.ErrNotFound)
}

func TestUnsnoozeAlert_DeletesSnooze(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertAcknowledged)
	a.Snooze = &models.Snooze{Until: time.Now().Add(time.Hour), By: "ops", At: time.Now().UTC()}
	expectFindByID(t, client, a, "1")

	client.EXPECT().
		DeleteAccountMetadata(gomock.Any(), testControl, itemAddrOf(a), schema.MetaSnooze).
		Return(nil)

	got, err := store.UnsnoozeAlert(context.Background(), a.ID, "ops")
	require.NoError(t, err)
	require.Nil(t, got.Snooze)
}

func TestUnsnoozeAlert_NoSnooze_NoOp(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	a := activeAlert(models.AlertOpen) // no snooze
	expectFindByID(t, client, a, "1")
	// No DeleteAccountMetadata expected.

	got, err := store.UnsnoozeAlert(context.Background(), a.ID, "ops")
	require.NoError(t, err)
	require.Nil(t, got.Snooze)
}
