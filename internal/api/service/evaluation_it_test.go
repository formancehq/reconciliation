//go:build it

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/formancehq/reconciliation/internal/ledgerresolver"
	"github.com/formancehq/reconciliation/internal/ledgerstore"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_EvaluateLive proves the live read model end-to-end (ADR-003):
// an evaluation reads two data ledgers live through the ledgerresolver adapter (no
// checkpoint), and the alert layer opens a case on a genuine break — all against a
// live Ledger v3.
//
//	go test -tags it -run TestIntegration_EvaluateLive ./internal/api/service/...
func TestIntegration_EvaluateLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := ledger.NewClient(evalItAddr(), nil)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	const control = "recon-it4"
	require.NoError(t, ledger.NewProvisioner(client, control, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT).Provision(ctx), "provision control-ledger")

	// Fresh data ledgers per run so the test is isolated on the shared dev ledger.
	suffix := uuid.NewString()
	ledgerA := "recon-it-eval-a-" + suffix
	ledgerB := "recon-it-eval-b-" + suffix
	for _, l := range []string{ledgerA, ledgerB} {
		require.NoError(t, client.CreateLedger(ctx, l, nil, nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT), "create %s", l)
	}

	// Wire the real V1 stack: control-ledger store + live Tier-1 reader + a stub
	// Tier-2 (unused for ledger↔ledger).
	reader := ledger.NewReader(client)
	res := engine.Resolvers{Ledger: ledgerresolver.New(reader), Payments: nopPayments{}}
	eng, err := engine.New(res, engine.DefaultLimits)
	require.NoError(t, err)
	store := ledgerstore.New(client, control)
	svc := NewService(store, nil, eng, templates.DefaultRegistry(), res)

	const asset = "USD/2"
	prefix := "acc:" + suffix + ":"
	q := json.RawMessage(fmt.Sprintf(`{"$match":{"address":%q}}`, prefix+"*"))
	specJSON, err := json.Marshal(templates.ParitySpec{
		Left:      templates.SourceSpec{Kind: templates.SourceLedger, Ledger: ledgerA, Query: q},
		Right:     templates.SourceSpec{Kind: templates.SourceLedger, Ledger: ledgerB, Query: q},
		Tolerance: map[string]int64{asset: 0},
	})
	require.NoError(t, err)
	rule, err := svc.CreateRule(ctx, &CreateRuleRequest{
		Name:         "it-parity",
		TemplateKind: models.TemplateSourceParity,
		TemplateSpec: specJSON,
		Severity:     models.SeverityHigh,
	})
	require.NoError(t, err)

	acct := prefix + "x"

	// Balanced: A == B == 100 → parity holds → PASS, no alert.
	writeBalance(ctx, t, client, ledgerA, acct, asset, 100)
	writeBalance(ctx, t, client, ledgerB, acct, asset, 100)
	requireEventualAgg(ctx, t, reader, ledgerA, q, asset, "100")
	requireEventualAgg(ctx, t, reader, ledgerB, q, asset, "100")

	ev, err := svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now().UTC()})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationPass, ev.Result, "balanced ledgers reconcile")
	fps, err := store.ListActiveAlertFingerprints(ctx, rule.ID, models.ContinuousPeriod)
	require.NoError(t, err)
	require.Empty(t, fps, "a passing evaluation opens no alert")

	// Break it: A gains +50 (A=150, B=100) → diff 50 > tolerance 0 → FAIL + alert.
	writeBalance(ctx, t, client, ledgerA, acct, asset, 50)
	requireEventualAgg(ctx, t, reader, ledgerA, q, asset, "150")

	ev, err = svc.EvaluateRule(ctx, rule.ID, EvaluateRuleRequest{PIT: time.Now().UTC()})
	require.NoError(t, err)
	require.Equal(t, models.EvaluationFail, ev.Result, "imbalanced ledgers break parity")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		fps, ferr := store.ListActiveAlertFingerprints(ctx, rule.ID, models.ContinuousPeriod)
		if !assert.NoError(c, ferr) {
			return
		}
		assert.NotEmpty(c, fps, "a failing evaluation opens an alert")
	}, 5*time.Second, 25*time.Millisecond)
}

// nopPayments is a stand-in Tier-2 resolver: engine.New requires one, but a
// ledger↔ledger parity rule never calls it.
type nopPayments struct{}

func (nopPayments) PoolBalanceLatest(context.Context, string) (map[string]*big.Int, error) {
	return map[string]*big.Int{}, nil
}

func evalItAddr() string {
	if a := os.Getenv("RECON_LEDGER_ADDR"); a != "" {
		return a
	}

	return "127.0.0.1:8888"
}

// writeBalance mints amount of asset to account in ledger (world → account).
func writeBalance(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, account, asset string, amount uint64) {
	t.Helper()

	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_Apply{
			Apply: &servicepb.LedgerApplyRequest{
				Ledger: ledgerName,
				Action: &servicepb.LedgerAction{
					Data: &servicepb.LedgerAction_CreateTransaction{
						CreateTransaction: &servicepb.CreateTransactionPayload{
							Postings: []*commonpb.Posting{{
								Source:      "world",
								Destination: account,
								Amount:      commonpb.NewUint256FromUint64(amount),
								Asset:       asset,
							}},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err, "write %d %s to %s@%s", amount, asset, account, ledgerName)
}

// requireEventualAgg polls the live aggregate until asset equals want (the read
// index is eventually consistent with writes), so the evaluation that follows is
// guaranteed to observe the writes.
func requireEventualAgg(ctx context.Context, t *testing.T, r *ledger.Reader, ledgerName string, q json.RawMessage, asset, want string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		bal, err := r.AggregateBalance(ctx, ledgerName, q)
		if !assert.NoError(c, err) {
			return
		}
		got := "0"
		if bal[asset] != nil {
			got = bal[asset].String()
		}
		assert.Equal(c, want, got, "%s balance on %s", asset, ledgerName)
	}, 5*time.Second, 25*time.Millisecond)
}
