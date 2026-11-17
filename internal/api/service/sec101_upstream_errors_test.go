package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
	"github.com/formancehq/formance-sdk-go/v3/pkg/models/sdkerrors"
	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Embed the existing successful fixture, overriding only the failed source.
type failingSourceSDK struct {
	mockSDKFormanceClient
	ledgerError error
	poolError   error
}

func (f *failingSourceSDK) V2GetBalancesAggregated(ctx context.Context, req operations.V2GetBalancesAggregatedRequest) (*operations.V2GetBalancesAggregatedResponse, error) {
	if f.ledgerError != nil {
		return nil, f.ledgerError
	}
	return f.mockSDKFormanceClient.V2GetBalancesAggregated(ctx, req)
}

func (f *failingSourceSDK) V3GetPoolBalances(ctx context.Context, req operations.V3GetPoolBalancesRequest) (*operations.V3GetPoolBalancesResponse, error) {
	if f.poolError != nil {
		return nil, f.poolError
	}
	return f.mockSDKFormanceClient.V3GetPoolBalances(ctx, req)
}

func (f *failingSourceSDK) V3GetPoolBalancesLatest(ctx context.Context, req operations.V3GetPoolBalancesLatestRequest) (*operations.V3GetPoolBalancesLatestResponse, error) {
	if f.poolError != nil {
		return nil, f.poolError
	}
	return f.mockSDKFormanceClient.V3GetPoolBalancesLatest(ctx, req)
}

func TestSEC101_UpstreamDetailsAbsentFromPolicyAndEvaluationErrors(t *testing.T) {
	for _, source := range []string{"ledger", "pool"} {
		t.Run(source, func(t *testing.T) {
			const marker = "PRIVATE-UPSTREAM-BODY-SECRET"
			// Error codes are also response data, not a trusted safe classification.
			cause := &sdkerrors.V3ErrorResponse{ErrorCode: marker, ErrorMessage: marker}
			client := &failingSourceSDK{}
			if source == "ledger" {
				client.ledgerError = cause
			} else {
				client.poolError = cause
			}
			res := engine.Resolvers{Ledger: engine.NewSDKLedgerResolver(client), Payments: engine.NewSDKPaymentsResolver(client)}
			eng, err := engine.New(res, engine.DefaultLimits)
			require.NoError(t, err)
			store := newFakeV1Store()
			svc := NewService(store, client, eng, templates.DefaultRegistry(), res)

			// Legacy policy reconciliation returns a safe error to its API caller.
			legacy := NewService(newMockStore(), client, nil, nil, engine.Resolvers{})
			_, err = legacy.Reconciliation(t.Context(), uuid.NewString(), &ReconciliationRequest{
				ReconciledAtLedger: time.Now().Add(-time.Hour), ReconciledAtPayments: time.Now().Add(-time.Hour),
			})
			require.ErrorContains(t, err, "upstream request failed")
			require.NotContains(t, err.Error(), marker)

			// The same source failure must stay safe in evaluation and alert storage.
			spec, err := json.Marshal(templates.ParitySpec{
				Left:  templates.SourceSpec{Kind: templates.SourcePaymentsPool, PoolID: orchestrationTestPoolID},
				Right: templates.SourceSpec{Kind: templates.SourceLedger, Ledger: "sec101-ledger", Query: json.RawMessage(`{}`)},
			})
			require.NoError(t, err)
			rule, err := svc.CreateRule(t.Context(), &CreateRuleRequest{Name: "error privacy", TemplateKind: models.TemplateSourceParity, TemplateSpec: spec})
			require.NoError(t, err)
			ev, err := svc.EvaluateRule(t.Context(), rule.ID, EvaluateRuleRequest{PIT: time.Now()})
			require.NoError(t, err)
			require.Equal(t, models.EvaluationError, ev.Result)
			stored, err := store.GetEvaluation(t.Context(), ev.ID)
			require.NoError(t, err)
			alert := store.activeFor(rule.ID, "engine.error")
			require.NotNil(t, alert)
			for _, text := range []string{stored.Error, string(stored.Evidence), string(alert.Evidence)} {
				require.Contains(t, text, "upstream request failed")
				require.NotContains(t, text, marker)
				require.NotContains(t, text, orchestrationTestPoolID)
				require.NotContains(t, text, "sec101-ledger")
			}
		})
	}
}
