package ledgerstore

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/go-libs/query"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	recstore "github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func ruleAccount(t *testing.T, r *models.Rule) *commonpb.Account {
	t.Helper()

	md, err := ruleToMetadata(r)
	require.NoError(t, err)

	return &commonpb.Account{Address: schema.RuleAccount(r.ID.String()), Metadata: md}
}

func TestListRules_SortedAndPaginated(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	base := time.Now().Truncate(time.Microsecond).UTC()
	mk := func(name string, ageHours int) *models.Rule {
		return &models.Rule{
			ID: uuid.New(), Name: name, TemplateKind: models.TemplateLedgerInvariant,
			Enabled: true, Severity: models.SeverityLow, PeriodType: models.PeriodTypeContinuous,
			CreatedAt: base.Add(-time.Duration(ageHours) * time.Hour), UpdatedAt: base,
		}
	}

	// Returned in arbitrary order; ListRules must sort created_at DESC (newest first).
	newest, mid, oldest := mk("newest", 0), mk("mid", 1), mk("oldest", 2)
	client.EXPECT().
		QueryAccounts(gomock.Any(), testControl, gomock.Any()).
		Return([]*commonpb.Account{ruleAccount(t, mid), ruleAccount(t, oldest), ruleAccount(t, newest)}, nil)

	// Page 1: pageSize 2 → [newest, mid], more to come.
	q := recstore.GetRulesQuery{PageSize: 2}
	page, err := store.ListRules(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, page.Data, 2)
	require.Equal(t, "newest", page.Data[0].Name)
	require.Equal(t, "mid", page.Data[1].Name)
	require.True(t, page.HasMore)
	require.NotEmpty(t, page.Next)
}

func TestListRules_SecondPage(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	base := time.Now().Truncate(time.Microsecond).UTC()
	mk := func(name string, ageHours int) *models.Rule {
		return &models.Rule{
			ID: uuid.New(), Name: name, CreatedAt: base.Add(-time.Duration(ageHours) * time.Hour), UpdatedAt: base,
		}
	}

	client.EXPECT().
		QueryAccounts(gomock.Any(), testControl, gomock.Any()).
		Return([]*commonpb.Account{ruleAccount(t, mk("newest", 0)), ruleAccount(t, mk("mid", 1)), ruleAccount(t, mk("oldest", 2))}, nil)

	// Page 2: offset 2, pageSize 2 → [oldest], no more.
	page, err := store.ListRules(context.Background(), recstore.GetRulesQuery{PageSize: 2, Offset: 2})
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	require.Equal(t, "oldest", page.Data[0].Name)
	require.False(t, page.HasMore)
	require.Empty(t, page.Next)
	require.NotEmpty(t, page.Previous)
}

func TestListRules_FiltersContractBeforePagination(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)
	base := time.Now().Truncate(time.Microsecond).UTC()
	v1 := &models.Rule{ID: uuid.New(), Name: "v1", ContractVersion: models.ContractVersionV1, CreatedAt: base.Add(-time.Hour), UpdatedAt: base}
	v2 := &models.Rule{ID: uuid.New(), Name: "v2-newest", ContractVersion: models.ContractVersionV2, CreatedAt: base, UpdatedAt: base}
	client.EXPECT().QueryAccounts(gomock.Any(), testControl, gomock.Any()).Return([]*commonpb.Account{ruleAccount(t, v2), ruleAccount(t, v1)}, nil)

	version := models.ContractVersionV1
	q := recstore.NewGetRulesQuery(recstore.NewPaginatedQueryOptions(recstore.RulesFilters{ContractVersion: &version}).WithPageSize(1))
	page, err := store.ListRules(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	require.Equal(t, "v1", page.Data[0].Name)
	require.False(t, page.HasMore)
}

func TestListAlerts_SortedByLastSeen(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)

	base := time.Now().Truncate(time.Microsecond).UTC()
	mk := func(fp string, ageHours int) *models.Alert {
		return &models.Alert{
			ID: uuid.New(), RuleID: uuid.New(), Fingerprint: fp, PeriodID: "2026-03",
			Status: models.AlertOpen, Severity: models.SeverityHigh,
			FirstSeenAt: base.Add(-24 * time.Hour), LastSeenAt: base.Add(-time.Duration(ageHours) * time.Hour),
			CreatedAt: base.Add(-24 * time.Hour),
		}
	}

	client.EXPECT().
		QueryAccounts(gomock.Any(), testControl, gomock.Any()).
		Return([]*commonpb.Account{priorAccount(t, mk("fp-old", 5), "1"), priorAccount(t, mk("fp-fresh", 0), "1")}, nil)

	page, err := store.ListAlerts(context.Background(), recstore.GetAlertsQuery{PageSize: 10})
	require.NoError(t, err)
	require.Len(t, page.Data, 2)
	require.Equal(t, "fp-fresh", page.Data[0].Fingerprint, "most-recently-seen first")
	require.Equal(t, "fp-old", page.Data[1].Fingerprint)
	require.False(t, page.HasMore)
}

func TestListAlerts_FiltersContractBeforePagination(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	store := New(client, testControl)
	base := time.Now().Truncate(time.Microsecond).UTC()
	v1 := &models.Alert{ID: uuid.New(), RuleID: uuid.New(), Fingerprint: "v1", ContractVersion: models.ContractVersionV1, PeriodID: "continuous", Status: models.AlertOpen, LastSeenAt: base.Add(-time.Hour), CreatedAt: base}
	v2 := &models.Alert{ID: uuid.New(), RuleID: uuid.New(), Fingerprint: "v2-newest", ContractVersion: models.ContractVersionV2, PeriodID: "continuous", Status: models.AlertOpen, LastSeenAt: base, CreatedAt: base}
	client.EXPECT().QueryAccounts(gomock.Any(), testControl, gomock.Any()).Return([]*commonpb.Account{priorAccount(t, v2, "1"), priorAccount(t, v1, "1")}, nil)

	version := models.ContractVersionV1
	q := recstore.NewGetAlertsQuery(recstore.NewPaginatedQueryOptions(recstore.AlertsFilters{ContractVersion: &version}).WithPageSize(1))
	page, err := store.ListAlerts(context.Background(), q)
	require.NoError(t, err)
	require.Len(t, page.Data, 1)
	require.Equal(t, "v1", page.Data[0].Fingerprint)
	require.False(t, page.HasMore)
}

func TestListAlerts_InvalidFilter(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	// No QueryAccounts call — translation fails before any ledger read.
	store := New(NewMockledgerClient(ctrl), testControl)

	opts := recstore.PaginatedQueryOptions[recstore.AlertsFilters]{PageSize: 10}.WithQueryBuilder(query.Match("bogus", "x"))
	_, err := store.ListAlerts(context.Background(), recstore.NewGetAlertsQuery(opts))
	require.ErrorIs(t, err, recstore.ErrInvalidQuery)
}
