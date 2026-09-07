package templates

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
)

// The shared harness for every template test: an in-memory ledger resolver, an
// engine wired to it, and the small assertion helpers.
// --- helpers -----------------------------------------------------------------

type fakeLedger struct {
	balances map[string]map[string]*big.Int // (ledger|query) → asset → amount
	accounts map[string][]engine.Account    // (ledger|query) → accounts (for per_account)
}

func (f *fakeLedger) AggregateBalance(_ context.Context, ledger string, query json.RawMessage) (map[string]*big.Int, error) {
	if f.balances == nil {
		return map[string]*big.Int{}, nil
	}
	key := ledger + "|" + string(query)
	b, ok := f.balances[key]
	if !ok {
		return map[string]*big.Int{}, nil
	}
	return b, nil
}
func (f *fakeLedger) ListAccounts(_ context.Context, ledger string, query json.RawMessage, limit int) ([]engine.Account, error) {
	if f.accounts == nil {
		return nil, nil
	}
	accts := f.accounts[ledger+"|"+string(query)]
	if len(accts) > limit {
		return nil, errors.New("listAccounts: exceeded accounts budget")
	}
	return accts, nil
}

func newTestEngine(t *testing.T, l engine.LedgerResolver) (*engine.Engine, engine.Resolvers) {
	t.Helper()
	r := engine.Resolvers{Ledger: l}
	eng, err := engine.New(r, engine.DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng, r
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func findOutcome(out []Outcome, fp string) *Outcome {
	for i := range out {
		if out[i].Fingerprint == fp {
			return &out[i]
		}
	}
	return nil
}

// --- registry ---------------------------------------------------------------

func TestDefaultRegistry_ContainsEveryTemplate(t *testing.T) {
	r := DefaultRegistry()
	for _, kind := range []models.TemplateKind{
		models.TemplateBalanceEquation,
		models.TemplateExchangeRateBounds,
		models.TemplateSourceConsensus,
		models.TemplateCoverageRatioBounds,
		models.TemplateStaleHolds,
		models.TemplateBalanceBounds,
	} {
		if _, err := r.Get(kind); err != nil {
			t.Errorf("registry missing %s: %v", kind, err)
		}
	}
}

// The retired V1 kinds must not resolve. A persisted rule naming one now takes
// the engine-error path (ERROR evaluation + engine.error meta-alert) rather than
// failing silently — see Service.EvaluateRule.
func TestDefaultRegistry_RetiredKindsAreGone(t *testing.T) {
	r := DefaultRegistry()
	for _, kind := range []string{"ledger_invariant", "account_threshold", "source_parity"} {
		if _, err := r.Get(models.TemplateKind(kind)); !errors.Is(err, ErrUnknownTemplate) {
			t.Errorf("retired kind %s still resolves (err=%v)", kind, err)
		}
	}
}

func TestRegistry_UnknownKind(t *testing.T) {
	r := DefaultRegistry()
	_, err := r.Get(models.TemplateKind("nope"))
	if !errors.Is(err, ErrUnknownTemplate) {
		t.Errorf("expected ErrUnknownTemplate, got %v", err)
	}
}
