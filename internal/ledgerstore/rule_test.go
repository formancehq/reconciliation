package ledgerstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

const controlLedger = "reconciliation"

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
		GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String()), uint64(0)).
		Return(&commonpb.Account{Address: schema.RuleAccount(id.String()), Metadata: ruleToMetadata(want)}, nil)

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
		GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String()), uint64(0)).
		Return(&commonpb.Account{Address: schema.RuleAccount(id.String())}, nil)

	_, err := New(m, controlLedger).GetRule(context.Background(), id)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestLedgerStore_GetRule_NotFoundOnGRPCStatus(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	m := NewMockledgerClient(ctrl)

	id := uuid.New()
	m.EXPECT().
		GetAccount(gomock.Any(), controlLedger, schema.RuleAccount(id.String()), uint64(0)).
		Return(nil, status.Error(codes.NotFound, "account not found"))

	_, err := New(m, controlLedger).GetRule(context.Background(), id)
	require.ErrorIs(t, err, storage.ErrNotFound)
}
