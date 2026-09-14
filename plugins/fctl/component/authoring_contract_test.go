package component

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/formancehq/reconciliation/plugins/fctl/audit"
)

const (
	fctlSDKRevision        = "e9b1395f46f3100b381dbe00f5213de28e6df0e1"
	fctlCanonicalWITSHA256 = "38fdf377264eeada82b23fef153e6bf106ed0624e8916ff62cabdf210d6255f5"
)

func TestPluginPinsTheFinalFCTLSDKContract(t *testing.T) {
	if audit.FctlV2Revision != fctlSDKRevision {
		t.Fatalf("audit fctl SDK revision = %s, want final %s", audit.FctlV2Revision, fctlSDKRevision)
	}
	contents, err := os.ReadFile("../wit/plugin.wit")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != fctlCanonicalWITSHA256 {
		t.Fatalf("plugin.wit SHA-256 = %s, want canonical fctl %s contract %s", got, fctlSDKRevision[:8], fctlCanonicalWITSHA256)
	}
}
