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
		Ledger:   "holds",
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
			Ledger: "holds",
			Query:  json.RawMessage(`{"$match":{"address":"holds:*"}}`),
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

// A hold whose recorded expiry has passed lands in the aggregate: one outcome
// per asset, carrying the count and the amount rather than the address.
func TestStaleHolds_FlagsExpiredHold(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:h1", 25_00, expiring(evalNow.Add(-3*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected one outcome, got %d: %+v", len(outcomes), outcomes)
	}
	outcome := outcomes[0]
	if outcome.Passed {
		t.Error("expected the expired hold to fail")
	}
	if want := "asset:USD/2"; outcome.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", outcome.Fingerprint, want)
	}
	if got := outcome.Evidence["holdsFlagged"]; got != 1 {
		t.Errorf("holdsFlagged = %v, want 1", got)
	}
	if got := outcome.Evidence["amountFlagged"]; got != "2500" {
		t.Errorf("amountFlagged = %v, want 2500", got)
	}
	// No per-account data: the address is recoverable from the query, and a list
	// in evidence would grow with the size of the problem.
	for _, key := range []string{"hold", "holds", "holdsSampled", "identity", "basis"} {
		if _, present := outcome.Evidence[key]; present {
			t.Errorf("evidence must not carry %q: %+v", key, outcome.Evidence)
		}
	}
}

// A hold still inside its expiry is not flagged, and the clean run still
// records what was checked.
func TestStaleHolds_PassesWhenNothingIsStale(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:h1", 25_00, expiring(evalNow.Add(12*time.Hour))),
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
		hold("holds:released", 0, expiring(evalNow.Add(-30*24*time.Hour))),
		hold("holds:stuck", 10_00, expiring(evalNow.Add(-1*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected one aggregate outcome, got %d: %+v", len(outcomes), outcomes)
	}
	if got := outcomes[0].Evidence["holdsFlagged"]; got != 1 {
		t.Errorf("holdsFlagged = %v, want only the stuck hold", got)
	}
	if got := outcomes[0].Evidence["holdsReleased"]; got != 1 {
		t.Errorf("holdsReleased = %v, want 1", got)
	}
	if got := outcomes[0].Evidence["amountFlagged"]; got != "1000" {
		t.Errorf("amountFlagged = %v, want only the stuck hold's 1000", got)
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
		hold("holds:soon", 30_00, expiring(evalNow.Add(2*time.Hour))),
		hold("holds:already", 40_00, expiring(evalNow.Add(-2*time.Hour))),
		hold("holds:later", 50_00, expiring(evalNow.Add(24*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected one aggregate outcome, got %d: %+v", len(outcomes), outcomes)
	}
	// Only `soon` is inside the band: `already` has gone stale (the stale rule
	// owns it) and `later` is beyond the window.
	if got := outcomes[0].Evidence["holdsFlagged"]; got != 1 {
		t.Errorf("holdsFlagged = %v, want 1", got)
	}
	if got := outcomes[0].Evidence["amountFlagged"]; got != "3000" {
		t.Errorf("amountFlagged = %v, want only holds:soon's 3000", got)
	}
	if got := outcomes[0].Evidence["deadlineAfter"]; got != evalNow.Format(time.RFC3339) {
		t.Errorf("deadlineAfter = %v, want the band's lower bound", got)
	}
}

// With no recorded expiry, the hold is dated from its creation metadata plus
// the fallback age.
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
		hold("holds:old", 15_00, map[string]string{
			"hold_created_at": evalNow.Add(-50 * time.Hour).Format(time.RFC3339),
		}),
		// No expiry, created 10h ago: still inside the fallback.
		hold("holds:fresh", 15_00, map[string]string{
			"hold_created_at": evalNow.Add(-10 * time.Hour).Format(time.RFC3339),
		}),
		// Recorded expiry present and still in the future: the expiry wins
		// over the fallback, even though the hold is 5 days old.
		hold("holds:extended", 15_00, map[string]string{
			"hold_created_at": evalNow.Add(-5 * 24 * time.Hour).Format(time.RFC3339),
			"hold_expires_at": evalNow.Add(24 * time.Hour).Format(time.RFC3339),
		}),
	})

	if len(outcomes) != 1 {
		t.Fatalf("expected one aggregate outcome, got %d: %+v", len(outcomes), outcomes)
	}
	// Only `old` is past its fallback deadline; `extended`'s recorded expiry wins
	// over the fallback even though it is five days old.
	if got := outcomes[0].Evidence["holdsFlagged"]; got != 1 {
		t.Errorf("holdsFlagged = %v, want 1", got)
	}
	if got := outcomes[0].Evidence["amountFlagged"]; got != "1500" {
		t.Errorf("amountFlagged = %v, want holds:old's 1500", got)
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
				hold("holds:h1", 100, map[string]string{"hold_expires_at": tc.value}),
			})
			if len(outcomes) != 1 || outcomes[0].Passed {
				t.Fatalf("expected the hold to be flagged, got %+v", outcomes)
			}
			if got := outcomes[0].Evidence["oldestDeadline"]; got != deadline.Format(time.RFC3339) {
				t.Errorf("oldestDeadline = %v, want %v", got, deadline.Format(time.RFC3339))
			}
		})
	}
}

