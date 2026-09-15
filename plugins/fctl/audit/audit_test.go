package audit_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/formancehq/reconciliation/plugins/fctl/audit"
)

// specPath is the Reconciliation document, relative to this package directory.
const specPath = "../../../openapi.yaml"

func build(t *testing.T) *audit.Report {
	t.Helper()
	report, err := audit.Build(specPath)
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	return report
}

// TestReportMatchesGolden is the determinism gate: the inventory numbers and
// per-operation facts quoted in the inventory document are exactly what the
// current document yields. A spec change is expected to fail this test.
func TestReportMatchesGolden(t *testing.T) {
	report := build(t)

	got, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("encode report: %v", err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "report.json")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	if string(got) != string(want) {
		t.Fatalf("report drifted from %s; run `just fctl-audit` and review the diff", golden)
	}
}

// TestGeneratedMarkdownIsCommitted keeps the generated tables in the docs
// directory in step with the golden report.
func TestGeneratedMarkdownIsCommitted(t *testing.T) {
	report := build(t)

	path := filepath.Join("..", "docs", "operations.generated.md")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated markdown: %v", err)
	}
	if report.Markdown() != string(want) {
		t.Fatalf("%s is out of date; run `just fctl-audit`", path)
	}
}

// TestBuildIsDeterministic proves the report does not depend on Go's map
// iteration order: two independent builds of the same document must encode
// byte-identically.
func TestBuildIsDeterministic(t *testing.T) {
	for i := 0; i < 8; i++ {
		first, err := json.Marshal(build(t))
		if err != nil {
			t.Fatalf("encode first: %v", err)
		}
		second, err := json.Marshal(build(t))
		if err != nil {
			t.Fatalf("encode second: %v", err)
		}
		if string(first) != string(second) {
			t.Fatalf("two builds of the same document differ on iteration %d", i)
		}
	}
}

// TestEveryOperationIsClassified fails when the document grows an operation
// that the frozen family table does not name. This is the guard that stops a
// new spec operation from being silently absorbed.
func TestEveryOperationIsClassified(t *testing.T) {
	report := build(t)

	for _, rec := range report.Operations {
		if rec.Family == "" {
			t.Errorf("operation %s has no family", rec.OperationID)
		}
	}

	// The reverse direction: the table must not name an operation the document
	// no longer declares.
	present := map[string]struct{}{}
	for _, rec := range report.Operations {
		present[rec.OperationID] = struct{}{}
	}
	for _, id := range audit.ClassifiedOperationIDs() {
		if _, ok := present[id]; !ok {
			t.Errorf("family table names %s, which the document does not declare", id)
		}
	}
}

// TestBaselineTargetsExist proves every legacy command maps onto an operation
// the current document actually declares.
func TestBaselineTargetsExist(t *testing.T) {
	report := build(t)
	if missing := report.UnknownBaselineTargets(); len(missing) > 0 {
		t.Fatalf("baseline maps onto operations the document does not declare: %v", missing)
	}
}

// TestBlockersReferenceRealOperations stops a blocker from outliving the
// operation it was recorded against.
func TestBlockersReferenceRealOperations(t *testing.T) {
	report := build(t)

	present := map[string]struct{}{}
	for _, rec := range report.Operations {
		present[rec.OperationID] = struct{}{}
	}
	for _, b := range audit.Blockers {
		if len(b.OperationIDs) == 0 {
			t.Errorf("blocker %s lists no operation", b.ID)
		}
		if b.Summary == "" || b.Evidence == "" {
			t.Errorf("blocker %s has an empty summary or evidence", b.ID)
		}
		for _, id := range b.OperationIDs {
			if _, ok := present[id]; !ok {
				t.Errorf("blocker %s references unknown operation %s", b.ID, id)
			}
		}
	}
}

