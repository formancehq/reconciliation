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

func TestFilterMetadataExists(t *testing.T) {
	t.Parallel()

	f := schema.FilterMetadataExists("counterparty", false)
	if got := f.GetField().GetField().GetMetadata(); got != "counterparty" {
		t.Errorf("field key: got %q", got)
	}

	if f.GetField().GetExistsCond() == nil {
		t.Fatal("exists cond: want non-nil")
	}

	if f.GetField().GetExistsCond().GetIncludeNull() {
		t.Error("includeNull: want false")
	}

	if schema.FilterMetadataExists("k", true).GetField().GetExistsCond().GetIncludeNull() != true {
		t.Error("includeNull: want true")
	}
}

func TestFilterAll(t *testing.T) {
	t.Parallel()

	f := schema.FilterAll(schema.FilterAddressPrefix("rule:"), schema.FilterMetadataBool("enabled", true))
	if n := len(f.GetAnd().GetFilters()); n != 2 {
		t.Fatalf("and filters: got %d, want 2", n)
	}
}

func TestFilterAddressExact(t *testing.T) {
	t.Parallel()

	f := schema.FilterAddressExact("acct:x")
	if got := f.GetAddress().GetHardcodedExact(); got != "acct:x" {
		t.Errorf("exact address: got %q", got)
	}

	if f.GetAddress().GetHardcodedPrefix() != "" {
		t.Error("exact match must not set a prefix")
	}
}

func TestFilterMetadataString(t *testing.T) {
	t.Parallel()

	f := schema.FilterMetadataString("status", "OPEN")
	if got := f.GetField().GetField().GetMetadata(); got != "status" {
		t.Errorf("field key: got %q", got)
	}

	if got := f.GetField().GetStringCond().GetHardcoded(); got != "OPEN" {
		t.Errorf("string cond: got %q", got)
	}
}

func TestFilterMetadataInt64Range(t *testing.T) {
	t.Parallel()

	min := int64(100)
	f := schema.FilterMetadataInt64Range("first_seen_at", &min, nil, true, false)

	cond := f.GetField().GetIntCond()
	if cond.GetMin() != 100 {
		t.Errorf("min: got %d", cond.GetMin())
	}

	if !cond.GetMinExclusive() {
		t.Error("min should be exclusive")
	}

	// nil max leaves the upper bound unset.
	if cond.Max != nil {
		t.Errorf("max: want nil, got %v", cond.Max)
	}
}

func TestFilterAnyNot(t *testing.T) {
	t.Parallel()

	any := schema.FilterAny(schema.FilterMetadataString("status", "OPEN"), schema.FilterMetadataString("status", "ACKNOWLEDGED"))
	if n := len(any.GetOr().GetFilters()); n != 2 {
		t.Fatalf("or filters: got %d, want 2", n)
	}

	not := schema.FilterNot(schema.FilterMetadataBool("enabled", true))
	if not.GetNot().GetFilter().GetField().GetField().GetMetadata() != "enabled" {
		t.Error("not: inner field mismatch")
	}
}

func TestMetadataIndexes(t *testing.T) {
	t.Parallel()

	idxs := schema.MetadataIndexes()
	if len(idxs) == 0 {
		t.Fatal("expected metadata indexes")
	}

	// id must be indexed — it backs id→address resolution.
	found := false
	for _, idx := range idxs {
		if idx.GetMetadata().GetKey() == schema.MetaID {
			found = true
		}

		if idx.GetMetadata().GetTarget() != commonpb.TargetType_TARGET_TYPE_ACCOUNT {
			t.Errorf("index %q: target %v, want ACCOUNT", idx.GetMetadata().GetKey(), idx.GetMetadata().GetTarget())
		}
	}

	if !found {
		t.Errorf("missing required %q index", schema.MetaID)
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
