package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"testing"

	"github.com/formancehq/reconciliation/plugins/fctl/audit"
)

// restatement is one tracked place that repeats part of the fctl SDK lock in
// text. A repin edits every one of them by hand, so each needs its own anchor.
type restatement struct {
	// path is relative to this directory.
	path string
	// anchor captures exactly one value in its first group. It has to be
	// specific enough that a superseded value cannot survive next to the
	// current one, which is why the match count is asserted too.
	anchor *regexp.Regexp
	// want reads the authoritative value out of the lock.
	want func(sdkLock) string
}

// fctl-sdk.lock.json is the single source of the SDK contract, but ten other
// tracked files restate part of it: the audit constant the golden report and
// the component authoring contract both hang off, the wrapper contract test's
// fixtures, the plugin module requirement, the Nix provenance comment, and
// four documents. Repinning used to mean editing all of them by hand with
// nothing to catch a miss or a leftover.
var restatements = []restatement{
	{
		path:   "../audit/blockers.go",
		anchor: regexp.MustCompile(`const FctlV2Revision = "([0-9a-f]{40})"`),
		want:   func(l sdkLock) string { return l.Commit },
	},
	{
		path:   "../audit/testdata/report.json",
		anchor: regexp.MustCompile(`"fctlV2Revision": "([0-9a-f]{40})"`),
		want:   func(l sdkLock) string { return l.Commit },
	},
	{
		path:   "../component/authoring_contract_test.go",
		anchor: regexp.MustCompile(`fctlSDKRevision\s+= "([0-9a-f]{40})"`),
		want:   func(l sdkLock) string { return l.Commit },
	},
	{
		path:   "../component/authoring_contract_test.go",
		anchor: regexp.MustCompile(`fctlCanonicalWITSHA256 = "([0-9a-f]{64})"`),
		want:   func(l sdkLock) string { return l.WITSHA256 },
	},
	{
		path:   "test-fctl-sdk-contract.sh",
		anchor: regexp.MustCompile(`readonly expected_commit='([0-9a-f]{40})'`),
		want:   func(l sdkLock) string { return l.Commit },
	},
	{
		path:   "test-fctl-sdk-contract.sh",
		anchor: regexp.MustCompile(`readonly expected_repository='(\S+)'`),
		want:   func(l sdkLock) string { return l.Repository },
	},
	{
		path:   "test-fctl-sdk-contract.sh",
		anchor: regexp.MustCompile(`readonly expected_nar_hash='(\S+)'`),
		want:   func(l sdkLock) string { return l.SDKNarHash },
	},
	{
		path:   "test-fctl-sdk-contract.sh",
		anchor: regexp.MustCompile(`readonly expected_wit_hash='([0-9a-f]{64})'`),
		want:   func(l sdkLock) string { return l.WITSHA256 },
	},
	{
		path:   "test-fctl-sdk-contract.sh",
		anchor: regexp.MustCompile(`readonly expected_bundle_nar_hash='(\S+)'`),
		want:   func(l sdkLock) string { return l.BundleNarHash },
	},
	{
		path:   "test-fctl-sdk-contract.sh",
		anchor: regexp.MustCompile(`readonly expected_bundle_path='(\S+)'`),
		want:   func(l sdkLock) string { return l.BundlePath },
	},
	{
		path:   "../sdk/README.md",
		anchor: regexp.MustCompile("commit\n`([0-9a-f]{40})`"),
		want:   func(l sdkLock) string { return l.Commit },
	},
	{
		path:   "../go.mod",
		anchor: regexp.MustCompile(`\n\t(github\.com/formancehq/fctl-v2-poc/pkg/plugin) v`),
		want:   func(l sdkLock) string { return l.ModulePath },
	},
	{
		path:   "../docs/command-inventory.md",
		anchor: regexp.MustCompile("\\| `fctl-v2` \\| `([0-9a-f]{40})`"),
		want:   func(l sdkLock) string { return l.Commit },
	},
	{
		path:   "../docs/operations.generated.md",
		anchor: regexp.MustCompile("fctl-v2 programme revision: `([0-9a-f]{40})`"),
		want:   func(l sdkLock) string { return l.Commit },
	},
	{
		path:   "../../../nix/component-toolchain.nix",
		anchor: regexp.MustCompile(`Aligned with fctl-v2 ([0-9a-f]{40})\.`),
		want:   func(l sdkLock) string { return l.Commit },
	},
	{
		// The README names the revision in short form, so this asserts the
		// prefix rather than the whole commit.
		path:   "../README.md",
		anchor: regexp.MustCompile("as fctl-v2 `([0-9a-f]{7})`"),
		want:   func(l sdkLock) string { return l.Commit[:7] },
	},
}