// TestBaselineIsExactlySevenMappedCommands pins the legacy surface. The
// fctl-v2 inventory at the pinned programme revision lists exactly these seven
// executable `reconciliation` leaves; a change here is a change to the parity
// obligation, not a refactor.
func TestBaselineIsExactlySevenMappedCommands(t *testing.T) {
	report := build(t)

	if got := report.Totals.BaselineCommands; got != 7 {
		t.Errorf("baseline commands = %d, want 7", got)
	}
	if got := report.Totals.BaselineMapped; got != 7 {
		t.Errorf("mapped baseline commands = %d, want 7", got)
	}
	if got := report.Totals.BaselineExcluded; got != 0 {
		t.Errorf("excluded baseline commands = %d, want 0", got)
	}

	want := []string{
		"reconciliation get <reconciliationID>",
		"reconciliation list",
		"reconciliation policies create <file>|-",
		"reconciliation policies delete <policyID>",
		"reconciliation policies get <policyID>",
		"reconciliation policies list",
		"reconciliation policies reconcile <policyID> <atLedger> <atPayments>",
	}
	var got []string
	for _, c := range audit.Baseline {
		got = append(got, c.Path)
		if len(c.Aliases) == 0 {
			t.Errorf("baseline command %q records no alias", c.Path)
		}
		if c.SourceFile == "" {
			t.Errorf("baseline command %q records no source file", c.Path)
		}
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("baseline paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("baseline path %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestFrozenTrancheMatchesBaseline proves the first tranche and the legacy
// baseline describe the same seven operations. Task 10B states Reconciliation
// covers "reconciliations and policies"; this asserts that statement is exactly
// the legacy surface, with nothing added and nothing dropped.
func TestFrozenTrancheMatchesBaseline(t *testing.T) {
	report := build(t)

	var frozen []string
	for _, rec := range report.Operations {
		if rec.Frozen {
			frozen = append(frozen, rec.OperationID)
			if len(rec.BaselineCommands) == 0 {
				t.Errorf("frozen operation %s has no legacy precedent", rec.OperationID)
			}
		}
	}
	sort.Strings(frozen)

	baseline := audit.BaselineTargets()
	if len(frozen) != len(baseline) {
		t.Fatalf("frozen tranche = %v, baseline targets = %v", frozen, baseline)
	}
	for i := range baseline {
		if frozen[i] != baseline[i] {
			t.Errorf("frozen[%d] = %q, baseline[%d] = %q", i, frozen[i], i, baseline[i])
		}
	}
}

// TestTotalsAreInternallyConsistent checks the derived counts against each
// other, so a quoted number in the inventory cannot drift from the partition it
// claims to describe.
func TestTotalsAreInternallyConsistent(t *testing.T) {
	report := build(t)
	t0 := report.Totals

	if t0.SpecOperations != t0.UniqueOperationIDs {
		t.Errorf("operations = %d but unique operationIds = %d", t0.SpecOperations, t0.UniqueOperationIDs)
	}
	if t0.SpecOperations != len(report.Operations) {
		t.Errorf("operations total = %d but %d records", t0.SpecOperations, len(report.Operations))
	}
	if got := t0.FrozenOperations + t0.RecordedOperations; got != t0.SpecOperations {
		t.Errorf("frozen + recorded = %d, want %d", got, t0.SpecOperations)
	}
	if got := t0.WithBaseline + t0.WithoutBaseline; got != t0.SpecOperations {
		t.Errorf("withBaseline + withoutBaseline = %d, want %d", got, t0.SpecOperations)
	}
	if got := t0.Admissible + t0.Blocked; got != t0.SpecOperations {
		t.Errorf("admissible + blocked = %d, want %d", got, t0.SpecOperations)
	}
	if got := t0.FrozenAdmissible + t0.FrozenBlocked; got != t0.FrozenOperations {
		t.Errorf("frozenAdmissible + frozenBlocked = %d, want %d", got, t0.FrozenOperations)
	}
	if t0.Blocked != len(audit.BlockedOperationIDs()) {
		t.Errorf("blocked = %d, want %d", t0.Blocked, len(audit.BlockedOperationIDs()))
	}
}

// TestMissingDocumentIsAnError proves the loader reports a missing document
// rather than silently producing an empty inventory.
func TestMissingDocumentIsAnError(t *testing.T) {
	if _, err := audit.Build(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("expected an error for a missing document")
	}
}

// TestMalformedDocumentIsAnError proves a document this audit cannot read
// exactly is rejected rather than partially interpreted.
func TestMalformedDocumentIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(path, []byte("paths: [not-a-mapping\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := audit.Build(path); err == nil {
		t.Fatal("expected an error for a malformed document")
	}
}

// TestUnclassifiedOperationIsRejected proves Build refuses a document carrying
// an operation the family table does not name, rather than dropping it.
func TestUnclassifiedOperationIsRejected(t *testing.T) {
	doc := `
openapi: 3.0.3
info: {title: t, version: v}
paths:
  /brand-new:
    get:
      operationId: brandNewOperation
      tags: [reconciliation.v1]
      responses:
        '200': {description: ok}
`
	path := filepath.Join(t.TempDir(), "extra.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := audit.Build(path); err == nil {
		t.Fatal("expected an error for an unclassified operation")
	}
}
