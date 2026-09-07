package service

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/ledger"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/templates"
)

// validatingLedger is a resolver that carries the optional ValidateQuery
// capability (the production resolver's create-time guard), so CreateRule /
// PatchRule exercise the query-validation branch. ValidateQuery returns err.
type validatingLedger struct {
	err error
}

func (validatingLedger) AggregateBalance(context.Context, string, json.RawMessage) (map[string]*big.Int, error) {
	return map[string]*big.Int{}, nil
}

func (validatingLedger) ListAccounts(context.Context, string, json.RawMessage, int) ([]engine.Account, error) {
	return nil, nil
}

func (l validatingLedger) ValidateQuery(context.Context, string, json.RawMessage) error {
	return l.err
}

func newValidatingService(t *testing.T, l validatingLedger) (*Service, *fakeV1Store) {
	t.Helper()
	store := newFakeV1Store()
	res := engine.Resolvers{Ledger: l}
	eng, err := engine.New(res, engine.DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return NewService(store, eng, templates.DefaultRegistry(), res), store
}

func createParityRule(svc *Service, spec json.RawMessage) (*models.Rule, error) {
	return svc.CreateRule(context.Background(), &CreateRuleRequest{
		Name:         "r",
		TemplateKind: models.TemplateBalanceEquation,
		TemplateSpec: spec,
	})
}

// TestCreateRule_RejectsUnindexedQuery — a metadata query the target ledger can't
// plan (ValidateQuery → ledger.ErrQueryIndex) is rejected at create as a 400-class
// ErrInvalidSpec, and nothing is persisted (the reported eval-time ERROR is caught
// at create instead).
func TestCreateRule_RejectsUnindexedQuery(t *testing.T) {
	t.Parallel()

	svc, store := newValidatingService(t, validatingLedger{err: ledger.ErrQueryIndex})
	spec := paritySpec(t, "l", "r", `{"$match":{"metadata[tier]":"gold"}}`, nil)

	_, err := createParityRule(svc, spec)
	if !errors.Is(err, templates.ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec, got %v", err)
	}
	if len(store.rules) != 0 {
		t.Fatalf("rule must not be persisted, got %d", len(store.rules))
	}
}

// TestCreateRule_RejectsUntranslatableQuery — an unsupported operator
// (ledger.ErrQueryUnsupported) is likewise a 400.
func TestCreateRule_RejectsUntranslatableQuery(t *testing.T) {
	t.Parallel()

	svc, store := newValidatingService(t, validatingLedger{err: ledger.ErrQueryUnsupported})
	spec := paritySpec(t, "l", "r", `{"$like":{"metadata[type]":"pay*"}}`, nil)

	_, err := createParityRule(svc, spec)
	if !errors.Is(err, templates.ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec, got %v", err)
	}
	if len(store.rules) != 0 {
		t.Fatalf("rule must not be persisted, got %d", len(store.rules))
	}
}

// TestCreateRule_TransientValidationError — a non-sentinel error (e.g. the ledger
// is unreachable) is surfaced as-is, NOT masqueraded as a 400 validation failure,
// so the API renders it 500.
func TestCreateRule_TransientValidationError(t *testing.T) {
	t.Parallel()

	boom := errors.New("ledger unavailable")
	svc, store := newValidatingService(t, validatingLedger{err: boom})
	spec := paritySpec(t, "l", "r", `{"$match":{"address":"x"}}`, nil)

	_, err := createParityRule(svc, spec)
	if !errors.Is(err, boom) {
		t.Fatalf("want the transient error, got %v", err)
	}
	if errors.Is(err, templates.ErrInvalidSpec) {
		t.Fatal("transient error must not be wrapped as ErrInvalidSpec (would 400 a healthy rule)")
	}
	if len(store.rules) != 0 {
		t.Fatalf("rule must not be persisted, got %d", len(store.rules))
	}
}

// TestCreateRule_ValidQueryPersists — when ValidateQuery passes, the rule is
// created normally.
func TestCreateRule_ValidQueryPersists(t *testing.T) {
	t.Parallel()

	svc, store := newValidatingService(t, validatingLedger{err: nil})
	spec := paritySpec(t, "l", "r", `{"$match":{"metadata[tier]":"gold"}}`, nil)

	rule, err := createParityRule(svc, spec)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if rule == nil || len(store.rules) != 1 {
		t.Fatalf("rule must be persisted, got %d", len(store.rules))
	}
}
