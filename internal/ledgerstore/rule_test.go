package ledgerstore

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const controlLedger = "reconciliation"

func ptr[T any](v T) *T { return &v }

func newRule(id uuid.UUID) *models.Rule {
	now := time.Now().Truncate(time.Microsecond).UTC()

	return &models.Rule{
		ID:           id,
		Name:         "r",
		TemplateKind: models.TemplateLedgerInvariant,
		TemplateSpec: json.RawMessage(`{}`),
		Enabled:      true,
		Severity:     models.SeverityHigh,
		Cadence:      models.CadenceContinuous,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// account builds the control-ledger account a stored rule would read back as.
func account(t *testing.T, r *models.Rule) *commonpb.Account {
	t.Helper()

	md, err := ruleToMetadata(r)
	require.NoError(t, err)

	return &commonpb.Account{Address: schema.RuleAccount(r.ID.String()), Metadata: md}
}

func TestLedgerStore_CreateRule(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)
	id := uuid.New()

	m.EXPECT().
		SaveAccountMetadataValues(gomock.Any(), controlLedger, schema.RuleAccount(id.String()), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, md map[string]*commonpb.MetadataValue) error {
			require.Equal(t, "r", md[schema.MetaName].GetStringValue())
			require.True(t, md[schema.MetaEnabled].GetBoolValue())

			return nil
		})

	require.NoError(t, New(m, controlLedger).CreateRule(context.Background(), newRule(id)))
}

func TestLedgerStore_GetRule(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)
	id := uuid.New()
	want := newRule(id)

	m.EXPECT().
		GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String())).
		Return(account(t, want), nil)

	got, err := New(m, controlLedger).GetRule(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, want.ID, got.ID)
	require.Equal(t, want.Name, got.Name)
	require.Equal(t, want.Enabled, got.Enabled)
}

func TestLedgerStore_GetRule_NotFoundOnEmptyMetadata(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)
	id := uuid.New()

	m.EXPECT().
		GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String())).
		Return(&commonpb.Account{Address: schema.RuleAccount(id.String())}, nil)

	_, err := New(m, controlLedger).GetRule(context.Background(), id)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestLedgerStore_GetRule_NotFoundOnGRPCStatus(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)
	id := uuid.New()

	m.EXPECT().
		GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String())).
		Return(nil, status.Error(codes.NotFound, "account not found"))

	_, err := New(m, controlLedger).GetRule(context.Background(), id)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestLedgerStore_PatchRule(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)
	id := uuid.New()

	m.EXPECT().
		GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String())).
		Return(account(t, newRule(id)), nil)
	m.EXPECT().
		SaveAccountMetadataValues(gomock.Any(), controlLedger, schema.RuleAccount(id.String()), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, md map[string]*commonpb.MetadataValue) error {
			require.Equal(t, "renamed", md[schema.MetaName].GetStringValue())
			require.False(t, md[schema.MetaEnabled].GetBoolValue()) // patched to false
			return nil
		})

	err := New(m, controlLedger).PatchRule(context.Background(), id, store.RulePatch{
		Name:    ptr("renamed"),
		Enabled: ptr(false),
	})
	require.NoError(t, err)
}

func TestLedgerStore_PatchRule_PrunesRemovedLabels(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)
	id := uuid.New()

	base := newRule(id)
	base.Labels = map[string]string{"env": "prod", "team": "treasury"}

	m.EXPECT().GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String())).Return(account(t, base), nil)
	m.EXPECT().SaveAccountMetadataValues(gomock.Any(), controlLedger, schema.RuleAccount(id.String()), gomock.Any()).Return(nil)
	// "team" was removed -> its label key must be deleted.
	m.EXPECT().
		DeleteAccountMetadata(gomock.Any(), controlLedger, schema.RuleAccount(id.String()), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, keys ...string) error {
			require.Equal(t, []string{schema.LabelPrefix + "team"}, keys)
			return nil
		})

	err := New(m, controlLedger).PatchRule(context.Background(), id, store.RulePatch{
		Labels: ptr(map[string]string{"env": "prod"}),
	})
	require.NoError(t, err)
}

func TestLedgerStore_DeleteRule(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)
	id := uuid.New()

	m.EXPECT().GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String())).Return(account(t, newRule(id)), nil)
	m.EXPECT().
		DeleteAccountMetadata(gomock.Any(), controlLedger, schema.RuleAccount(id.String()), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, keys ...string) error {
			require.NotEmpty(t, keys)
			require.True(t, slices.Contains(keys, schema.MetaName))
			return nil
		})

	require.NoError(t, New(m, controlLedger).DeleteRule(context.Background(), id))
}

func TestLedgerStore_DeleteRule_NotFound(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)
	id := uuid.New()

	m.EXPECT().
		GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String())).
		Return(&commonpb.Account{Address: schema.RuleAccount(id.String())}, nil)

	require.ErrorIs(t, New(m, controlLedger).DeleteRule(context.Background(), id), store.ErrNotFound)
}
