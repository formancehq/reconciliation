//go:build it

package ledgerresolver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	"github.com/formancehq/reconciliation/internal/ledgerpb/servicepb"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func itAddr() string {
	if a := os.Getenv("RECON_LEDGER_ADDR"); a != "" {
		return a
	}

	return "127.0.0.1:8888"
}

// TestIntegration_StaleHolds proves the stale_holds design against a live
// ledger — the parts a fake resolver cannot check:
//
//   - a `datetime` metadata key really is comparable with an integer epoch-micros
//     bound, so the deadline predicate can be pushed into the query (the ledger
//     stores datetimes as int64 micros, and reads them back as RFC3339);
//
//   - the `$or` over `$exists` picks the issuer expiry when a hold carries one
//     and falls back to creation + maxAge when it does not;
//
//   - a released hold — zero balance, deadline metadata retained — still matches
//     the query and is dropped by the template, not by the ledger;
//
//   - Queries() (the augmented query) passes the create-time ValidateQuery guard;
//
//   - identity labels survive the ledger's metadata round-trip and land on the
//     flagged hold's evidence, without any index of their own.
//
//     go test -tags it -run TestIntegration_StaleHolds ./internal/ledgerresolver/...
func TestIntegration_StaleHolds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := ledger.NewClient(itAddr(), nil)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	const (
		asset      = "USD/2"
		expiryKey  = "hold_expires_at"
		createdKey = "hold_created_at"
	)
	ledgerName := "recon-it-holds-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, ledgerName) }()

	// Both deadline keys are declared datetime and indexed: a metadata filter
	// needs the declared type *and* the accounts index.
	require.NoError(t, client.CreateLedger(ctx, ledgerName,
		[]*commonpb.SetMetadataFieldTypeCommand{
			{TargetType: commonpb.TargetType_TARGET_TYPE_ACCOUNT, Key: expiryKey, Type: commonpb.MetadataType_METADATA_TYPE_DATETIME},
			{TargetType: commonpb.TargetType_TARGET_TYPE_ACCOUNT, Key: createdKey, Type: commonpb.MetadataType_METADATA_TYPE_DATETIME},
		},
		nil, commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))
	for _, key := range []string{expiryKey, createdKey} {
		require.NoError(t, client.CreateIndex(ctx, ledgerName,
			&servicepb.CreateIndexRequest{Id: commonpb.AccountMetadataIndexID(key)}))
	}

	now := time.Now().UTC()
	prefix := "holds:enfuce:" + uuid.NewString() + ":"
	stale := prefix + "stale"
	fresh := prefix + "fresh"
	released := prefix + "released"
	aged := prefix + "aged"

	// Four holds, each 25.00: one past its issuer expiry, one still inside it,
	// one released (balance returned to world) that keeps a long-passed expiry,
	// and one with no expiry at all that is older than the 48h fallback.
	for _, address := range []string{stale, fresh, released, aged} {
		mint(ctx, t, client, ledgerName, address, asset, 2500)
	}
	burn(ctx, t, client, ledgerName, released, asset, 2500)

	// An identity label on the stale hold. It is never filtered on, so it needs
	// no declared type and no index — the write below would fail if it did.
	require.NoError(t, client.SaveAccountMetadataValues(ctx, ledgerName, stale,
		map[string]*commonpb.MetadataValue{
			"enfuce_auth_id": {Type: &commonpb.MetadataValue_StringValue{StringValue: "AUTH-8801"}},
		}))

	setDatetime(ctx, t, client, ledgerName, stale, map[string]time.Time{
		createdKey: now.Add(-4 * time.Hour),
		expiryKey:  now.Add(-3 * time.Hour),
	})
	setDatetime(ctx, t, client, ledgerName, fresh, map[string]time.Time{
		createdKey: now.Add(-1 * time.Hour),
		expiryKey:  now.Add(12 * time.Hour),
	})
	setDatetime(ctx, t, client, ledgerName, released, map[string]time.Time{
		createdKey: now.Add(-30 * 24 * time.Hour),
		expiryKey:  now.Add(-29 * 24 * time.Hour),
	})
	// No expiry: dated only by creation, 50h ago — past the 48h fallback.
	setDatetime(ctx, t, client, ledgerName, aged, map[string]time.Time{
		createdKey: now.Add(-50 * time.Hour),
	})

	spec := mustSpec(t, templates.StaleHoldsSpec{
		Source: templates.V2NamedSource{
			ID:     "enfuce-holds",
			Ledger: ledgerName,
			Query:  json.RawMessage(fmt.Sprintf(`{"$match":{"address":%q}}`, prefix+"*")),
			Asset:  asset,
		},
		Deadline: templates.HoldDeadlineSpec{
			ExpiryKey:  expiryKey,
			CreatedKey: createdKey,
			Encoding:   templates.EncodingDatetime,
			MaxAge:     "48h",
		},
		IdentityKeys: []string{"enfuce_auth_id", "never_written"},
	})

	evaluator := templates.NewStaleHolds()
	require.NoError(t, evaluator.Validate(spec))

	resolver := New(ledger.NewReader(client))
	eng, err := engine.New(engine.Resolvers{Ledger: resolver}, engine.DefaultLimits)
	require.NoError(t, err)

	// The augmented query — the rule's own query plus the deadline clause — must
	// pass the create-time guard, or an unindexed deadline key would only
	// surface as an evaluation error.
	sources, err := evaluator.Queries(spec)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, resolver.ValidateQuery(ctx, ledgerName, sources[0].Query))
	}, 15*time.Second, 100*time.Millisecond)

	// The read index is eventually consistent with the writes above.
	var outcomes []templates.Outcome
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		got, evalErr := evaluator.Evaluate(ctx, spec, eng, engine.Resolvers{Ledger: resolver}, engine.EvalInput{PIT: time.Now().UTC()})
		if !assert.NoError(c, evalErr) {
			return
		}
		if !assert.Len(c, got, 2, "expected the expired hold and the aged one") {
			return
		}
		outcomes = got
	}, 20*time.Second, 250*time.Millisecond)

	byFingerprint := map[string]templates.Outcome{}
	for _, outcome := range outcomes {
		byFingerprint[outcome.Fingerprint] = outcome
	}

	staleOutcome, ok := byFingerprint["asset:"+asset+"|hold:"+stale]
	require.True(t, ok, "the expired hold must be flagged: %+v", byFingerprint)
	require.False(t, staleOutcome.Passed)
	require.Equal(t, "expiry", staleOutcome.Evidence["basis"], "an issuer expiry wins over the fallback")
	require.Equal(t, "2500", staleOutcome.Evidence["amount"])

	identity, ok := staleOutcome.Evidence["identity"].(map[string]string)
	require.True(t, ok, "the alert must name the authorisation: %+v", staleOutcome.Evidence)
	require.Equal(t, "AUTH-8801", identity["enfuce_auth_id"])
	require.NotContains(t, identity, "never_written", "a label the hold does not carry is omitted")

	agedIdentity := byFingerprint["asset:"+asset+"|hold:"+aged].Evidence["identity"]
	require.Nil(t, agedIdentity, "a hold carrying none of the declared labels reports no identity")

	agedOutcome, ok := byFingerprint["asset:"+asset+"|hold:"+aged]
	require.True(t, ok, "the hold with no expiry must fall back to created + maxAge")
	require.Equal(t, "created_at", agedOutcome.Evidence["basis"])

	require.NotContains(t, byFingerprint, "asset:"+asset+"|hold:"+fresh, "a hold inside its expiry must pass")
	require.NotContains(t, byFingerprint, "asset:"+asset+"|hold:"+released,
		"a released hold keeps its expired metadata and must be dropped on its zero balance")

	// The warning band selects the hold that is still inside its expiry, and
	// excludes the one that already went stale — the escalation handover.
	bandSpec := mustSpec(t, templates.StaleHoldsSpec{
		Source: templates.V2NamedSource{
			ID:     "enfuce-holds",
			Ledger: ledgerName,
			Query:  json.RawMessage(fmt.Sprintf(`{"$match":{"address":%q}}`, prefix+"*")),
			Asset:  asset,
		},
		Deadline:   templates.HoldDeadlineSpec{ExpiryKey: expiryKey, Encoding: templates.EncodingDatetime},
		Mode:       templates.StaleHoldsApproaching,
		WarnWithin: "24h",
	})
	require.NoError(t, evaluator.Validate(bandSpec))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		got, evalErr := evaluator.Evaluate(ctx, bandSpec, eng, engine.Resolvers{Ledger: resolver}, engine.EvalInput{PIT: time.Now().UTC()})
		if !assert.NoError(c, evalErr) || !assert.Len(c, got, 1) {
			return
		}
		assert.Equal(c, "asset:"+asset+"|hold:"+fresh, got[0].Fingerprint)
		assert.False(c, got[0].Passed)
	}, 20*time.Second, 250*time.Millisecond)
}