// A matched hold whose deadline metadata cannot be read is surfaced as an
// engine error, not silently skipped — the same stance metadataInt takes.
func TestStaleHolds_UnreadableDeadlineIsAnError(t *testing.T) {
	t.Parallel()

	ledger := &holdLedger{accounts: []engine.Account{
		hold("holds:h1", 100, map[string]string{"hold_expires_at": "yesterday"}),
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

func TestStaleHolds_AggregatesTheStaleSet(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:h1", 25_00, expiring(evalNow.Add(-3*time.Hour))),
		hold("holds:h2", 15_00, expiring(evalNow.Add(-9*time.Hour))),
		hold("holds:ok", 99_00, expiring(evalNow.Add(9*time.Hour))),
		hold("holds:released", 0, expiring(evalNow.Add(-99*time.Hour))),
	})

	if len(outcomes) != 1 {
		t.Fatalf("stale_holds must emit exactly one outcome per asset, got %d", len(outcomes))
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
	if got := evidence["oldestDeadline"]; got != evalNow.Add(-9*time.Hour).Format(time.RFC3339) {
		t.Errorf("oldestDeadline = %v, want the worst offender's deadline", got)
	}
	if got := evidence["ledger"]; got != "holds" {
		t.Errorf("ledger = %v, want holds", got)
	}
	// The set is recoverable rather than embedded: effectiveQuery is what this
	// evaluation actually asked the ledger, deadline cutoff included.
	query, _ := evidence["effectiveQuery"].(string)
	for _, want := range []string{`"address":"holds:*"`, "metadata[hold_expires_at]", strconvI(evalNow.UnixMicro())} {
		if !strings.Contains(query, want) {
			t.Errorf("effectiveQuery %q should contain %q", query, want)
		}
	}
}

// The counts partition what the ledger returned, so an operator can add them up
// and get holdsMatched back. The fake resolver ignores the query, which is
// exactly how the divergence case is reachable: it hands back a hold the real
// pushdown would have filtered out, and the authoritative in-Go check rejects
// it rather than dropping it silently.
func TestStaleHolds_CountsPartitionTheMatchedSet(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:stuck", 25_00, expiring(evalNow.Add(-2*time.Hour))),  // flagged
		hold("holds:released", 0, expiring(evalNow.Add(-9*time.Hour))),   // zero balance
		hold("holds:not-due", 30_00, expiring(evalNow.Add(6*time.Hour))), // outside the window
	})

	evidence := outcomes[0].Evidence
	matched, _ := evidence["holdsMatched"].(int)
	released, _ := evidence["holdsReleased"].(int)
	rejected, _ := evidence["holdsRejected"].(int)
	flagged, _ := evidence["holdsFlagged"].(int)

	if matched != 3 || released != 1 || rejected != 1 || flagged != 1 {
		t.Errorf("matched/released/rejected/flagged = %d/%d/%d/%d, want 3/1/1/1",
			matched, released, rejected, flagged)
	}
	if released+rejected+flagged != matched {
		t.Errorf("counts must partition the matched set: %d + %d + %d != %d",
			released, rejected, flagged, matched)
	}
	// Only the flagged hold contributes to the total.
	if got := evidence["amountFlagged"]; got != "2500" {
		t.Errorf("amountFlagged = %v, want only the stuck hold's 2500", got)
	}
}

