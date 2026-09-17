package templates

import (
	"encoding/json"
	"testing"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/stretchr/testify/require"
)

// Queries() is the create-time guard's only input: Service.validateQueries
// calls it and runs every returned SourceSpec through the ledger's
// ValidateQuery, so an unindexed metadata key or an unsupported predicate is
// rejected when the rule is created rather than on every evaluation forever
// after.
//
// A template that under-reports its sources silently disables that guard for
// the ones it omits, and nothing else in the system notices: the rule is
// accepted, and the omitted source only fails at evaluation time. So the
// property under test is not "Queries returns something" but "Queries returns
// exactly the sources this spec declares".
//
// The case table is keyed by template kind and checked for exhaustiveness
// against DefaultRegistry below, so a newly shipped template cannot land
// without one.
var queriesCases = map[models.TemplateKind]struct {
	spec json.RawMessage
	want []SourceSpec
}{
	models.TemplateBalanceEquation: {
		spec: mustMarshal(BalanceEquationSpec{
			Sources: []V2NamedSource{
				{ID: "a", Ledger: "ledger-a", Query: json.RawMessage(`{"$match":{"address":"assets:*"}}`), Asset: "USD/2"},
				{ID: "b", Ledger: "ledger-b", Query: json.RawMessage(`{}`), Asset: "USD/2"},
			},
			Terms:     []BalanceEquationTerm{{Source: "a", Coefficient: 1}, {Source: "b", Coefficient: -1}},
			Tolerance: "0",
		}),
		want: []SourceSpec{
			{Ledger: "ledger-a", Query: json.RawMessage(`{"$match":{"address":"assets:*"}}`), Asset: "USD/2"},
			{Ledger: "ledger-b", Query: json.RawMessage(`{}`), Asset: "USD/2"},
		},
	},
	models.TemplateExchangeRateBounds: {
		spec: mustMarshal(ExchangeRateBoundsSpec{
			Sources: []V2NamedSource{
				{ID: "eur", Ledger: "book", Query: json.RawMessage(`{"$match":{"address":"eur:*"}}`), Asset: "EUR/2"},
				{ID: "usd", Ledger: "book", Query: json.RawMessage(`{"$match":{"address":"usd:*"}}`), Asset: "USD/2"},
			},
			BaseSource:  "eur",
			QuoteSource: "usd",
			Rate:        RateConstraint{Min: "1", Max: "1.25"},
		}),
		want: []SourceSpec{
			{Ledger: "book", Query: json.RawMessage(`{"$match":{"address":"eur:*"}}`), Asset: "EUR/2"},
			{Ledger: "book", Query: json.RawMessage(`{"$match":{"address":"usd:*"}}`), Asset: "USD/2"},
		},
	},
	models.TemplateSourceConsensus: {
		spec: mustMarshal(SourceConsensusSpec{
			Sources: []V2NamedSource{
				{ID: "book", Ledger: "books", Query: json.RawMessage(`{}`), Asset: "USD/2"},
				{ID: "bank", Ledger: "bank", Query: json.RawMessage(`{"$match":{"address":"bank:*"}}`), Asset: "USD/2"},
				{ID: "processor", Ledger: "processor", Query: json.RawMessage(`{}`), Asset: "USD/2"},
			},
			Tolerance: "10",
		}),
		want: []SourceSpec{
			{Ledger: "books", Query: json.RawMessage(`{}`), Asset: "USD/2"},
			{Ledger: "bank", Query: json.RawMessage(`{"$match":{"address":"bank:*"}}`), Asset: "USD/2"},
			{Ledger: "processor", Query: json.RawMessage(`{}`), Asset: "USD/2"},
		},
	},
	models.TemplateCoverageRatioBounds: {
		spec: mustMarshal(CoverageRatioBoundsSpec{
			Sources: []V2NamedSource{
				{ID: "cash", Ledger: "books", Query: json.RawMessage(`{"$match":{"address":"cash:*"}}`), Asset: "USD/2"},
				{ID: "securities", Ledger: "books", Query: json.RawMessage(`{}`), Asset: "USD/2"},
				{ID: "liabilities", Ledger: "books", Query: json.RawMessage(`{}`), Asset: "USD/2"},
			},
			NumeratorTerms:   []BalanceEquationTerm{{Source: "cash", Coefficient: 1}, {Source: "securities", Coefficient: 1}},
			DenominatorTerms: []BalanceEquationTerm{{Source: "liabilities", Coefficient: 1}},
			Ratio:            RateConstraint{Min: "1", Max: "1.25"},
		}),
		want: []SourceSpec{
			{Ledger: "books", Query: json.RawMessage(`{"$match":{"address":"cash:*"}}`), Asset: "USD/2"},
			{Ledger: "books", Query: json.RawMessage(`{}`), Asset: "USD/2"},
			{Ledger: "books", Query: json.RawMessage(`{}`), Asset: "USD/2"},
		},
	},
	models.TemplateBalanceBounds: {
		spec: mustMarshal(BalanceBoundsSpec{
			Source: V2NamedSource{
				ID: "treasury", Ledger: "book",
				Query: json.RawMessage(`{"$match":{"address":"treasury:*"}}`), Asset: "USD/2",
			},
			Bounds: map[string]BalanceBound{"USD/2": {Min: "0", Max: "1000000"}},
		}),
		want: []SourceSpec{
			{Ledger: "book", Query: json.RawMessage(`{"$match":{"address":"treasury:*"}}`), Asset: "USD/2"},
		},
	},
	// stale_holds is the one template whose Queries() does not simply project
	// the spec: it returns the *augmented* query (the rule's own clause AND the
	// deadline clause), because that augmented shape is what the evaluation
	// actually runs and therefore what the guard has to validate. Asserting the
	// projection here would be asserting the wrong contract, so this case pins
	// the augmentation instead — see queriesAugmentsStaleHolds.
	models.TemplateStaleHolds: {
		spec: mustMarshal(StaleHoldsSpec{
			Source: V2NamedSource{
				ID:     "holds",
				Ledger: "holds",
				Query:  json.RawMessage(`{"$match":{"address":"holds:*"}}`),
				Asset:  "USD/2",
			},
			Deadline: HoldDeadlineSpec{ExpiryKey: "hold_expires_at", Encoding: EncodingDatetime},
		}),
		want: nil, // checked by queriesAugmentsStaleHolds, not by equality
	},
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return b
}

