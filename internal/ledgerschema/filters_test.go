package ledgerschema_test

import (
	"testing"

	"github.com/formancehq/reconciliation/internal/ledgerpb/commonpb"
	schema "github.com/formancehq/reconciliation/internal/ledgerschema"
)

func TestFilterAddressPrefix(t *testing.T) {
	t.Parallel()

	f := schema.FilterAddressPrefix("alert:st:open:")
	if got := f.GetAddress().GetHardcodedPrefix(); got != "alert:st:open:" {
		t.Errorf("prefix: got %q", got)
	}
}

func TestFilterMetadataBool(t *testing.T) {
	t.Parallel()

	f := schema.FilterMetadataBool("enabled", true)
	if got := f.GetField().GetField().GetMetadata(); got != "enabled" {
		t.Errorf("field key: got %q", got)
	}

	if !f.GetField().GetBoolCond().GetHardcoded() {
		t.Error("bool cond: want true")
	}
}

func TestFilterAll(t *testing.T) {
	t.Parallel()

	f := schema.FilterAll(schema.FilterAddressPrefix("rule:"), schema.FilterMetadataBool("enabled", true))
	if n := len(f.GetAnd().GetFilters()); n != 2 {
		t.Fatalf("and filters: got %d, want 2", n)
	}
}

func TestPreparedQueries(t *testing.T) {
	t.Parallel()

	qs := schema.PreparedQueries()
	if len(qs) != 2 {
		t.Fatalf("got %d prepared queries, want 2", len(qs))
	}

	names := map[string]bool{}
	for _, q := range qs {
		names[q.GetName()] = true

		if q.GetTarget() != commonpb.QueryTarget_QUERY_TARGET_ACCOUNTS {
			t.Errorf("%s: target %v, want ACCOUNTS", q.GetName(), q.GetTarget())
		}

		if q.GetFilter() == nil {
			t.Errorf("%s: nil filter", q.GetName())
		}
	}

	for _, want := range []string{schema.PQOpenCount, schema.PQRulesEnabled} {
		if !names[want] {
			t.Errorf("missing prepared query %q", want)
		}
	}
}
