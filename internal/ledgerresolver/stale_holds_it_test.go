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
//   - the `$or` over `$exists` picks the recorded expiry when a hold carries
//     one and falls back to creation + maxAge when it does not;
//
//   - a released hold — zero balance, deadline metadata retained — still matches
//     the query and is dropped by the template, not by the ledger. The ledger
//     declares no account type, so the holds are NORMAL and never purged; the
//     EPHEMERAL case is TestIntegration_StaleHolds_EphemeralPurge;
//
//   - Queries() (the augmented query) passes the create-time ValidateQuery guard;
//
//   - the aggregate counts what the live query returned, and its effectiveQuery
//     is the query the ledger actually answered.
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
	prefix := "holds:" + uuid.NewString() + ":"
	stale := prefix + "stale"
	fresh := prefix + "fresh"
	released := prefix + "released"
	aged := prefix + "aged"

	// Four holds, each 25.00: one past its recorded expiry, one still inside it,
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
			"hold_reference": {Type: &commonpb.MetadataValue_StringValue{StringValue: "H-8801"}},
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
			ID:     "holds",
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
		// One aggregate per asset, whatever the stale count.
		if !assert.Len(c, got, 1) {
			return
		}
		if !assert.Equal(c, 2, got[0].Evidence["holdsFlagged"], "expected the expired hold and the aged one") {
			return
		}
		outcomes = got
	}, 20*time.Second, 250*time.Millisecond)

	evidence := outcomes[0].Evidence
	require.False(t, outcomes[0].Passed)
	require.Equal(t, "asset:"+asset, outcomes[0].Fingerprint)

	// `stale` (2500, past its recorded expiry) and `aged` (2500, no expiry, past
	// created + 48h) are both in. `fresh` is inside its expiry, and `released`
	// keeps its expired metadata but was dropped on its zero balance — which the
	// ledger cannot do, since balances are not filterable.
	require.Equal(t, "5000", evidence["amountFlagged"])
	require.Equal(t, 1, evidence["holdsReleased"], "the released hold matched the query and was post-filtered")
	require.Equal(t, 3, evidence["holdsMatched"], "stale + released + aged past the cutoff; `fresh` is still inside its expiry, so the ledger excludes it")

	// The set is recoverable from evidence: this is the query the ledger answered.
	query, _ := evidence["effectiveQuery"].(string)
	require.Contains(t, query, prefix+"*")
	require.Contains(t, query, expiryKey)
	require.Equal(t, ledgerName, evidence["ledger"])

	// The warning band selects the hold that is still inside its expiry, and
	// excludes the one that already went stale — the escalation handover.
	bandSpec := mustSpec(t, templates.StaleHoldsSpec{
		Source: templates.V2NamedSource{
			ID:     "holds",
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
		// Only `fresh` is inside the 24h band: `stale` has already breached and
		// belongs to the stale rule.
		assert.Equal(c, 1, got[0].Evidence["holdsFlagged"])
		assert.False(c, got[0].Passed)
	}, 20*time.Second, 250*time.Millisecond)
}

