package templates

// Runs the golden cases in testdata/golden against this package's templates.
//
// The cases describe the target semantics of reconciliation v3, independent
// of any implementation: the re-implementation (S5, S6) reuses the JSON files
// with its own harness. This harness adapts them to the prototype: it turns
// the per-asset `tolerances` entries (minor units, an asset without an entry
// being exact) into the single minor-unit `tolerance` string the prototype
// reads, and skips the cases that need different tolerances per asset.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type goldenFile struct {
	Template string       `json:"template"`
	Cases    []goldenCase `json:"cases"`
}

type goldenCase struct {
	Name            string                     `json:"name"`
	Requires        []string                   `json:"requires"`
	Spec            map[string]json.RawMessage `json:"spec"`
	Reads           map[string]goldenRead      `json:"reads"`
	Expect          []map[string]any           `json:"expect"`
	Invalid         bool                       `json:"invalid"`
	EvaluationError bool                       `json:"evaluationError"`
}

type goldenRead struct {
	Balances map[string]string `json:"balances"`
	Accounts []struct {
		Address  string            `json:"address"`
		Metadata map[string]string `json:"metadata"`
	} `json:"accounts"`
}

// goldenUnsupported lists the semantics the prototype does not implement.
var goldenUnsupported = map[string]string{
	"v3-tolerances": "per-asset tolerances (review decision 12c) come with S5",
}

func TestGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "golden", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	registry := DefaultRegistry()
	for _, path := range files {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var file goldenFile
		require.NoError(t, json.Unmarshal(raw, &file), path)

		evaluator, err := registry.Get(models.TemplateKind(file.Template))
		require.NoError(t, err, path)

		t.Run(file.Template, func(t *testing.T) {
			for _, tc := range file.Cases {
				t.Run(tc.Name, func(t *testing.T) { runGoldenCase(t, evaluator, tc) })
			}
		})
	}
}

func runGoldenCase(t *testing.T, evaluator Evaluator, tc goldenCase) {
	for _, feature := range tc.Requires {
		if reason, unsupported := goldenUnsupported[feature]; unsupported {
			t.Skipf("needs %s: %s", feature, reason)
		}
	}

	spec, err := prototypeSpec(tc)
	require.NoError(t, err)

	if err := evaluator.Validate(spec); err != nil {
		require.ErrorIs(t, err, ErrInvalidSpec)
		require.True(t, tc.Invalid, "spec rejected: %v", err)
		return
	}
	require.False(t, tc.Invalid, "spec accepted, want it rejected")

	ledger, err := newGoldenLedger(evaluator, spec, tc.Reads)
	require.NoError(t, err)
	eng, resolvers := newTestEngine(t, ledger)

	outcomes, err := evaluator.Evaluate(t.Context(), spec, eng, resolvers, engine.EvalInput{})
	if tc.EvaluationError {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)

	got := make([]string, 0, len(outcomes))
	for _, o := range outcomes {
		got = append(got, o.Fingerprint)
	}
	want := make([]string, 0, len(tc.Expect))
	for _, e := range tc.Expect {
		want = append(want, fingerprintFor("asset", e["asset"].(string)))
	}
	require.Equal(t, want, got, "one outcome per expected asset, sorted by asset")

	for i, e := range tc.Expect {
		o := outcomes[i]
		assert.Equal(t, e["passed"], o.Passed, "%s: passed", o.Fingerprint)
		for field, value := range e {
			if field == "asset" || field == "passed" {
				continue
			}
			wantJSON, _ := json.Marshal(value)
			gotJSON, _ := json.Marshal(o.Evidence[field])
			assert.JSONEq(t, string(wantJSON), string(gotJSON), "%s: evidence %s", o.Fingerprint, field)
		}
	}
}

