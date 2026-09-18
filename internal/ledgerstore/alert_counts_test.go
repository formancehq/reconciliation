package ledgerstore

import (
	"context"
	"math/big"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// A page of rules costs one aggregate, grouped by (state, rule) prefix — not one
// query per rule, and not a scan of alerts (EN-2240).
func TestCountAlertsByRuleUsesOneGroupedAggregate(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)

	noisy, quiet := uuid.New(), uuid.New()

	client.EXPECT().
		AggregateVolumesGrouped(gomock.Any(), controlLedger, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, filter *commonpb.QueryFilter, prefixes []string) (map[string]map[string]*big.Int, error) {
			// The scan is scoped to markers; the prefixes do the bucketing.
			require.Equal(t, schema.StatePrefix(), filter.GetAddress().GetHardcodedPrefix())
			require.Equal(t, []string{
				schema.StateByRulePrefix(schema.StateOpen, noisy.String()),
				schema.StateByRulePrefix(schema.StateAck, noisy.String()),
				schema.StateByRulePrefix(schema.StateOpen, quiet.String()),
				schema.StateByRulePrefix(schema.StateAck, quiet.String()),
			}, prefixes)

			return map[string]map[string]*big.Int{
				schema.StateByRulePrefix(schema.StateOpen, noisy.String()): {schema.AssetAlert: big.NewInt(3)},
				schema.StateByRulePrefix(schema.StateAck, noisy.String()):  {schema.AssetAlert: big.NewInt(1)},
				// quiet holds no marker at all: its prefixes match nothing and are
				// absent from the response rather than present as zero.
			}, nil
		})

	store := New(client, controlLedger)

	counts, err := store.CountAlertsByRule(context.Background(), []uuid.UUID{noisy, quiet})

	require.NoError(t, err)
	require.Equal(t, models.AlertCounts{Open: 3, Acknowledged: 1}, counts[noisy])
	require.Equal(t, models.AlertCounts{}, counts[quiet], "a rule with no live alert counts zero, and is present")
}

// No rules, no call: an empty page must not reach the ledger.
func TestCountAlertsByRuleSkipsTheCallWhenThereAreNoRules(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := NewMockledgerClient(ctrl)

	counts, err := New(client, controlLedger).CountAlertsByRule(context.Background(), nil)

	require.NoError(t, err)
	require.Empty(t, counts)
}