func TestEveryRestatedSDKFactMatchesTheLock(t *testing.T) {
	lock := readLock(t)
	for _, r := range restatements {
		t.Run(r.path+"/"+r.anchor.String(), func(t *testing.T) {
			matches := r.anchor.FindAllStringSubmatch(read(t, r.path), -1)
			if len(matches) != 1 {
				t.Fatalf("%s states %d values for %s; want exactly one", r.path, len(matches), r.anchor)
			}
			if got, want := matches[0][1], r.want(lock); got != want {
				t.Errorf("%s restates %q, but the lock pins %q", r.path, got, want)
			}
		})
	}
}

// TestAuditRevisionConstantMatchesTheLock binds the compiled constant, not its
// source text. audit.FctlV2Revision is the value the golden report fixture and
// the component authoring contract are both checked against, so it is the one
// restatement that can silently carry a whole subtree away from the lock.
func TestAuditRevisionConstantMatchesTheLock(t *testing.T) {
	if lock := readLock(t); audit.FctlV2Revision != lock.Commit {
		t.Fatalf("audit.FctlV2Revision = %s, but fctl-sdk.lock.json pins %s", audit.FctlV2Revision, lock.Commit)
	}
}

// TestVendoredWITMatchesLock proves the WIT copied into this repository is the
// exact SDK interface the lock pins, without needing an SDK checkout.
func TestVendoredWITMatchesLock(t *testing.T) {
	lock := readLock(t)
	sum := sha256.Sum256([]byte(read(t, "../wit/plugin.wit")))
	if got := hex.EncodeToString(sum[:]); got != lock.WITSHA256 {
		t.Fatalf("vendored wit/plugin.wit hashes to %s, but the lock pins %s", got, lock.WITSHA256)
	}
}

// nonSDKRevisions are the full-length revisions that legitimately appear in the
// same files without being fctl SDK pins. Every other 40-hex revision in those
// files has to be the locked commit, which is what catches a leftover left
// behind somewhere the anchors above do not look.
var nonSDKRevisions = map[string]string{
	audit.ProductRevision:                      "Reconciliation product revision the audit was read at",
	"693c58e27865f83332e6c3199d61fed81b742f41": "legacy fctl CLI baseline the inventory is measured against",
	"448f6df8f688cee5d6995e96b1ffc31f9bf00742": "WASI-Virt source revision pinned by the component toolchain",
	"0000000000000000000000000000000000000000": "all-zero placeholder in the wrapper contract test's fixtures",
}

func TestNoSupersededSDKRevisionSurvives(t *testing.T) {
	lock := readLock(t)
	fullRevision := regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	for _, path := range []string{
		"../audit/blockers.go", "../audit/testdata/report.json",
		"../component/authoring_contract_test.go", "test-fctl-sdk-contract.sh",
		"../docs/command-inventory.md", "../docs/operations.generated.md",
		"../../../nix/component-toolchain.nix", "../README.md", "../fctl-sdk.lock.json",
		"../sdk/README.md",
	} {
		for _, found := range fullRevision.FindAllString(read(t, path), -1) {
			if found == lock.Commit {
				continue
			}
			if _, known := nonSDKRevisions[found]; !known {
				t.Errorf("%s carries unrecognised revision %s; the lock pins %s", path, found, lock.Commit)
			}
		}
	}
}

func readLock(t *testing.T) sdkLock {
	t.Helper()
	file, err := os.Open("../fctl-sdk.lock.json")
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	defer func() { _ = file.Close() }()
	lock, err := decodeLock(file)
	if err != nil {
		t.Fatalf("decode lock: %v", err)
	}
	return lock
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