// prototypeSpec turns the v3 `tolerances` (per-asset entries in minor units,
// an asset without an entry being exact) into the prototype's single
// minor-unit `tolerance`. Every asset the case touches must resolve to the same
// value: a case that needs more is marked v3-tolerances. A key that is not an
// asset code is passed on as an invalid tolerance, so the prototype rejects it.
func prototypeSpec(tc goldenCase) (json.RawMessage, error) {
	spec := maps.Clone(tc.Spec)
	tolerances := map[string]string{}
	if raw, ok := spec["tolerances"]; ok {
		if err := json.Unmarshal(raw, &tolerances); err != nil {
			return nil, err
		}
		delete(spec, "tolerances")
	}
	if _, ok := spec["sources"]; !ok { // balance_bounds has no tolerance
		return json.Marshal(spec)
	}
	for key := range tolerances {
		if !engine.ValidAssetCode(key) {
			spec["tolerance"], _ = json.Marshal("invalid-key:" + key)
			return json.Marshal(spec)
		}
	}
	assets, err := caseAssets(tc)
	if err != nil {
		return nil, err
	}
	effective := ""
	for _, asset := range assets {
		value, ok := tolerances[asset]
		if !ok {
			value = "0"
		}
		if effective != "" && value != effective {
			return nil, fmt.Errorf("case %s needs per-asset tolerances: mark it v3-tolerances", tc.Name)
		}
		effective = value
	}
	if effective == "" {
		effective = "0"
	}
	spec["tolerance"], _ = json.Marshal(effective)
	return json.Marshal(spec)
}

// caseAssets returns the assets a case evaluates: the declared assets of a
// fixed-asset spec, or the expected assets of a wildcard spec.
func caseAssets(tc goldenCase) ([]string, error) {
	var sources []V2NamedSource
	if raw, ok := tc.Spec["sources"]; ok {
		if err := json.Unmarshal(raw, &sources); err != nil {
			return nil, err
		}
	}
	var assets []string
	for _, s := range sources {
		if s.Asset != AssetWildcard && !slices.Contains(assets, s.Asset) {
			assets = append(assets, s.Asset)
		}
	}
	for _, e := range tc.Expect {
		if a, _ := e["asset"].(string); !slices.Contains(assets, a) {
			assets = append(assets, a)
		}
	}
	return assets, nil
}

// goldenLedger serves each source's reads, keyed by ledger and compacted query.
type goldenLedger struct {
	balances map[string]map[string]*big.Int
	accounts map[string][]engine.Account
}

func newGoldenLedger(evaluator Evaluator, spec json.RawMessage, reads map[string]goldenRead) (*goldenLedger, error) {
	l := &goldenLedger{balances: map[string]map[string]*big.Int{}, accounts: map[string][]engine.Account{}}
	var shape struct {
		Sources []V2NamedSource `json:"sources"`
		Source  *V2NamedSource  `json:"source"`
	}
	if err := json.Unmarshal(spec, &shape); err != nil {
		return nil, err
	}
	sources := shape.Sources
	if shape.Source != nil {
		sources = append(sources, *shape.Source)
	}
	for _, s := range sources {
		key, err := goldenKey(s.Ledger, s.Query)
		if err != nil {
			return nil, err
		}
		read := reads[s.ID]
		l.balances[key] = map[string]*big.Int{}
		for asset, amount := range read.Balances {
			n, ok := new(big.Int).SetString(amount, 10)
			if !ok {
				return nil, fmt.Errorf("source %s: amount %q is not an integer", s.ID, amount)
			}
			l.balances[key][asset] = n
		}
		for _, a := range read.Accounts {
			l.accounts[key] = append(l.accounts[key], engine.Account{Address: a.Address, Ledger: s.Ledger, Metadata: a.Metadata})
		}
	}
	return l, nil
}

func goldenKey(ledger string, query json.RawMessage) (string, error) {
	b, err := goldenCompact(query)
	if err != nil {
		return "", err
	}
	return ledger + "|" + string(b), nil
}

func goldenCompact(raw json.RawMessage) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func (l *goldenLedger) AggregateBalance(_ context.Context, ledger string, query json.RawMessage) (map[string]*big.Int, error) {
	key, err := goldenKey(ledger, query)
	if err != nil {
		return nil, err
	}
	b, ok := l.balances[key]
	if !ok {
		return nil, errors.New("golden: unexpected read " + key)
	}
	return b, nil
}

func (l *goldenLedger) ListAccounts(_ context.Context, ledger string, query json.RawMessage, _ int) ([]engine.Account, error) {
	key, err := goldenKey(ledger, query)
	if err != nil {
		return nil, err
	}
	return l.accounts[key], nil
}
