package templates

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
)

func TestSourceConsensus_Validate(t *testing.T) {
	t.Parallel()
	valid := SourceConsensusSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("book", "books", `{}`, "USD/2"),
			v2LedgerSource("bank", "bank", `{}`, "USD/2"),
			v2LedgerSource("processor", "processor", `{}`, "USD/2"),
		},
		Tolerance: "10",
	}
	requireValidSpec(t, NewSourceConsensus(), valid)

	differentAsset := valid
	differentAsset.Sources = append([]V2NamedSource(nil), valid.Sources...)
	differentAsset.Sources[2].Asset = "EUR/2"
	if err := NewSourceConsensus().Validate(mustJSON(t, differentAsset)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("different assets: expected ErrInvalidSpec, got %v", err)
	}

	negativeTolerance := valid
	negativeTolerance.Tolerance = "-1"
	if err := NewSourceConsensus().Validate(mustJSON(t, negativeTolerance)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("negative tolerance: expected ErrInvalidSpec, got %v", err)
	}
}

func TestSourceConsensus_ExactSpreadMatchesCEL(t *testing.T) {
	t.Parallel()
	const (
		qA = `{"$match":{"address":"a"}}`
		qB = `{"$match":{"address":"b"}}`
		qC = `{"$match":{"address":"c"}}`
	)
	base, _ := new(big.Int).SetString("1000000000000000000000000000000", 10)
	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"books|" + qA: {"USD/2": new(big.Int).Add(new(big.Int).Set(base), big.NewInt(4))},
		"bank|" + qB:  {"USD/2": new(big.Int).Add(new(big.Int).Set(base), big.NewInt(10))},
		"proc|" + qC:  {"USD/2": new(big.Int).Add(new(big.Int).Set(base), big.NewInt(7))},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := SourceConsensusSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("book", "books", qA, "USD/2"),
			v2LedgerSource("bank", "bank", qB, "USD/2"),
			v2LedgerSource("processor", "proc", qC, "USD/2"),
		},
		Tolerance: "6",
	}
	raw := mustJSON(t, spec)
	tmpl := NewSourceConsensus()
	outcomes, err := tmpl.Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Passed {
		t.Fatalf("outcomes = %+v, want pass", outcomes)
	}
	evidence := outcomes[0].Evidence
	if evidence["spread"] != "6" || evidence["minimumSource"] != "book" || evidence["maximumSource"] != "bank" {
		t.Fatalf("evidence = %+v", evidence)
	}
	assertTemplateCELMatches(t, tmpl, raw, eng, outcomes[0].Passed)
}