// TestIntegration_StaleHolds_EphemeralPurge pins how stale_holds behaves on a
// ledger that purges an EPHEMERAL account once its last volume reaches zero
// (ledger EN-2036, release/v3.0 38c6eef55), next to NORMAL holds that are never
// purged. The test reads the ledger's behaviour instead of assuming it, so it
// holds on builds with and without the purge:
//
//   - with the purge, a released EPHEMERAL hold has no row and no metadata left,
//     so the deadline query no longer returns it and holdsReleased counts only
//     the NORMAL one. The post-filter stays for NORMAL books and older ledgers;
//
//   - re-funding a purged address starts a fresh account without the old
//     deadline metadata, so that hold leaves the control until its metadata is
//     written again. Without the purge, the metadata survives and it is flagged.
//
//     go test -tags it -run TestIntegration_StaleHolds_EphemeralPurge ./internal/ledgerresolver/...
func TestIntegration_StaleHolds_EphemeralPurge(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := ledger.NewClient(itAddr(), nil)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	const (
		asset     = "USD/2"
		expiryKey = "hold_expires_at"
	)
	ledgerName := "recon-it-holds-purge-" + uuid.NewString()
	defer func() { _ = client.DeleteLedger(ctx, ledgerName) }()

	segment := func(pattern string) *commonpb.SegmentType {
		return &commonpb.SegmentType{Constraint: &commonpb.SegmentType_Regex{Regex: pattern}}
	}
	holdSegments := map[string]*commonpb.SegmentType{"run": segment(`^[0-9a-f-]+$`), "hold": segment(`^[a-z]+$`)}
	require.NoError(t, client.CreateLedger(ctx, ledgerName,
		[]*commonpb.SetMetadataFieldTypeCommand{
			{TargetType: commonpb.TargetType_TARGET_TYPE_ACCOUNT, Key: expiryKey, Type: commonpb.MetadataType_METADATA_TYPE_DATETIME},
		},
		map[string]*commonpb.AccountType{
			"hold-ephemeral": {
				Name: "hold-ephemeral", Pattern: "holds:{run}:eph:{hold}",
				Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL, SegmentTypes: holdSegments,
			},
			"hold-normal": {
				Name: "hold-normal", Pattern: "holds:{run}:nrm:{hold}",
				Persistence: commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL, SegmentTypes: holdSegments,
			},
		},
		commonpb.ChartEnforcementMode_CHART_ENFORCEMENT_AUDIT))
	require.NoError(t, client.CreateIndex(ctx, ledgerName,
		&servicepb.CreateIndexRequest{Id: commonpb.AccountMetadataIndexID(expiryKey)}))

	now := time.Now().UTC()
	prefix := "holds:" + uuid.NewString() + ":"
	openEphemeral := prefix + "eph:stale"
	releasedEphemeral := prefix + "eph:released"
	refundedEphemeral := prefix + "eph:refunded"
	releasedNormal := prefix + "nrm:released"

	// Every hold is past its expiry. Deadline metadata is written while the hold
	// is funded: on a purging ledger, a zero-volume EPHEMERAL account has no
	// current state for a metadata write to land on.
	for _, address := range []string{openEphemeral, releasedEphemeral, refundedEphemeral, releasedNormal} {
		mint(ctx, t, client, ledgerName, address, asset, 2500)
		setDatetime(ctx, t, client, ledgerName, address, map[string]time.Time{expiryKey: now.Add(-3 * time.Hour)})
	}
	for _, address := range []string{releasedEphemeral, refundedEphemeral, releasedNormal} {
		burn(ctx, t, client, ledgerName, address, asset, 2500)
	}
	mint(ctx, t, client, ledgerName, refundedEphemeral, asset, 2500)

	// The ledger tells us whether it purges: a released EPHEMERAL hold either has
	// no current state left, or still carries its deadline metadata at zero.
	purges := accountPurged(ctx, t, client, ledgerName, releasedEphemeral)
	t.Logf("ledger purges released EPHEMERAL accounts: %v", purges)
	require.False(t, accountPurged(ctx, t, client, ledgerName, releasedNormal), "a NORMAL account is never purged")

	refunded, err := client.GetAccount(ctx, ledgerName, refundedEphemeral)
	require.NoError(t, err)
	_, keptExpiry := refunded.GetMetadata()[expiryKey]
	require.Equal(t, !purges, keptExpiry, "re-funding a purged address must not restore its metadata")

	spec := mustSpec(t, templates.StaleHoldsSpec{
		Source: templates.V2NamedSource{
			ID:     "holds",
			Ledger: ledgerName,
			Query:  json.RawMessage(fmt.Sprintf(`{"$match":{"address":%q}}`, prefix+"*")),
			Asset:  asset,
		},
		Deadline: templates.HoldDeadlineSpec{ExpiryKey: expiryKey, Encoding: templates.EncodingDatetime},
	})
	evaluator := templates.NewStaleHolds()
	require.NoError(t, evaluator.Validate(spec))

	resolver := New(ledger.NewReader(client))
	eng, err := engine.New(engine.Resolvers{Ledger: resolver}, engine.DefaultLimits)
	require.NoError(t, err)

	// With the purge: the open EPHEMERAL hold and the released NORMAL one match;
	// the released EPHEMERAL hold is gone and the re-funded one has no deadline.
	// Without it: all four match, both released holds are post-filtered, and the
	// re-funded one keeps its expiry and is flagged.
	wantMatched, wantReleased, wantFlagged, wantAmount := 2, 1, 1, "2500"
	if !purges {
		wantMatched, wantReleased, wantFlagged, wantAmount = 4, 2, 2, "5000"
	}

	// The read projection drops a purged account's metadata memberships
	// asynchronously, so the expected counts may take a moment to appear.
	var evidence map[string]any
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		got, evalErr := evaluator.Evaluate(ctx, spec, eng, engine.Resolvers{Ledger: resolver}, engine.EvalInput{PIT: time.Now().UTC()})
		if !assert.NoError(c, evalErr) || !assert.Len(c, got, 1) {
			return
		}
		ev := got[0].Evidence
		if !assert.Equal(c, wantMatched, ev["holdsMatched"]) || !assert.Equal(c, wantReleased, ev["holdsReleased"]) {
			return
		}
		assert.False(c, got[0].Passed)
		evidence = ev
	}, 20*time.Second, 250*time.Millisecond)

	require.Equal(t, wantFlagged, evidence["holdsFlagged"])
	require.Equal(t, wantAmount, evidence["amountFlagged"])
	require.Equal(t, evidence["holdsMatched"], evidence["holdsReleased"].(int)+evidence["holdsRejected"].(int)+evidence["holdsFlagged"].(int),
		"holdsMatched = holdsReleased + holdsRejected + holdsFlagged")
}

// accountPurged reports whether an account has no current state left: no
// volume and no metadata. GetAccount never answers NotFound for an address —
// an unknown or purged one comes back empty — so emptiness is the signal. Only
// call it on an account the test gave metadata to, or a never-funded address
// would read as purged.
func accountPurged(ctx context.Context, t *testing.T, c *ledger.Client, ledgerName, address string) bool {
	t.Helper()

	acct, err := c.GetAccount(ctx, ledgerName, address)
	require.NoError(t, err, "get account %s", address)

	return len(acct.GetVolumes()) == 0 && len(acct.GetMetadata()) == 0
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
