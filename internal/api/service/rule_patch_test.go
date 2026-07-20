package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/formancehq/reconciliation/internal/templates"
)

// TestPatchRule_RederivesCompiledCELOnSpecChange — when a patch changes the
// template spec, PatchRule must re-validate it against the template and
// rederive compiled_cel, so the persisted explanation stays truthful. A patch
// that leaves the template surface alone must not touch it.
func TestPatchRule_RederivesCompiledCELOnSpecChange(t *testing.T) {
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
	oldCEL := created.CompiledCEL
	if !strings.Contains(oldCEL, "100") {
		t.Fatalf("baseline compiled_cel should mention the 100 bound: %q", oldCEL)
	}

	t.Run("spec change rederives compiled_cel", func(t *testing.T) {
		newSpec := json.RawMessage(`{"ledger":"main","query":{},"mode":"aggregate","bounds":{"USD/2":{"min":500}}}`)
		if err := svc.PatchRule(ctx, created.ID, storage.RulePatch{TemplateSpec: newSpec}); err != nil {
			t.Fatalf("PatchRule: %v", err)
		}
		updated, err := svc.GetRule(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetRule: %v", err)
		}
		if updated.CompiledCEL == oldCEL {
			t.Errorf("compiled_cel not rederived after spec change (still %q)", updated.CompiledCEL)
		}
		if !strings.Contains(updated.CompiledCEL, "500") {
			t.Errorf("rederived compiled_cel should reflect the new 500 bound: %q", updated.CompiledCEL)
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
		if !strings.Contains(got.CompiledCEL, "500") {
			t.Errorf("rejected patch should not have mutated compiled_cel: %q", got.CompiledCEL)
		}
	})
}
