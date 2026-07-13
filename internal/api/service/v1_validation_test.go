package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/storage"
	"github.com/google/uuid"
)

// Severity outside the public enum is rejected at create validation rather than
// deferred to the DB CHECK constraint (which would surface as a 500).
func TestCreateRuleRequest_Validate_Severity(t *testing.T) {
	base := func() *CreateRuleRequest {
		return &CreateRuleRequest{Name: "r", TemplateKind: models.TemplateAccountThreshold, TemplateSpec: json.RawMessage(`{}`)}
	}
	cases := map[string]struct {
		sev     models.Severity
		wantErr bool
	}{
		"empty defaults, ok": {"", false},
		"valid high":         {models.SeverityHigh, false},
		"invalid urgent":     {models.Severity("urgent"), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := base()
			req.Severity = tc.sev
			if err := req.Validate(); tc.wantErr != (err != nil) {
				t.Fatalf("severity %q: wantErr=%v, got %v", tc.sev, tc.wantErr, err)
			}
		})
	}
}

// A schedule with an unknown or empty kind is rejected rather than persisted and
// then silently ignored by the scheduler.
func TestCreateRuleRequest_Validate_ScheduleKind(t *testing.T) {
	base := func() *CreateRuleRequest {
		return &CreateRuleRequest{Name: "r", TemplateKind: models.TemplateAccountThreshold, TemplateSpec: json.RawMessage(`{}`)}
	}
	cases := map[string]struct {
		sched   *models.Schedule
		wantErr bool
	}{
		"unknown kind": {&models.Schedule{Kind: models.ScheduleKind("hourly")}, true},
		"empty kind":   {&models.Schedule{}, true},
		"on_demand ok": {&models.Schedule{Kind: models.ScheduleOnDemand}, false},
		"cron ok":      {&models.Schedule{Kind: models.ScheduleCron, Expr: "*/5 * * * *"}, false},
		"nil ok":       {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := base()
			req.Schedule = tc.sched
			if err := req.Validate(); tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}
}

// PatchRule applies the same severity/schedule validation as create, wrapped in
// ErrValidation so the HTTP layer maps it to 400 (not the DB CHECK → 500, nor a
// silently-skipped malformed cron). Validation runs before touching the store,
// so a non-existent id still surfaces the validation error.
func TestPatchRule_RejectsInvalidSeverityAndSchedule(t *testing.T) {
	svc, _ := newOrchestrationService(t, &orchestrationLedger{}, &orchestrationPayments{})
	ctx := context.Background()
	id := uuid.New()

	badSev := models.Severity("urgent")
	cases := map[string]storage.RulePatch{
		"invalid severity":      {Severity: &badSev},
		"malformed cron":        {Schedule: &models.Schedule{Kind: models.ScheduleCron, Expr: "not a cron"}},
		"cron without expr":     {Schedule: &models.Schedule{Kind: models.ScheduleCron}},
		"unknown schedule kind": {Schedule: &models.Schedule{Kind: models.ScheduleKind("weekly-ish")}},
	}
	for name, patch := range cases {
		t.Run(name, func(t *testing.T) {
			if err := svc.PatchRule(ctx, id, patch); !errors.Is(err, ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

// Alert-action requests missing their required fields must return ErrValidation
// (→ HTTP 400), not a bare error that falls through to 500. Validation runs
// before the store call, so a random id is fine.
func TestAlertActionValidation_MapsToErrValidation(t *testing.T) {
	svc, _ := newOrchestrationService(t, &orchestrationLedger{}, &orchestrationPayments{})
	ctx := context.Background()
	id := uuid.New()

	if _, err := svc.AckAlert(ctx, id, &AckAlertRequest{}); !errors.Is(err, ErrValidation) {
		t.Errorf("ack missing 'by': expected ErrValidation, got %v", err)
	}
	if _, err := svc.ResolveAlert(ctx, id, &ResolveAlertRequest{}); !errors.Is(err, ErrValidation) {
		t.Errorf("resolve missing 'by': expected ErrValidation, got %v", err)
	}
	if _, err := svc.AcceptAlert(ctx, id, &AcceptAlertRequest{}); !errors.Is(err, ErrValidation) {
		t.Errorf("accept missing 'by': expected ErrValidation, got %v", err)
	}
	if _, err := svc.AcceptAlert(ctx, id, &AcceptAlertRequest{By: "ops"}); !errors.Is(err, ErrValidation) {
		t.Errorf("accept missing 'note': expected ErrValidation, got %v", err)
	}
	if _, err := svc.SnoozeAlert(ctx, id, &SnoozeAlertRequest{}); !errors.Is(err, ErrValidation) {
		t.Errorf("snooze missing 'by': expected ErrValidation, got %v", err)
	}
	if _, err := svc.UnsnoozeAlert(ctx, id, &UnsnoozeAlertRequest{}); !errors.Is(err, ErrValidation) {
		t.Errorf("unsnooze missing 'by': expected ErrValidation, got %v", err)
	}
}
