package ledgerstore

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/models"
	recstore "github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestGetAlert_Found(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	now := time.Now().Truncate(time.Microsecond).UTC()
	prior := &models.Alert{
		ID: uuid.New(), RuleID: uuid.New(), Fingerprint: "fp:x", PeriodID: "2026-03",
		Status: models.AlertOpen, Severity: models.SeverityHigh,
		FirstSeenAt: now, LastSeenAt: now, CreatedAt: now,
	}

	// The store resolves by the indexed `id` metadata, scoped to the item prefix.
	client.EXPECT().
		QueryAccounts(gomock.Any(), testControl, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, filter *commonpb.QueryFilter) ([]*commonpb.Account, error) {
			require.NotNil(t, filter.GetAnd(), "id lookup ANDs the item-prefix and id conditions")

			return []*commonpb.Account{priorAccount(t, prior, "4")}, nil
		})

	got, err := store.GetAlert(context.Background(), prior.ID)
	require.NoError(t, err)
	require.Equal(t, prior.ID, got.ID)
	require.Equal(t, models.AlertOpen, got.Status)
	require.Equal(t, int64(4), got.OccurrenceCount)
}

func TestGetAlert_NotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	client.EXPECT().
		QueryAccounts(gomock.Any(), testControl, gomock.Any()).
		Return(nil, nil)

	_, err := store.GetAlert(context.Background(), uuid.New())
	require.ErrorIs(t, err, recstore.ErrNotFound)
}
