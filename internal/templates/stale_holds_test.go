package templates

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/formancehq/reconciliation/internal/engine"
)

// evalNow is the fixed evaluation clock every test in this file runs against —
// stale_holds is the one template whose result depends on it.
var evalNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// holdLedger is a ledger fake that ignores the query and returns a fixed set of
// hold accounts. The ledger's own filtering is exercised separately (the query
// shape is pinned by TestStaleHolds_QueryPushdown); what these tests check is
// that the template's own arithmetic decides the outcome.
type holdLedger struct {
	accounts  []engine.Account
	lastQuery json.RawMessage
	err       error
}

func (l *holdLedger) AggregateBalance(context.Context, string, json.RawMessage) (map[string]*big.Int, error) {
	return map[string]*big.Int{}, nil
}

func (l *holdLedger) ListAccounts(_ context.Context, _ string, query json.RawMessage, limit int) ([]engine.Account, error) {
	l.lastQuery = query
	if l.err != nil {
		return nil, l.err
	}
	if len(l.accounts) > limit {
		return nil, errors.New("listAccounts: exceeded accounts budget")
	}
	return l.accounts, nil
}

func hold(address string, amount int64, metadata map[string]string) engine.Account {
	return engine.Account{
		Address:  address,
		Ledger:   "cards",
		Metadata: metadata,
		Balances: map[string]*big.Int{"USD/2": big.NewInt(amount)},
	}
}

func expiring(at time.Time) map[string]string {
	return map[string]string{"hold_expires_at": at.Format(time.RFC3339)}
}

func holdSpec(t *testing.T, mutate func(*StaleHoldsSpec)) json.RawMessage {
	t.Helper()
	spec := StaleHoldsSpec{
		Source: V2NamedSource{
			ID:     "holds",
			Ledger: "cards",
			Query:  json.RawMessage(`{"$match":{"address":"holds:enfuce:*"}}`),
			Asset:  "USD/2",
		},
		Deadline: HoldDeadlineSpec{ExpiryKey: "hold_expires_at", Encoding: EncodingDatetime},
	}
	if mutate != nil {
		mutate(&spec)
	}
	return mustJSON(t, spec)
}

func evaluateHolds(t *testing.T, spec json.RawMessage, accounts []engine.Account) ([]Outcome, *holdLedger) {
	t.Helper()
	ledger := &holdLedger{accounts: accounts}
	eng, resolvers := newTestEngine(t, ledger)
	outcomes, err := NewStaleHolds().Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{PIT: evalNow})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return outcomes, ledger
}

// A hold whose issuer expiry has passed is flagged, one alert per hold.
func TestStaleHolds_FlagsExpiredHold(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:enfuce:h1", 25_00, expiring(evalNow.Add(-3*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected one outcome, got %d: %+v", len(outcomes), outcomes)
	}
	outcome := outcomes[0]
	if outcome.Passed {
		t.Error("expected the expired hold to fail")
	}
	if want := "asset:USD/2|hold:holds:enfuce:h1"; outcome.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", outcome.Fingerprint, want)
	}
	if got := outcome.Evidence["basis"]; got != "expiry" {
		t.Errorf("basis = %v, want expiry", got)
	}
	if got := outcome.Evidence["amount"]; got != "2500" {
		t.Errorf("amount = %v, want 2500", got)
	}
	if got := outcome.Evidence["overdueSeconds"]; got != int64(3*3600) {
		t.Errorf("overdueSeconds = %v, want %d", got, 3*3600)
	}
	if cel, _ := outcome.Evidence["compiledCEL"].(string); !strings.Contains(cel, "holds:enfuce:h1") {
		t.Errorf("compiledCEL should narrow to the single hold, got %q", cel)
	}
}

