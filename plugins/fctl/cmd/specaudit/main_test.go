package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const specPath = "../../../../openapi.yaml"

// prepare makes an empty artefact tree the generator can write into.
func prepare(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{filepath.Join("audit", "testdata"), "docs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	return root
}

func TestCommandReportsBuildFailureWithoutExiting(t *testing.T) {
	var stderr bytes.Buffer
	if status := command([]string{"-spec", filepath.Join(t.TempDir(), "absent.yaml")}, &stderr); status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}
	if got := stderr.String(); got == "" {
		t.Fatal("missing diagnostic")
	}
}

func TestCommandRejectsInvalidFlags(t *testing.T) {
	var stderr bytes.Buffer
	if status := command([]string{"-not-a-flag"}, &stderr); status != 2 {
		t.Fatalf("status = %d, want 2", status)
	}
}

func artefacts(root string) []string {
	return []string{
		filepath.Join(root, "audit", "testdata", "report.json"),
		filepath.Join(root, "docs", "operations.generated.md"),
	}
}

// TestRunWritesBothArtefacts proves the generator writes exactly the two
// committed artefacts.
func TestRunWritesBothArtefacts(t *testing.T) {
	root := prepare(t)

	if err := run(specPath, root, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, path := range artefacts(root) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", path)
		}
	}
}

// TestRunIsByteReproducible is the second-generation gate Task 10A asks for on
// generated output: regenerating over a clean tree must produce zero diff.
func TestRunIsByteReproducible(t *testing.T) {
	root := prepare(t)

	if err := run(specPath, root, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := map[string][]byte{}
	for _, path := range artefacts(root) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		first[path] = data
	}

	if err := run(specPath, root, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	for path, want := range first {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("re-read %s: %v", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is not byte-reproducible across runs", path)
		}
	}
}

// TestCheckPassesOnFreshOutput proves -check accepts what -out just wrote.
func TestCheckPassesOnFreshOutput(t *testing.T) {
	root := prepare(t)

	if err := run(specPath, root, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := run(specPath, root, true); err != nil {
		t.Errorf("check on fresh output: %v", err)
	}
}

// TestCheckFailsOnStaleOutput proves -check is a real gate: a tampered artefact
// is rejected rather than silently rewritten.
func TestCheckFailsOnStaleOutput(t *testing.T) {
	root := prepare(t)

	if err := run(specPath, root, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	stale := filepath.Join(root, "docs", "operations.generated.md")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := run(specPath, root, true); err == nil {
		t.Error("expected -check to reject a stale artefact")
	}
}

// TestCheckFailsWhenArtefactIsMissing proves -check does not pass silently when
// a committed artefact has been deleted.
func TestCheckFailsWhenArtefactIsMissing(t *testing.T) {
	root := prepare(t)

	if err := run(specPath, root, true); err == nil {
		t.Error("expected -check to fail when the artefacts do not exist")
	}
}

// TestRunRejectsAMissingDocument proves a bad -spec is an error, not an empty
// inventory.
func TestRunRejectsAMissingDocument(t *testing.T) {
	root := prepare(t)

	if err := run(filepath.Join(root, "absent.yaml"), root, false); err == nil {
		t.Error("expected an error for a missing document")
	}
}
