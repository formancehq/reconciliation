package ledgerschema_test

import (
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
)

const (
	ruleID = "550e8400-e29b-41d4-a716-446655440000"
	period = "2026-03"
)

func TestFingerprintHash(t *testing.T) {
	t.Parallel()

	h := schema.FingerprintHash("asset:USD/2|account:merchant:m1:held")
	if len(h) != 16 {
		t.Fatalf("want 16 hex chars, got %d (%q)", len(h), h)
	}

	if h != schema.FingerprintHash("asset:USD/2|account:merchant:m1:held") {
		t.Fatal("hash is not deterministic")
	}

	if h == schema.FingerprintHash("asset:EUR/2") {
		t.Fatal("distinct fingerprints collided")
	}
}

func TestAddressBuilders(t *testing.T) {
	t.Parallel()

	fp := schema.FingerprintHash("asset:USD/2")

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"rule", schema.RuleAccount(ruleID), "rule:" + ruleID},
		{"item", schema.AlertItemAccount(ruleID, period, fp), "alert:item:rule:" + ruleID + ":per:" + period + ":fp:" + fp},
		{"state", schema.AlertStateAccount(schema.StateOpen, ruleID, period, fp), "alert:st:open:rule:" + ruleID + ":per:" + period + ":fp:" + fp},
		{"pool", schema.PoolAccount(ruleID), "alert:pool:rule:" + ruleID},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// The status-left naming must let the aggregation prefix match every open marker.
func TestOpenPrefixMatchesMarkers(t *testing.T) {
	t.Parallel()

	fp := schema.FingerprintHash("x")
	open := schema.AlertStateAccount(schema.StateOpen, ruleID, period, fp)

	if !strings.HasPrefix(open, schema.OpenPrefix()) {
		t.Errorf("%q is not under global open prefix %q", open, schema.OpenPrefix())
	}

	if !strings.HasPrefix(open, schema.OpenByRulePrefix(ruleID)) {
		t.Errorf("%q is not under by-rule open prefix %q", open, schema.OpenByRulePrefix(ruleID))
	}

	// The (rule, period) sweep prefix must match this open marker, and its
	// remainder is the fp segment.
	if !strings.HasPrefix(open, schema.OpenByRulePeriodPrefix(ruleID, period)) {
		t.Errorf("%q is not under (rule,period) open prefix %q", open, schema.OpenByRulePeriodPrefix(ruleID, period))
	}

	// A resolved marker must NOT be counted as open.
	resolved := schema.AlertStateAccount(schema.StateResolved, ruleID, period, fp)
	if strings.HasPrefix(resolved, schema.OpenPrefix()) {
		t.Errorf("resolved marker %q wrongly matches open prefix", resolved)
	}
}

func TestAccountTypesChart(t *testing.T) {
	t.Parallel()

	types := schema.AccountTypes()
	for _, name := range []string{
		schema.AccountTypeRule, schema.AccountTypeAlertItem, schema.AccountTypeAlertState,
		schema.AccountTypeAlertPool, schema.AccountTypeCapture, schema.AccountTypeCapturePool,
	} {
		at, ok := types[name]
		if !ok {
			t.Fatalf("missing account type %q", name)
		}

		if at.GetPattern() == "" {
			t.Errorf("account type %q has empty pattern", name)
		}
	}

	// Only the marker family is EPHEMERAL (so drained states self-purge; metadata
	// never lives on an ephemeral account).
	if got := types[schema.AccountTypeAlertState].GetPersistence(); got != commonpb.AccountTypePersistence_ACCOUNT_TYPE_EPHEMERAL {
		t.Errorf("alert-state persistence: got %v, want EPHEMERAL", got)
	}

	if got := types[schema.AccountTypeAlertItem].GetPersistence(); got != commonpb.AccountTypePersistence_ACCOUNT_TYPE_NORMAL {
		t.Errorf("alert-item persistence: got %v, want NORMAL", got)
	}
}

func TestMetadataSchemaAllAccountTarget(t *testing.T) {
	t.Parallel()

	cmds := schema.MetadataSchema()
	if len(cmds) == 0 {
		t.Fatal("empty metadata schema")
	}

	for _, c := range cmds {
		if c.GetTargetType() != commonpb.TargetType_TARGET_TYPE_ACCOUNT {
			t.Errorf("field %q: target %v, want ACCOUNT", c.GetKey(), c.GetTargetType())
		}

		if c.GetKey() == "" {
			t.Error("metadata field with empty key")
		}
	}
}
