package ledgerstore

import (
	"context"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestListRuleActivitiesCombinesAndOrdersKinds(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	ruleID := uuid.New()
	t0 := time.Now().UTC()
	makeTx := func(id uint64, kind string, at time.Time) *commonpb.Transaction {
		md, err := activityMetadata(kind, ruleID, models.ContractVersionV2, "sha256:r", "eval", at, map[string]any{"ok": true})
		require.NoError(t, err)
		return &commonpb.Transaction{Id: id, Metadata: md}
	}
	client.EXPECT().ListTransactionsFunc(gomock.Any(), controlLedger, gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, filter *commonpb.QueryFilter, fn func(*commonpb.Transaction) error) error {
		require.Equal(t, schema.ActivityAccount(ruleID.String()), filter.GetAddress().GetHardcodedPrefix())
		require.NoError(t, fn(makeTx(10, "rule.updated", t0.Add(-time.Minute))))
		require.NoError(t, fn(makeTx(11, "evaluation.completed", t0)))
		require.NoError(t, fn(makeTx(12, "alert.opened", t0)))
		return nil
	})
	version := models.ContractVersionV2
	q := store.NewGetRuleActivitiesQuery(store.NewPaginatedQueryOptions(store.RuleActivitiesFilters{ContractVersion: &version}))
	cur, err := New(client, controlLedger).ListRuleActivities(context.Background(), ruleID, q)
	require.NoError(t, err)
	require.Equal(t, []string{"alert.opened", "evaluation.completed", "rule.updated"}, []string{cur.Data[0].Kind, cur.Data[1].Kind, cur.Data[2].Kind})
	require.Equal(t, "12:0", cur.Data[0].ID)
}

func TestListRuleActivitiesHidesOtherContract(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)
	ruleID := uuid.New()
	client.EXPECT().ListTransactionsFunc(gomock.Any(), controlLedger, gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ string, _ *commonpb.QueryFilter, fn func(*commonpb.Transaction) error) error {
		md, err := activityMetadata("rule.created", ruleID, models.ContractVersionV2, "r", "", time.Now(), map[string]any{})
		require.NoError(t, err)
		return fn(&commonpb.Transaction{Id: 1, Metadata: md})
	})
	version := models.ContractVersionV1
	q := store.NewGetRuleActivitiesQuery(store.NewPaginatedQueryOptions(store.RuleActivitiesFilters{ContractVersion: &version}))
	_, err := New(client, controlLedger).ListRuleActivities(context.Background(), ruleID, q)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestListRuleActivitiesEmptyStreamRequiresMatchingRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		storedRule  *models.Rule
		query       models.ContractVersion
		wantMissing bool
	}{
		{name: "missing rule", query: models.ContractVersionV2, wantMissing: true},
		{name: "matching rule", storedRule: newRule(uuid.New()), query: models.ContractVersionV1},
		{name: "other contract", storedRule: newRule(uuid.New()), query: models.ContractVersionV2, wantMissing: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			client := NewMockledgerClient(ctrl)
			ruleID := uuid.New()
			if tt.storedRule != nil {
				tt.storedRule.ID = ruleID
			}

			client.EXPECT().ListTransactionsFunc(gomock.Any(), controlLedger, gomock.Any(), gomock.Any()).Return(nil)
			get := client.EXPECT().GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(ruleID.String()))
			if tt.storedRule == nil {
				get.Return(nil, notFound())
			} else {
				get.Return(account(t, tt.storedRule), nil)
			}

			q := store.NewGetRuleActivitiesQuery(store.NewPaginatedQueryOptions(store.RuleActivitiesFilters{ContractVersion: &tt.query}))
			cur, err := New(client, controlLedger).ListRuleActivities(context.Background(), ruleID, q)
			if tt.wantMissing {
				require.ErrorIs(t, err, store.ErrNotFound)
				return
			}
			require.NoError(t, err)
			require.Empty(t, cur.Data)
		})
	}
}