func mustSpec(t *testing.T, spec templates.StaleHoldsSpec) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(spec)
	require.NoError(t, err)

	return raw
}

func mint(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, account, asset string, amount uint64) {
	t.Helper()
	posting(ctx, t, c, ledgerName, "world", account, asset, amount)
}

// burn returns a hold's funds to world, leaving the account row (and its
// deadline metadata) behind with a zero balance — a released hold.
func burn(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, account, asset string, amount uint64) {
	t.Helper()
	posting(ctx, t, c, ledgerName, account, "world", asset, amount)
}

func posting(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, source, destination, asset string, amount uint64) {
	t.Helper()

	_, err := c.Apply(ctx, &servicepb.Request{
		Type: &servicepb.Request_Apply{
			Apply: &servicepb.LedgerApplyRequest{
				Ledger: ledgerName,
				Action: &servicepb.LedgerAction{
					Data: &servicepb.LedgerAction_CreateTransaction{
						CreateTransaction: &servicepb.CreateTransactionPayload{
							Postings: []*commonpb.Posting{{
								Source:      source,
								Destination: destination,
								Amount:      commonpb.NewUint256FromUint64(amount),
								Asset:       asset,
							}},
						},
					},
				},
			},
		},
	})
	require.NoError(t, err, "post %d %s: %s → %s", amount, asset, source, destination)
}

func setDatetime(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, account string, values map[string]time.Time) {
	t.Helper()

	metadata := make(map[string]*commonpb.MetadataValue, len(values))
	for key, at := range values {
		metadata[key] = &commonpb.MetadataValue{
			Type: &commonpb.MetadataValue_DatetimeValue{DatetimeValue: at.UnixMicro()},
		}
	}
	require.NoError(t, c.SaveAccountMetadataValues(ctx, ledgerName, account, metadata))
}
