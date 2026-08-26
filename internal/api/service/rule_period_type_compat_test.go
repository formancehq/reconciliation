package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/engine"
	"github.com/formancehq/reconciliation/internal/models"
	"github.com/formancehq/reconciliation/internal/templates"
)

// newCompatService builds a real Service over a fake store. These tests exercise
// the actual CreateRule path — including Validate and the periodType default —
// rather than asserting against a mock, so what they prove is what a client
// would observe.
func newCompatService(t *testing.T) *Service {
	t.Helper()
	resolvers := engine.Resolvers{Ledger: &orchestrationLedger{}, Payments: &orchestrationPayments{}}
	eng, err := engine.New(resolvers, engine.DefaultLimits)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return NewService(newFakeV1Store(), nil, eng, templates.DefaultRegistry(), resolvers)
}

func compatSpec() json.RawMessage {
	return json.RawMessage(`{"ledger":"main","query":{},"mode":"aggregate","bounds":{"USD/2":{"min":100}}}`)
}

// TestCreateRule_AcceptsEitherPeriodTypeKey covers the deprecation window: a
// client still on the pre-2.4.2 contract sends `cadence`, a current client sends
// `periodType`, and both must produce the same rule. Before dual-accept, the
// legacy key was silently dropped and the rule fell back to `continuous` — a
// monthly reconciliation quietly becoming one unbounded period.
func TestCreateRule_AcceptsEitherPeriodTypeKey(t *testing.T) {
	cases := []struct {
		name    string
		req     *CreateRuleRequest
		want    models.PeriodType
		wantErr string
	}{
		{
			name: "legacy cadence only is honoured",
			req:  &CreateRuleRequest{Cadence: models.PeriodTypeMonthly},
			want: models.PeriodTypeMonthly,
		},
		{
			name: "periodType only is honoured",
			req:  &CreateRuleRequest{PeriodType: models.PeriodTypeWeekly},
			want: models.PeriodTypeWeekly,
		},
		{
			name: "both keys agreeing is accepted",
			req:  &CreateRuleRequest{PeriodType: models.PeriodTypeDaily, Cadence: models.PeriodTypeDaily},
			want: models.PeriodTypeDaily,
		},
		{
			name:    "both keys disagreeing is rejected, not silently resolved",
			req:     &CreateRuleRequest{PeriodType: models.PeriodTypeDaily, Cadence: models.PeriodTypeMonthly},
			wantErr: "disagree",
		},
		{
			name: "neither key defaults to continuous",
			req:  &CreateRuleRequest{},
			want: models.PeriodTypeContinuous,
		},
		{
			name:    "an invalid legacy value is rejected like an invalid new one",
			req:     &CreateRuleRequest{Cadence: models.PeriodType("hourly")},
			wantErr: "periodType must be one of",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newCompatService(t)
			tc.req.Name = "compat-" + tc.name
			tc.req.TemplateKind = models.TemplateAccountThreshold
			tc.req.TemplateSpec = compatSpec()

			rule, err := svc.CreateRule(context.Background(), tc.req)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got a rule with periodType %q", tc.wantErr, rule.PeriodType)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateRule: %v", err)
			}
			if rule.PeriodType != tc.want {
				t.Fatalf("periodType = %q, want %q", rule.PeriodType, tc.want)
			}
		})
	}
}

// TestCreateRuleRequest_ResolveClearsLegacyKey pins that the legacy key does not
// survive into the persisted rule path: everything downstream reads PeriodType
// only, so there is no second source of truth to drift.
func TestCreateRuleRequest_ResolveClearsLegacyKey(t *testing.T) {
	req := &CreateRuleRequest{
		Name:         "clears-legacy",
		TemplateKind: models.TemplateAccountThreshold,
		TemplateSpec: compatSpec(),
		Cadence:      models.PeriodTypeMonthly,
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if req.PeriodType != models.PeriodTypeMonthly {
		t.Fatalf("PeriodType = %q, want monthly", req.PeriodType)
	}
	if req.Cadence != "" {
		t.Fatalf("Cadence should be cleared after Validate, got %q", req.Cadence)
	}
}