// A hold still inside its expiry is not flagged, and the clean run still
// records what was checked.
func TestStaleHolds_PassesWhenNothingIsStale(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:enfuce:h1", 25_00, expiring(evalNow.Add(12*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected one summary outcome, got %d", len(outcomes))
	}
	if !outcomes[0].Passed {
		t.Error("expected a passing outcome")
	}
	if outcomes[0].Fingerprint != "asset:USD/2" {
		t.Errorf("fingerprint = %q, want asset:USD/2", outcomes[0].Fingerprint)
	}
	if got := outcomes[0].Evidence["holdsFlagged"]; got != 0 {
		t.Errorf("holdsFlagged = %v, want 0", got)
	}
	if got := outcomes[0].Evidence["evaluatedAt"]; got != evalNow.Format(time.RFC3339) {
		t.Errorf("evaluatedAt = %v, want the evaluation PIT", got)
	}
}

// A released hold keeps its account row and its long-past expiry metadata, so
// it still matches the deadline filter. Only the zero balance tells us it is
// gone — and balances are not filterable in a query.
func TestStaleHolds_IgnoresReleasedHolds(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:enfuce:released", 0, expiring(evalNow.Add(-30*24*time.Hour))),
		hold("holds:enfuce:stuck", 10_00, expiring(evalNow.Add(-1*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected only the stuck hold, got %d: %+v", len(outcomes), outcomes)
	}
	if outcomes[0].Fingerprint != "asset:USD/2|hold:holds:enfuce:stuck" {
		t.Errorf("flagged the wrong hold: %q", outcomes[0].Fingerprint)
	}
}

// Approaching mode is a band: a hold due inside the window is flagged, and one
// that already went stale has left the band (the stale rule owns it).
func TestStaleHolds_ApproachingIsABand(t *testing.T) {
	t.Parallel()

	spec := holdSpec(t, func(s *StaleHoldsSpec) {
		s.Mode = StaleHoldsApproaching
		s.WarnWithin = "6h"
	})
	outcomes, _ := evaluateHolds(t, spec, []engine.Account{
		hold("holds:enfuce:soon", 30_00, expiring(evalNow.Add(2*time.Hour))),
		hold("holds:enfuce:already", 40_00, expiring(evalNow.Add(-2*time.Hour))),
		hold("holds:enfuce:later", 50_00, expiring(evalNow.Add(24*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected only the hold inside the band, got %d: %+v", len(outcomes), outcomes)
	}
	if outcomes[0].Fingerprint != "asset:USD/2|hold:holds:enfuce:soon" {
		t.Errorf("flagged the wrong hold: %q", outcomes[0].Fingerprint)
	}
	if got := outcomes[0].Evidence["dueInSeconds"]; got != int64(2*3600) {
		t.Errorf("dueInSeconds = %v, want %d", got, 2*3600)
	}
}

// With no issuer expiry, the hold is dated from its creation metadata plus the
// fallback age.
func TestStaleHolds_FallsBackToCreatedPlusMaxAge(t *testing.T) {
	t.Parallel()

	spec := holdSpec(t, func(s *StaleHoldsSpec) {
		s.Deadline = HoldDeadlineSpec{
			ExpiryKey:  "hold_expires_at",
			CreatedKey: "hold_created_at",
			Encoding:   EncodingDatetime,
			MaxAge:     "48h",
		}
	})
	outcomes, _ := evaluateHolds(t, spec, []engine.Account{
		// No expiry: created 50h ago, so 2h past the 48h fallback.
		hold("holds:enfuce:old", 15_00, map[string]string{
			"hold_created_at": evalNow.Add(-50 * time.Hour).Format(time.RFC3339),
		}),
		// No expiry, created 10h ago: still inside the fallback.
		hold("holds:enfuce:fresh", 15_00, map[string]string{
			"hold_created_at": evalNow.Add(-10 * time.Hour).Format(time.RFC3339),
		}),
		// Issuer expiry present and still in the future: the expiry wins over
		// the fallback, even though the hold is 5 days old.
		hold("holds:enfuce:extended", 15_00, map[string]string{
			"hold_created_at": evalNow.Add(-5 * 24 * time.Hour).Format(time.RFC3339),
			"hold_expires_at": evalNow.Add(24 * time.Hour).Format(time.RFC3339),
		}),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected one flagged hold, got %d: %+v", len(outcomes), outcomes)
	}
	if outcomes[0].Fingerprint != "asset:USD/2|hold:holds:enfuce:old" {
		t.Errorf("flagged the wrong hold: %q", outcomes[0].Fingerprint)
	}
	if got := outcomes[0].Evidence["basis"]; got != "created_at" {
		t.Errorf("basis = %v, want created_at", got)
	}
}

func TestStaleHolds_EpochEncodings(t *testing.T) {
	t.Parallel()

	deadline := evalNow.Add(-time.Hour)
	for _, tc := range []struct {
		encoding InstantEncoding
		value    string
	}{
		{EncodingEpochSeconds, strconvI(deadline.Unix())},
		{EncodingEpochMillis, strconvI(deadline.UnixMilli())},
		{EncodingEpochMicros, strconvI(deadline.UnixMicro())},
	} {
		t.Run(string(tc.encoding), func(t *testing.T) {
			spec := holdSpec(t, func(s *StaleHoldsSpec) { s.Deadline.Encoding = tc.encoding })
			outcomes, _ := evaluateHolds(t, spec, []engine.Account{
				hold("holds:enfuce:h1", 100, map[string]string{"hold_expires_at": tc.value}),
			})
			if len(outcomes) != 1 || outcomes[0].Passed {
				t.Fatalf("expected the hold to be flagged, got %+v", outcomes)
			}
			if got := outcomes[0].Evidence["deadline"]; got != deadline.Format(time.RFC3339) {
				t.Errorf("deadline = %v, want %v", got, deadline.Format(time.RFC3339))
			}
		})
	}
}

// A matched hold whose deadline metadata cannot be read is surfaced as an
// engine error, not silently skipped — the same stance metadataInt takes.
func TestStaleHolds_UnreadableDeadlineIsAnError(t *testing.T) {
	t.Parallel()

	ledger := &holdLedger{accounts: []engine.Account{
		hold("holds:enfuce:h1", 100, map[string]string{"hold_expires_at": "yesterday"}),
	}}
	eng, resolvers := newTestEngine(t, ledger)
	_, err := NewStaleHolds().Evaluate(context.Background(), holdSpec(t, nil), eng, resolvers, engine.EvalInput{PIT: evalNow})
	if err == nil || !strings.Contains(err.Error(), "hold_expires_at") {
		t.Fatalf("expected an error naming the key, got %v", err)
	}
}

func TestStaleHolds_RequiresEvaluationClock(t *testing.T) {
	t.Parallel()

	eng, resolvers := newTestEngine(t, &holdLedger{})
	_, err := NewStaleHolds().Evaluate(context.Background(), holdSpec(t, nil), eng, resolvers, engine.EvalInput{})
	if err == nil || !strings.Contains(err.Error(), "PIT") {
		t.Fatalf("expected a PIT error, got %v", err)
	}
}

func TestStaleHolds_AggregateScope(t *testing.T) {
	t.Parallel()

	spec := holdSpec(t, func(s *StaleHoldsSpec) { s.Scope = StaleHoldsAggregate })
	outcomes, _ := evaluateHolds(t, spec, []engine.Account{
		hold("holds:enfuce:h1", 25_00, expiring(evalNow.Add(-3*time.Hour))),
		hold("holds:enfuce:h2", 15_00, expiring(evalNow.Add(-9*time.Hour))),
		hold("holds:enfuce:ok", 99_00, expiring(evalNow.Add(9*time.Hour))),
		hold("holds:enfuce:released", 0, expiring(evalNow.Add(-99*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("aggregate scope must emit exactly one outcome, got %d", len(outcomes))
	}
	evidence := outcomes[0].Evidence
	if outcomes[0].Passed {
		t.Error("expected the aggregate outcome to fail")
	}
	if outcomes[0].Fingerprint != "asset:USD/2" {
		t.Errorf("fingerprint = %q, want asset:USD/2", outcomes[0].Fingerprint)
	}
	if got := evidence["holdsFlagged"]; got != 2 {
		t.Errorf("holdsFlagged = %v, want 2", got)
	}
	if got := evidence["holdsReleased"]; got != 1 {
		t.Errorf("holdsReleased = %v, want 1", got)
	}
	if got := evidence["amountFlagged"]; got != "4000" {
		t.Errorf("amountFlagged = %v, want 4000", got)
	}
	// Oldest first, so the sample leads with the worst offender.
	if got := evidence["oldestDeadline"]; got != evalNow.Add(-9*time.Hour).Format(time.RFC3339) {
		t.Errorf("oldestDeadline = %v", got)
	}
	sample, ok := evidence["holds"].([]map[string]any)
	if !ok || len(sample) != 2 || sample[0]["hold"] != "holds:enfuce:h2" {
		t.Errorf("unexpected sample: %+v", evidence["holds"])
	}
}

// The deadline predicate must reach the ledger as a query clause — that is what
// keeps the scan proportional to the stale holds rather than to every hold.
func TestStaleHolds_QueryPushdown(t *testing.T) {
	t.Parallel()

	cutoff := strconvI(evalNow.UnixMicro())

	t.Run("expiry only", func(t *testing.T) {
		_, ledger := evaluateHolds(t, holdSpec(t, nil), nil)
		want := `{"$and":[{"$match":{"address":"holds:enfuce:*"}},{"$lte":{"metadata[hold_expires_at]":` + cutoff + `}}]}`
		if string(ledger.lastQuery) != want {
			t.Errorf("query =\n%s\nwant\n%s", ledger.lastQuery, want)
		}
	})

	t.Run("created only shifts the cutoff by maxAge", func(t *testing.T) {
		spec := holdSpec(t, func(s *StaleHoldsSpec) {
			s.Deadline = HoldDeadlineSpec{CreatedKey: "hold_created_at", Encoding: EncodingDatetime, MaxAge: "48h"}
		})
		_, ledger := evaluateHolds(t, spec, nil)
		shifted := strconvI(evalNow.Add(-48 * time.Hour).UnixMicro())
		want := `{"$and":[{"$match":{"address":"holds:enfuce:*"}},{"$lte":{"metadata[hold_created_at]":` + shifted + `}}]}`
		if string(ledger.lastQuery) != want {
			t.Errorf("query =\n%s\nwant\n%s", ledger.lastQuery, want)
		}
	})

	t.Run("both keys branch on $exists", func(t *testing.T) {
		spec := holdSpec(t, func(s *StaleHoldsSpec) {
			s.Deadline = HoldDeadlineSpec{
				ExpiryKey: "hold_expires_at", CreatedKey: "hold_created_at",
				Encoding: EncodingDatetime, MaxAge: "48h",
			}
		})
		_, ledger := evaluateHolds(t, spec, nil)
		query := string(ledger.lastQuery)
		for _, fragment := range []string{
			`{"$or":[`,
			`{"$exists":{"metadata[hold_expires_at]":true}}`,
			`{"$exists":{"metadata[hold_expires_at]":false}}`,
			`{"$lte":{"metadata[hold_expires_at]":` + cutoff + `}}`,
			`{"$lte":{"metadata[hold_created_at]":` + strconvI(evalNow.Add(-48*time.Hour).UnixMicro()) + `}}`,
		} {
			if !strings.Contains(query, fragment) {
				t.Errorf("query missing %s:\n%s", fragment, query)
			}
		}
	})

	t.Run("approaching sends both bounds", func(t *testing.T) {
		spec := holdSpec(t, func(s *StaleHoldsSpec) {
			s.Mode = StaleHoldsApproaching
			s.WarnWithin = "6h"
		})
		_, ledger := evaluateHolds(t, spec, nil)
		query := string(ledger.lastQuery)
		if !strings.Contains(query, `{"$gt":{"metadata[hold_expires_at]":`+cutoff+`}}`) {
			t.Errorf("missing the band's lower bound:\n%s", query)
		}
		if !strings.Contains(query, `{"$lte":{"metadata[hold_expires_at]":`+strconvI(evalNow.Add(6*time.Hour).UnixMicro())+`}}`) {
			t.Errorf("missing the band's upper bound:\n%s", query)
		}
	})

	t.Run("epoch seconds encode in their own unit", func(t *testing.T) {
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.Deadline.Encoding = EncodingEpochSeconds })
		_, ledger := evaluateHolds(t, spec, nil)
		want := `{"$lte":{"metadata[hold_expires_at]":` + strconvI(evalNow.Unix()) + `}}`
		if !strings.Contains(string(ledger.lastQuery), want) {
			t.Errorf("query =\n%s\nwant it to contain\n%s", ledger.lastQuery, want)
		}
	})
}

// Explain is persisted as rule.compiled_cel and compiled at rule create, so it
// must type-check against the kernel. It must also be deterministic — the
// cutoff is a fixed placeholder, not the wall clock.
func TestStaleHolds_ExplainCompilesAndIsStable(t *testing.T) {
	t.Parallel()

	spec := holdSpec(t, func(s *StaleHoldsSpec) {
		s.Deadline = HoldDeadlineSpec{
			ExpiryKey: "hold_expires_at", CreatedKey: "hold_created_at",
			Encoding: EncodingDatetime, MaxAge: "48h",
		}
	})
	expression, err := NewStaleHolds().Explain(spec)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	eng, _ := newTestEngine(t, &holdLedger{})
	if _, err := eng.Compile(expression); err != nil {
		t.Fatalf("Explain output does not compile: %v\nexpr: %s", err, expression)
	}
	again, _ := NewStaleHolds().Explain(spec)
	if again != expression {
		t.Errorf("Explain is not deterministic:\n%s\n%s", expression, again)
	}
}

// The create-time query probe must see the deadline clause, or an unindexed
// deadline key would only surface as an evaluation error.
func TestStaleHolds_QueriesIncludeTheDeadlineClause(t *testing.T) {
	t.Parallel()

	sources, err := NewStaleHolds().Queries(holdSpec(t, nil))
	if err != nil {
		t.Fatalf("Queries: %v", err)
	}
	if len(sources) != 1 || sources[0].Ledger != "cards" {
		t.Fatalf("got %+v", sources)
	}
	if !strings.Contains(string(sources[0].Query), "metadata[hold_expires_at]") {
		t.Errorf("query should carry the deadline clause: %s", sources[0].Query)
	}
}

func TestStaleHolds_BudgetIsEnforced(t *testing.T) {
	t.Parallel()

	ledger := &holdLedger{err: errors.New("listAccounts: matched more than 50000 accounts on \"cards\" (accounts budget)")}
	eng, resolvers := newTestEngine(t, ledger)
	_, err := NewStaleHolds().Evaluate(context.Background(), holdSpec(t, nil), eng, resolvers, engine.EvalInput{PIT: evalNow})
	if err == nil || !strings.Contains(err.Error(), "accounts budget") {
		t.Fatalf("expected the budget error to propagate, got %v", err)
	}
}

// With one account per authorisation, the alert should name the hold in the
// operator's terms, not only by ledger address. Declared keys a hold does not
// carry are omitted rather than failing it.
func TestStaleHolds_IdentityLabels(t *testing.T) {
	t.Parallel()

	spec := holdSpec(t, func(s *StaleHoldsSpec) {
		s.IdentityKeys = []string{"enfuce_auth_id", "card_id", "merchant"}
	})
	outcomes, _ := evaluateHolds(t, spec, []engine.Account{
		hold("holds:enfuce:auth-8801", 2500, map[string]string{
			"hold_expires_at": evalNow.Add(-2 * time.Hour).Format(time.RFC3339),
			"enfuce_auth_id":  "AUTH-8801",
			"card_id":         "card_42",
			// no `merchant` key on this hold
		}),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected one outcome, got %d", len(outcomes))
	}
	identity, ok := outcomes[0].Evidence["identity"].(map[string]string)
	if !ok {
		t.Fatalf("identity missing from evidence: %+v", outcomes[0].Evidence)
	}
	if identity["enfuce_auth_id"] != "AUTH-8801" || identity["card_id"] != "card_42" {
		t.Errorf("unexpected identity: %+v", identity)
	}
	if _, present := identity["merchant"]; present {
		t.Errorf("a key the hold does not carry must be omitted: %+v", identity)
	}
}

// Identity labels are opt-in: without them the evidence carries no identity key
// at all, rather than an empty map.
func TestStaleHolds_IdentityAbsentByDefault(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:enfuce:h1", 2500, expiring(evalNow.Add(-time.Hour))),
	})
	if _, present := outcomes[0].Evidence["identity"]; present {
		t.Errorf("identity should be absent when no keys are declared: %+v", outcomes[0].Evidence)
	}
}

// The aggregate sample carries the same labels, so a single alert still names
// the offending holds.
func TestStaleHolds_IdentityInAggregateSample(t *testing.T) {
	t.Parallel()

	spec := holdSpec(t, func(s *StaleHoldsSpec) {
		s.Scope = StaleHoldsAggregate
		s.IdentityKeys = []string{"enfuce_auth_id"}
	})
	outcomes, _ := evaluateHolds(t, spec, []engine.Account{
		hold("holds:enfuce:auth-1", 2500, map[string]string{
			"hold_expires_at": evalNow.Add(-time.Hour).Format(time.RFC3339),
			"enfuce_auth_id":  "AUTH-1",
		}),
	})
	sample, ok := outcomes[0].Evidence["holds"].([]map[string]any)
	if !ok || len(sample) != 1 {
		t.Fatalf("unexpected sample: %+v", outcomes[0].Evidence["holds"])
	}
	identity, ok := sample[0]["identity"].(map[string]string)
	if !ok || identity["enfuce_auth_id"] != "AUTH-1" {
		t.Errorf("aggregate sample should carry identity: %+v", sample[0])
	}
}

// A rule-level cap bounds one evaluation's blast radius: exceeding it fails the
// evaluation rather than truncating the outcome list, because a truncated list
// would make the service auto-resolve the holds it dropped.
func TestStaleHolds_RuleLevelBudget(t *testing.T) {
	t.Parallel()

	accounts := []engine.Account{
		hold("holds:enfuce:h1", 100, expiring(evalNow.Add(-time.Hour))),
		hold("holds:enfuce:h2", 100, expiring(evalNow.Add(-time.Hour))),
		hold("holds:enfuce:h3", 100, expiring(evalNow.Add(-time.Hour))),
	}

	t.Run("under the cap", func(t *testing.T) {
		cap3 := 3
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.MaxHoldsScanned = &cap3 })
		outcomes, _ := evaluateHolds(t, spec, accounts)
		if len(outcomes) != 3 {
			t.Fatalf("expected all three holds flagged, got %d", len(outcomes))
		}
	})

	t.Run("over the cap fails loudly", func(t *testing.T) {
		cap2 := 2
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.MaxHoldsScanned = &cap2 })
		eng, resolvers := newTestEngine(t, &holdLedger{accounts: accounts})
		_, err := NewStaleHolds().Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{PIT: evalNow})
		if err == nil || !strings.Contains(err.Error(), "budget") {
			t.Fatalf("expected the budget to abort the evaluation, got %v", err)
		}
	})

	t.Run("cannot raise the engine budget", func(t *testing.T) {
		// The engine allows 2; the rule asks for 100. The engine wins.
		huge := 100
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.MaxHoldsScanned = &huge })
		resolvers := engine.Resolvers{Ledger: &holdLedger{accounts: accounts}}
		eng, err := engine.New(resolvers, engine.Limits{MaxAccountsScanned: 2})
		if err != nil {
			t.Fatalf("engine.New: %v", err)
		}
		if _, err := NewStaleHolds().Evaluate(context.Background(), spec, eng, resolvers, engine.EvalInput{PIT: evalNow}); err == nil {
			t.Fatal("a rule must not be able to read past the engine's accounts budget")
		}
	})

	t.Run("evidence records the effective budget", func(t *testing.T) {
		cap5 := 5
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.MaxHoldsScanned = &cap5 })
		outcomes, _ := evaluateHolds(t, spec, nil)
		if got := outcomes[0].Evidence["holdsBudget"]; got != 5 {
			t.Errorf("holdsBudget = %v, want 5", got)
		}
	})
}