// Every registered template must have a case. Reading the kinds off
// DefaultRegistry rather than a literal list is what makes this a gate: adding
// a template to the registry without adding a case here fails, instead of the
// new template silently shipping with an unexercised create-time guard.
func TestQueriesCasesAreExhaustive(t *testing.T) {
	t.Parallel()

	for kind := range DefaultRegistry().evaluators {
		if _, ok := queriesCases[kind]; !ok {
			t.Errorf("template %q is registered but has no Queries() case in queriesCases", kind)
		}
	}

	for kind := range queriesCases {
		if _, err := DefaultRegistry().Get(kind); err != nil {
			t.Errorf("queriesCases has a case for %q, which is not registered", kind)
		}
	}
}

func TestQueriesReturnsEveryDeclaredSource(t *testing.T) {
	t.Parallel()

	for kind, tc := range queriesCases {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			evaluator, err := DefaultRegistry().Get(kind)
			require.NoError(t, err)

			got, err := evaluator.Queries(tc.spec)
			require.NoError(t, err)

			if kind == models.TemplateStaleHolds {
				queriesAugmentsStaleHolds(t, got)

				return
			}

			require.Len(t, got, len(tc.want), "one SourceSpec per declared source")

			for i := range tc.want {
				require.Equal(t, tc.want[i].Ledger, got[i].Ledger, "source %d ledger", i)
				require.Equal(t, tc.want[i].Asset, got[i].Asset, "source %d asset", i)
				require.JSONEq(t, string(tc.want[i].Query), string(got[i].Query), "source %d query", i)
			}
		})
	}
}

// Whatever a template returns has to be usable by the guard: validateQueries
// passes each SourceSpec straight to ValidateQuery(ctx, src.Ledger, src.Query),
// so a blank ledger or a query that is not valid JSON means that source is
// either skipped or rejected for the wrong reason.
func TestQueriesReturnsValidatableSpecs(t *testing.T) {
	t.Parallel()

	for kind, tc := range queriesCases {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			evaluator, err := DefaultRegistry().Get(kind)
			require.NoError(t, err)

			got, err := evaluator.Queries(tc.spec)
			require.NoError(t, err)
			require.NotEmpty(t, got, "a template with no sources cannot be validated at create time")

			for i, src := range got {
				require.NotEmpty(t, src.Ledger, "source %d has no ledger, so ValidateQuery cannot target it", i)

				if len(src.Query) > 0 {
					require.True(t, json.Valid(src.Query), "source %d query is not valid JSON", i)
				}
			}
		})
	}
}

// An undecodable spec must come back as ErrInvalidSpec rather than an empty
// source list: validateQueries propagates the error and the service maps
// ErrInvalidSpec to a 400, whereas an empty list reads as "nothing to validate"
// and lets the rule through unguarded.
//
// Only spec-wide decode failures are asserted, not field-shape mismatches:
// Service.validateQueries runs after Validate, so by the time Queries is called
// the spec is already well-formed for its own template. A per-template bad
// payload would be testing a state the production path cannot reach — and the
// templates genuinely differ here, since balance_bounds takes a singular
// `source` where the others take a `sources` array.
func TestQueriesRejectsUndecodableSpec(t *testing.T) {
	t.Parallel()

	undecodable := map[string]json.RawMessage{
		"invalid JSON": json.RawMessage(`{"sources":`),
		"empty spec":   json.RawMessage(``),
		"wrong type":   json.RawMessage(`"a string, not an object"`),
	}

	for kind := range queriesCases {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			evaluator, err := DefaultRegistry().Get(kind)
			require.NoError(t, err)

			for name, raw := range undecodable {
				t.Run(name, func(t *testing.T) {
					got, err := evaluator.Queries(raw)
					require.ErrorIs(t, err, ErrInvalidSpec)
					require.Empty(t, got)
				})
			}
		})
	}
}

// stale_holds must return the augmented query — its own clause AND the deadline
// clause — because that is the query the evaluation runs. Returning the rule's
// bare query would validate a predicate that is never executed and leave the
// deadline key's index requirement unchecked until the first evaluation.
func queriesAugmentsStaleHolds(t *testing.T, got []SourceSpec) {
	t.Helper()

	require.Len(t, got, 1)
	require.Equal(t, "holds", got[0].Ledger)

	query := string(got[0].Query)
	require.True(t, json.Valid(got[0].Query), "augmented query is not valid JSON")
	require.Contains(t, query, "hold_expires_at", "the deadline key must be in the validated query")
	require.Contains(t, query, "holds:*", "the rule's own clause must survive augmentation")
}
