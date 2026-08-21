package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/models"
	"github.com/google/uuid"
)

// renderAlert must expose periodID: the OpenAPI Alert schema marks it required,
// and for periodic (daily/weekly/monthly) rules two alerts can share a
// (ruleID, fingerprint) but live in different periods — clients need the period
// to tell them apart.
func TestRenderAlert_IncludesPeriodID(t *testing.T) {
	a := &models.Alert{
		ID:               uuid.New(),
		RuleID:           uuid.New(),
		Fingerprint:      "asset:USD/2",
		PeriodID:         "2026-03",
		Status:           models.AlertOpen,
		Severity:         models.SeverityHigh,
		LastEvaluationID: uuid.New(),
	}
	resp := renderAlert(a)
	if resp.PeriodID != "2026-03" {
		t.Fatalf("renderAlert dropped periodID: got %q", resp.PeriodID)
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"periodID":"2026-03"`) {
		t.Fatalf("periodID absent from alert JSON: %s", b)
	}
}

// The journal links every alert transition to the entry that witnessed it, and
// docs/technical/audit-chain.md states outright that auditSequence is served on
// alerts and alert events — "without those links the evidence exists but is
// attached to nothing verifiable".
//
// It was served on GET /alerts only by accident: that handler renders the model
// directly, so the json tag carried it through, while renderAlert and
// renderAlertEvent dropped it. So GET /alerts had the link, GET /alerts/{id},
// every transition response, and the whole event timeline did not — reproduced
// against the local stack before fixing.
func TestRenderAlert_IncludesAuditSequence(t *testing.T) {
	seq := int64(42)
	a := &models.Alert{
		ID:               uuid.New(),
		RuleID:           uuid.New(),
		Fingerprint:      "asset:USD/2",
		PeriodID:         "2026-03",
		Status:           models.AlertOpen,
		Severity:         models.SeverityHigh,
		LastEvaluationID: uuid.New(),
		AuditSequence:    &seq,
	}
	b, err := json.Marshal(renderAlert(a))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"auditSequence":42`) {
		t.Fatalf("auditSequence absent from alert JSON: %s", b)
	}

	// Absent rather than 0 when there is no link: a zero sequence would name
	// entry 0, which does not exist, and would read as a real reference.
	a.AuditSequence = nil
	b, err = json.Marshal(renderAlert(a))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "auditSequence") {
		t.Fatalf("auditSequence should be omitted when unset: %s", b)
	}
}

func TestRenderAlertEvent_IncludesAuditSequence(t *testing.T) {
	seq := int64(7)
	e := &models.AlertEvent{
		ID:            uuid.New(),
		AlertID:       uuid.New(),
		Type:          models.AlertEventAck,
		NewStatus:     models.AlertAcknowledged,
		Notify:        true,
		AuditSequence: &seq,
	}
	b, err := json.Marshal(renderAlertEvent(e))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"auditSequence":7`) {
		t.Fatalf("auditSequence absent from alert event JSON: %s", b)
	}

	e.AuditSequence = nil
	b, err = json.Marshal(renderAlertEvent(e))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "auditSequence") {
		t.Fatalf("auditSequence should be omitted when unset: %s", b)
	}
}