// per_hold caps itself far below the engine's cluster-wide limit by default,
// because in that scope every matched hold can become an alert. aggregate emits
// one outcome regardless, so it keeps the engine's budget.
func TestStaleHolds_DefaultBudgetByScope(t *testing.T) {
	t.Parallel()

	t.Run("per_hold defaults well below the engine limit", func(t *testing.T) {
		outcomes, _ := evaluateHolds(t, holdSpec(t, nil), nil)
		if got := outcomes[0].Evidence["holdsBudget"]; got != defaultPerHoldBudget {
			t.Errorf("holdsBudget = %v, want the per-hold default %d", got, defaultPerHoldBudget)
		}
		if defaultPerHoldBudget >= engine.DefaultLimits.MaxAccountsScanned {
			t.Errorf("the per-hold default (%d) must sit below the engine budget (%d)",
				defaultPerHoldBudget, engine.DefaultLimits.MaxAccountsScanned)
		}
	})

	t.Run("aggregate keeps the engine budget", func(t *testing.T) {
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.Scope = StaleHoldsAggregate })
		outcomes, _ := evaluateHolds(t, spec, nil)
		if got := outcomes[0].Evidence["holdsBudget"]; got != engine.DefaultLimits.MaxAccountsScanned {
			t.Errorf("holdsBudget = %v, want the engine budget %d", got, engine.DefaultLimits.MaxAccountsScanned)
		}
	})

	t.Run("an explicit cap raises above the per-hold default", func(t *testing.T) {
		bigger := defaultPerHoldBudget * 5
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.MaxHoldsScanned = &bigger })
		outcomes, _ := evaluateHolds(t, spec, nil)
		if got := outcomes[0].Evidence["holdsBudget"]; got != bigger {
			t.Errorf("holdsBudget = %v, want %d", got, bigger)
		}
	})

	t.Run("but never past the engine budget", func(t *testing.T) {
		huge := engine.DefaultLimits.MaxAccountsScanned * 10
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.MaxHoldsScanned = &huge })
		outcomes, _ := evaluateHolds(t, spec, nil)
		if got := outcomes[0].Evidence["holdsBudget"]; got != engine.DefaultLimits.MaxAccountsScanned {
			t.Errorf("holdsBudget = %v, want it clamped to %d", got, engine.DefaultLimits.MaxAccountsScanned)
		}
	})
}