func TestSourceConsensus_MissingSourceFails(t *testing.T) {
	t.Parallel()
	const q = `{}`
	ledger := &fakeLedger{balances: map[string]map[string]*big.Int{
		"books|" + q: {"USD/2": big.NewInt(0)},
		"bank|" + q:  {},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := SourceConsensusSpec{
		Sources: []V2NamedSource{
			v2LedgerSource("book", "books", q, "USD/2"),
			v2LedgerSource("bank", "bank", q, "USD/2"),
		},
		Tolerance: "0",
	}
	raw := mustJSON(t, spec)
	tmpl := NewSourceConsensus()
	outcomes, err := tmpl.Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcomes[0].Passed {
		t.Fatal("missing source must fail consensus")
	}
	missing := outcomes[0].Evidence["missingSources"].([]string)
	if len(missing) != 1 || missing[0] != "bank" {
		t.Fatalf("missingSources = %v", missing)
	}
	assertTemplateCELMatches(t, tmpl, raw, eng, outcomes[0].Passed)
}

func TestSourceConsensus_EmptyMetadataQueryIsMissing(t *testing.T) {
	t.Parallel()
	const q = `{}`
	ledger := &fakeLedger{accounts: map[string][]engine.Account{
		"book|" + q: {{Address: "mirror:book", Metadata: map[string]string{"amount": "0"}}},
		"bank|" + q: nil,
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := SourceConsensusSpec{
		Sources: []V2NamedSource{
			{ID: "book", Kind: SourceAccountMetadata, Ledger: "book", Query: json.RawMessage(q), MetadataKey: "amount", Asset: "USD/2"},
			{ID: "bank", Kind: SourceAccountMetadata, Ledger: "bank", Query: json.RawMessage(q), MetadataKey: "amount", Asset: "USD/2"},
		},
		Tolerance: "0",
	}
	outcomes, err := NewSourceConsensus().Evaluate(context.Background(), mustJSON(t, spec), eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcomes[0].Passed {
		t.Fatal("an empty metadata query must fail consensus presence")
	}
	missing := outcomes[0].Evidence["missingSources"].([]string)
	if len(missing) != 1 || missing[0] != "bank" {
		t.Fatalf("missingSources = %v, want [bank]", missing)
	}
	assertTemplateCELMatches(t, NewSourceConsensus(), mustJSON(t, spec), eng, outcomes[0].Passed)
}

func TestSourceConsensus_MissingSourceStillResolvesLaterInvalidSource(t *testing.T) {
	t.Parallel()
	const q = `{}`
	ledger := &fakeLedger{accounts: map[string][]engine.Account{
		"book|" + q: nil,
		"bank|" + q: {{Address: "mirror:bank", Metadata: map[string]string{"amount": "invalid"}}},
	}}
	eng, resolvers := newTestEngine(t, ledger)
	spec := SourceConsensusSpec{
		Sources: []V2NamedSource{
			{ID: "book", Kind: SourceAccountMetadata, Ledger: "book", Query: json.RawMessage(q), MetadataKey: "amount", Asset: "USD/2"},
			{ID: "bank", Kind: SourceAccountMetadata, Ledger: "bank", Query: json.RawMessage(q), MetadataKey: "amount", Asset: "USD/2"},
		},
		Tolerance: "0",
	}
	raw := mustJSON(t, spec)
	tmpl := NewSourceConsensus()
	if _, err := tmpl.Evaluate(context.Background(), raw, eng, resolvers, engine.EvalInput{}); err == nil {
		t.Fatal("direct evaluation must reject later invalid metadata")
	}
	expression, err := tmpl.Explain(raw)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	compiled, err := eng.Compile(expression)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := eng.Evaluate(context.Background(), compiled, engine.EvalInput{}); err == nil {
		t.Fatal("CEL evaluation must reject later invalid metadata")
	}
}

func TestV2MetadataSourcesShareEvaluationAccountBudget(t *testing.T) {
	t.Parallel()
	const q = `{}`
	ledger := &fakeLedger{accounts: map[string][]engine.Account{
		"book|" + q: {{Address: "mirror:book", Metadata: map[string]string{"amount": "10"}}},
		"bank|" + q: {{Address: "mirror:bank", Metadata: map[string]string{"amount": "10"}}},
	}}
	resolvers := engine.Resolvers{Ledger: ledger}
	eng, err := engine.New(resolvers, engine.Limits{MaxAccountsScanned: 1})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	spec := SourceConsensusSpec{
		Sources: []V2NamedSource{
			{ID: "book", Kind: SourceAccountMetadata, Ledger: "book", Query: json.RawMessage(q), MetadataKey: "amount", Asset: "USD/2"},
			{ID: "bank", Kind: SourceAccountMetadata, Ledger: "bank", Query: json.RawMessage(q), MetadataKey: "amount", Asset: "USD/2"},
		},
		Tolerance: "0",
	}
	_, err = NewSourceConsensus().Evaluate(context.Background(), mustJSON(t, spec), eng, resolvers, engine.EvalInput{})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected cumulative evaluation budget error, got %v", err)
	}
	expression, err := NewSourceConsensus().Explain(mustJSON(t, spec))
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	compiled, err := eng.Compile(expression)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = eng.Evaluate(context.Background(), compiled, engine.EvalInput{})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected cumulative CEL evaluation budget error, got %v", err)
	}
}

func TestV2MetadataSourcesAllowZeroMatchAtExhaustedBudget(t *testing.T) {
	t.Parallel()
	const q = `{}`
	ledger := &fakeLedger{accounts: map[string][]engine.Account{
		"book|" + q: {{Address: "mirror:book", Metadata: map[string]string{"amount": "10"}}},
		"bank|" + q: nil,
	}}
	resolvers := engine.Resolvers{Ledger: ledger}
	eng, err := engine.New(resolvers, engine.Limits{MaxAccountsScanned: 1})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	spec := SourceConsensusSpec{
		Sources: []V2NamedSource{
			{ID: "book", Kind: SourceAccountMetadata, Ledger: "book", Query: json.RawMessage(q), MetadataKey: "amount", Asset: "USD/2"},
			{ID: "bank", Kind: SourceAccountMetadata, Ledger: "bank", Query: json.RawMessage(q), MetadataKey: "amount", Asset: "USD/2"},
		},
		Tolerance: "0",
	}

	outcomes, err := NewSourceConsensus().Evaluate(context.Background(), mustJSON(t, spec), eng, resolvers, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcomes[0].Passed {
		t.Fatal("zero-match source must fail consensus presence")
	}
	missing := outcomes[0].Evidence["missingSources"].([]string)
	if len(missing) != 1 || missing[0] != "bank" {
		t.Fatalf("missingSources = %v, want [bank]", missing)
	}
}

func requireValidSpec(t *testing.T, evaluator Evaluator, spec any) {
	t.Helper()
	if err := evaluator.Validate(mustJSON(t, spec)); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
}

func assertTemplateCELMatches(t *testing.T, evaluator Evaluator, raw []byte, eng *engine.Engine, direct bool) {
	t.Helper()
	expression, err := evaluator.Explain(raw)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	compiled, err := eng.Compile(expression)
	if err != nil {
		t.Fatalf("Compile %q: %v", expression, err)
	}
	result, err := eng.Evaluate(context.Background(), compiled, engine.EvalInput{})
	if err != nil {
		t.Fatalf("Evaluate CEL %q: %v", expression, err)
	}
	if result.Passed != direct {
		t.Fatalf("CEL verdict %v != direct verdict %v", result.Passed, direct)
	}
}
