package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
	"github.com/google/uuid"
)

type blockingRuleReadStore struct {
	*fakeV1Store
	read   chan struct{}
	resume chan struct{}
}

func (s *blockingRuleReadStore) GetRule(ctx context.Context, id uuid.UUID) (*models.Rule, error) {
	rule, err := s.fakeV1Store.GetRule(ctx, id)
	if err != nil {
		return nil, err
	}
	close(s.read)
	<-s.resume
	return rule, nil
}

// TestPatchRule_RederivesExplanationCELOnSpecChange — when a patch changes the
// template spec, PatchRule must re-validate it against the template and
// rederive explanation_cel, so the persisted explanation stays truthful. A patch
// that leaves the template surface alone must not touch it.
func TestPatchRule_RederivesExplanationCELOnSpecChange(t *testing.T) {
	svc, _ := newOrchestrationService(t, &orchestrationLedger{}, &orchestrationPayments{})
	ctx := context.Background()

	created, err := svc.CreateRule(ctx, &CreateRuleRequest{
		Name:         "threshold",
		TemplateKind: models.TemplateAccountThreshold,
		TemplateSpec: json.RawMessage(`{"ledger":"main","query":{},"mode":"aggregate","bounds":{"USD/2":{"min":100}}}`),
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	oldCEL := created.ExplanationCEL
	if !strings.Contains(oldCEL, "100") {
		t.Fatalf("baseline explanation_cel should mention the 100 bound: %q", oldCEL)
	}

	t.Run("spec change rederives explanation_cel", func(t *testing.T) {
		newSpec := json.RawMessage(`{"ledger":"main","query":{},"mode":"aggregate","bounds":{"USD/2":{"min":500}}}`)
		if err := svc.PatchRule(ctx, created.ID, storage.RulePatch{TemplateSpec: newSpec}); err != nil {
			t.Fatalf("PatchRule: %v", err)
		}
		updated, err := svc.GetRule(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetRule: %v", err)
		}
		if updated.ExplanationCEL == oldCEL {
			t.Errorf("explanation_cel not rederived after spec change (still %q)", updated.ExplanationCEL)
		}
		if !strings.Contains(updated.ExplanationCEL, "500") {
			t.Errorf("rederived explanation_cel should reflect the new 500 bound: %q", updated.ExplanationCEL)
		}
	})

	t.Run("invalid spec on patch is rejected before persisting", func(t *testing.T) {
		// min > max is caught by the template's own Validate.
		bad := json.RawMessage(`{"ledger":"main","query":{},"mode":"aggregate","bounds":{"USD/2":{"min":900,"max":100}}}`)
		err := svc.PatchRule(ctx, created.ID, storage.RulePatch{TemplateSpec: bad})
		if !errors.Is(err, templates.ErrInvalidSpec) {
			t.Fatalf("expected ErrInvalidSpec on bad spec patch, got %v", err)
		}
		// The prior good spec must be untouched.
		got, err := svc.GetRule(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetRule: %v", err)
		}
		if !strings.Contains(got.ExplanationCEL, "500") {
			t.Errorf("rejected patch should not have mutated explanation_cel: %q", got.ExplanationCEL)
		}
	})
}

func TestPatchRule_RejectsConcurrentRevisionAfterTemplateValidationRead(t *testing.T) {
	base := newFakeV1Store()
	store := &blockingRuleReadStore{
		fakeV1Store: base,
		read:        make(chan struct{}),
		resume:      make(chan struct{}),
	}
	resolvers := engine.Resolvers{Ledger: &orchestrationLedger{}, Payments: &orchestrationPayments{}}
	eng, err := engine.New(resolvers, engine.DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	svc := NewService(store, nil, eng, templates.DefaultRegistry(), resolvers)
	ctx := context.Background()

	created, err := svc.CreateRule(ctx, &CreateRuleRequest{
		Name:         "threshold",
		TemplateKind: models.TemplateAccountThreshold,
		TemplateSpec: json.RawMessage(`{"ledger":"main","query":{},"mode":"aggregate","bounds":{"USD/2":{"min":100}}}`),
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	patchDone := make(chan error, 1)
	go func() {
		patchDone <- svc.PatchRule(ctx, created.ID, storage.RulePatch{
			TemplateSpec: json.RawMessage(`{"ledger":"main","query":{},"mode":"aggregate","bounds":{"USD/2":{"min":500}}}`),
		})
	}()
	<-store.read

	concurrentName := "concurrently-revised"
	if err := base.PatchRule(ctx, created.ID, storage.RulePatch{Name: &concurrentName}); err != nil {
		t.Fatalf("concurrent PatchRule: %v", err)
	}
	close(store.resume)

	if err := <-patchDone; !errors.Is(err, ErrRuleChanged) {
		t.Fatalf("expected ErrRuleChanged, got %v", err)
	}
	stored, err := base.GetRule(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if stored.Name != concurrentName {
		t.Fatalf("concurrent patch was lost: name = %q", stored.Name)
	}
	if strings.Contains(stored.ExplanationCEL, "500") {
		t.Fatalf("stale template patch must not persist explanation CEL: %q", stored.ExplanationCEL)
	}
}