func TestStaleHolds_Validate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		mutate  func(*StaleHoldsSpec)
		wantErr string
	}{
		{name: "valid expiry rule"},
		{
			name:   "valid fallback rule",
			mutate: func(s *StaleHoldsSpec) { s.Deadline.CreatedKey = "hold_created_at"; s.Deadline.MaxAge = "48h" },
		},
		{
			name:    "no deadline key",
			mutate:  func(s *StaleHoldsSpec) { s.Deadline.ExpiryKey = "" },
			wantErr: "expiryKey, createdKey, or both",
		},
		{
			name:    "maxAge without createdKey",
			mutate:  func(s *StaleHoldsSpec) { s.Deadline.MaxAge = "48h" },
			wantErr: "deadline.maxAge applies to deadline.createdKey",
		},
		{
			name:    "createdKey without maxAge",
			mutate:  func(s *StaleHoldsSpec) { s.Deadline.CreatedKey = "hold_created_at" },
			wantErr: "deadline.maxAge is required",
		},
		{
			name:    "negative maxAge",
			mutate:  func(s *StaleHoldsSpec) { s.Deadline.CreatedKey = "c"; s.Deadline.MaxAge = "-1h" },
			wantErr: "must be positive",
		},
		{
			name:    "unknown encoding",
			mutate:  func(s *StaleHoldsSpec) { s.Deadline.Encoding = "rfc3339" },
			wantErr: "deadline.encoding must be one of",
		},
		{
			name:    "warnWithin in stale mode",
			mutate:  func(s *StaleHoldsSpec) { s.WarnWithin = "6h" },
			wantErr: "warnWithin applies to mode",
		},
		{
			name:    "approaching without warnWithin",
			mutate:  func(s *StaleHoldsSpec) { s.Mode = StaleHoldsApproaching },
			wantErr: "warnWithin is required",
		},
		{
			name:    "unknown mode",
			mutate:  func(s *StaleHoldsSpec) { s.Mode = "expired" },
			wantErr: "mode must be",
		},
		{
			name:    "unknown scope",
			mutate:  func(s *StaleHoldsSpec) { s.Scope = "per_account" },
			wantErr: "scope must be",
		},
		{
			name:    "metadata source",
			mutate:  func(s *StaleHoldsSpec) { s.Source.Kind = SourceAccountMetadata; s.Source.MetadataKey = "k" },
			wantErr: "must be a ledger source",
		},
		{
			name:    "missing asset",
			mutate:  func(s *StaleHoldsSpec) { s.Source.Asset = "" },
			wantErr: "missing an asset",
		},
		{
			name:    "missing query",
			mutate:  func(s *StaleHoldsSpec) { s.Source.Query = nil },
			wantErr: "missing a query",
		},
		{
			name:   "rule-level budget",
			mutate: func(s *StaleHoldsSpec) { n := 500; s.MaxHoldsScanned = &n },
		},
		{
			name:    "zero budget",
			mutate:  func(s *StaleHoldsSpec) { n := 0; s.MaxHoldsScanned = &n },
			wantErr: "maxHoldsScanned must be positive",
		},
		{
			name:    "negative budget",
			mutate:  func(s *StaleHoldsSpec) { n := -1; s.MaxHoldsScanned = &n },
			wantErr: "maxHoldsScanned must be positive",
		},
		{
			name:   "identity keys",
			mutate: func(s *StaleHoldsSpec) { s.IdentityKeys = []string{"enfuce_auth_id", "card_id"} },
		},
		{
			name:    "empty identity key",
			mutate:  func(s *StaleHoldsSpec) { s.IdentityKeys = []string{"enfuce_auth_id", " "} },
			wantErr: "identityKeys[1] is empty",
		},
		{
			name:    "duplicate identity key",
			mutate:  func(s *StaleHoldsSpec) { s.IdentityKeys = []string{"card_id", "card_id"} },
			wantErr: "repeats",
		},
		{
			name: "too many identity keys",
			mutate: func(s *StaleHoldsSpec) {
				s.IdentityKeys = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
			},
			wantErr: "at most 8 keys",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := NewStaleHolds().Validate(holdSpec(t, tc.mutate))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected the spec to validate, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("error should wrap ErrInvalidSpec: %v", err)
			}
		})
	}
}

// strconvI renders an epoch as it appears in a query literal.
func strconvI(n int64) string { return strconv.FormatInt(n, 10) }