// A hold the query and the direct check agree on leaves holdsRejected at zero,
// so a non-zero value is a real signal rather than routine noise.
func TestStaleHolds_NothingRejectedWhenTheyAgree(t *testing.T) {
	t.Parallel()

	outcomes, _ := evaluateHolds(t, holdSpec(t, nil), []engine.Account{
		hold("holds:stuck", 25_00, expiring(evalNow.Add(-2*time.Hour))),
	})
	if got := outcomes[0].Evidence["holdsRejected"]; got != 0 {
		t.Errorf("holdsRejected = %v, want 0", got)
	}
}

// The deadline predicate must reach the ledger as a query clause — that is what
// keeps the scan proportional to the stale holds rather than to every hold.
func TestStaleHolds_QueryPushdown(t *testing.T) {
	t.Parallel()

	cutoff := strconvI(evalNow.UnixMicro())

	t.Run("expiry only", func(t *testing.T) {
		_, ledger := evaluateHolds(t, holdSpec(t, nil), nil)
		want := `{"$and":[{"$match":{"address":"holds:*"}},{"$lte":{"metadata[hold_expires_at]":` + cutoff + `}}]}`
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
		want := `{"$and":[{"$match":{"address":"holds:*"}},{"$lte":{"metadata[hold_created_at]":` + shifted + `}}]}`
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
	if len(sources) != 1 || sources[0].Ledger != "holds" {
		t.Fatalf("got %+v", sources)
	}
	if !strings.Contains(string(sources[0].Query), "metadata[hold_expires_at]") {
		t.Errorf("query should carry the deadline clause: %s", sources[0].Query)
	}
}

func TestStaleHolds_BudgetIsEnforced(t *testing.T) {
	t.Parallel()

	ledger := &holdLedger{err: errors.New("listAccounts: matched more than 50000 accounts on \"holds\" (accounts budget)")}
	eng, resolvers := newTestEngine(t, ledger)
	_, err := NewStaleHolds().Evaluate(context.Background(), holdSpec(t, nil), eng, resolvers, engine.EvalInput{PIT: evalNow})
	if err == nil || !strings.Contains(err.Error(), "accounts budget") {
		t.Fatalf("expected the budget error to propagate, got %v", err)
	}
}

func TestStaleHolds_RuleLevelBudget(t *testing.T) {
	t.Parallel()

	accounts := []engine.Account{
		hold("holds:h1", 100, expiring(evalNow.Add(-time.Hour))),
		hold("holds:h2", 100, expiring(evalNow.Add(-time.Hour))),
		hold("holds:h3", 100, expiring(evalNow.Add(-time.Hour))),
	}

	t.Run("under the cap", func(t *testing.T) {
		cap3 := 3
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.MaxHoldsScanned = &cap3 })
		outcomes, _ := evaluateHolds(t, spec, accounts)
		if len(outcomes) != 1 {
			t.Fatalf("expected one aggregate outcome, got %d", len(outcomes))
		}
		if got := outcomes[0].Evidence["holdsFlagged"]; got != 3 {
			t.Errorf("holdsFlagged = %v, want all three", got)
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

// A rule reads under the engine's accounts budget unless it names a smaller
// cap. It can tighten, never loosen.
func TestStaleHolds_DefaultBudgetIsTheEngineLimit(t *testing.T) {
	t.Parallel()

	t.Run("default is the engine budget", func(t *testing.T) {
		outcomes, _ := evaluateHolds(t, holdSpec(t, nil), nil)
		if got := outcomes[0].Evidence["holdsBudget"]; got != engine.DefaultLimits.MaxAccountsScanned {
			t.Errorf("holdsBudget = %v, want the engine budget %d", got, engine.DefaultLimits.MaxAccountsScanned)
		}
	})

	t.Run("an explicit cap tightens it", func(t *testing.T) {
		smaller := engine.DefaultLimits.MaxAccountsScanned / 10
		spec := holdSpec(t, func(s *StaleHoldsSpec) { s.MaxHoldsScanned = &smaller })
		outcomes, _ := evaluateHolds(t, spec, nil)
		if got := outcomes[0].Evidence["holdsBudget"]; got != smaller {
			t.Errorf("holdsBudget = %v, want %d", got, smaller)
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
