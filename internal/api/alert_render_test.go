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
