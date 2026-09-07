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

// captureTx builds a capture transaction the way RecordCapture writes it (all
// metadata as strings, captured_at as RFC3339Nano).
func captureTx(id uint64, ruleID uuid.UUID, period, evalID, verdict, trigger string, at time.Time, evidence string) *commonpb.Transaction {
	md := map[string]*commonpb.MetadataValue{
		schema.CaptureMetaType: strVal(schema.CaptureType),
		schema.CaptureMetaRule: strVal(ruleID.String()),
		schema.CaptureMetaTmpl: strVal("ledger_invariant"),
		schema.CaptureMetaPer:  strVal(period),
		schema.CaptureMetaEval: strVal(evalID),
		schema.CaptureMetaVdt:  strVal(verdict),
		schema.CaptureMetaTrig: strVal(trigger),
		schema.CaptureMetaAt:   strVal(at.UTC().Format(time.RFC3339Nano)),
	}
	if evidence != "" {
		md[schema.CaptureMetaEvi] = strVal(evidence)
	}

	return &commonpb.Transaction{Id: id, Metadata: md}
}

func TestCaptureFromTransaction_RoundTrip(t *testing.T) {
	t.Parallel()

	ruleID, evalID := uuid.New(), uuid.New()
	at := time.Now().Truncate(time.Microsecond).UTC()

	c, ok := captureFromTransaction(captureTx(7, ruleID, "2026-03", evalID.String(), "fail", "scheduled", at, `{"delta":"5"}`))

	require.True(t, ok)
	require.Equal(t, uint64(7), c.TransactionID)
	require.Equal(t, ruleID, c.RuleID)
	require.Equal(t, "2026-03", c.PeriodID)
	require.Equal(t, evalID, c.EvaluationID)
	require.Equal(t, "fail", c.Verdict)
	require.Equal(t, "scheduled", c.Trigger)
	require.True(t, at.Equal(c.CapturedAt), "captured_at round-trips")
	require.JSONEq(t, `{"delta":"5"}`, string(c.Evidence))
	require.Equal(t, models.ContractVersionV2, c.ContractVersion, "a capture predating the stamp resolves to the live contract, not retired V1")

	v2tx := captureTx(8, ruleID, "2026-03", evalID.String(), "fail", "scheduled", at, `{}`)
	v2tx.Metadata[schema.CaptureMetaContractVersion] = strVal("2")
	v2, ok := captureFromTransaction(v2tx)
	require.True(t, ok)
	require.Equal(t, models.ContractVersionV2, v2.ContractVersion)
}

func TestCaptureFromTransaction_NotACapture(t *testing.T) {
	t.Parallel()

	_, ok := captureFromTransaction(&commonpb.Transaction{
		Id:       1,
		Metadata: map[string]*commonpb.MetadataValue{"foo": strVal("bar")},
	})
	require.False(t, ok, "a non-capture transaction is skipped")
}

func TestLedgerStore_ListCaptures_AllPeriods_SortedNewestFirst(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	s := New(NewMockledgerClient(ctrl), controlLedger)
	m := s.client.(*MockledgerClient)

	ruleID := uuid.New()
	t0 := time.Now().Truncate(time.Microsecond).UTC()

	m.EXPECT().
		ListTransactionsFunc(gomock.Any(), controlLedger, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, filter *commonpb.QueryFilter, fn func(*commonpb.Transaction) error) error {
			// No period → the filter scopes to the rule's capture prefix (all periods).
			require.Equal(t, schema.CaptureRulePrefix(ruleID.String()), filter.GetAddress().GetHardcodedPrefix())

			// Emitted out of order; ListCaptures must return newest-first.
			require.NoError(t, fn(captureTx(1, ruleID, "2026-01", uuid.NewString(), "pass", "scheduled", t0.Add(-2*time.Hour), "")))
			require.NoError(t, fn(captureTx(3, ruleID, "2026-03", uuid.NewString(), "fail", "manual", t0, `{"x":"1"}`)))
			require.NoError(t, fn(captureTx(2, ruleID, "2026-02", uuid.NewString(), "pass", "scheduled", t0.Add(-1*time.Hour), "")))

			return nil
		})

	cur, err := s.ListCaptures(context.Background(), ruleID, store.NewGetCapturesQuery(store.NewPaginatedQueryOptions(store.CapturesFilters{})))
	require.NoError(t, err)
	require.Len(t, cur.Data, 3)
	require.Equal(t, uint64(3), cur.Data[0].TransactionID)
	require.Equal(t, uint64(2), cur.Data[1].TransactionID)
	require.Equal(t, uint64(1), cur.Data[2].TransactionID)
}

func TestLedgerStore_ListCaptures_PeriodScoped(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	s := New(NewMockledgerClient(ctrl), controlLedger)
	m := s.client.(*MockledgerClient)

	ruleID := uuid.New()

	m.EXPECT().
		ListTransactionsFunc(gomock.Any(), controlLedger, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, filter *commonpb.QueryFilter, fn func(*commonpb.Transaction) error) error {
			// A set period → the filter scopes to that exact (rule, period) bucket.
			require.Equal(t, schema.CaptureAccount(ruleID.String(), "2026-03"), filter.GetAddress().GetHardcodedPrefix())
			require.NoError(t, fn(captureTx(9, ruleID, "2026-03", uuid.NewString(), "pass", "manual", time.Now().UTC(), "")))

			return nil
		})

	q := store.NewGetCapturesQuery(store.NewPaginatedQueryOptions(store.CapturesFilters{Period: "2026-03"}))
	cur, err := s.ListCaptures(context.Background(), ruleID, q)
	require.NoError(t, err)
	require.Len(t, cur.Data, 1)
	require.Equal(t, "2026-03", cur.Data[0].PeriodID)
}
