package core

import (
	"os"
	"strings"
	"testing"

	reconciliationclient "github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
	clienttypes "github.com/formancehq/reconciliation/pkg/client/types"
)

func TestGeneratedClientContractIsAvailableToTheAdapter(t *testing.T) {
	var _ reconciliationclient.HTTPClient
	_ = components.Policy{}
	_ = components.Reconciliation{}
	if got := clienttypes.MustNewBigIntFromString("9007199254740993").String(); got != "9007199254740993" {
		t.Fatalf("generated bigint scalar = %q", got)
	}
}

func TestAdapterDelegatesRequestConstructionToGeneratedClient(t *testing.T) {
	source, err := os.ReadFile("adapter_v1.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "github.com/formancehq/reconciliation/pkg/client") {
		t.Fatal("adapter does not import the generated client")
	}
	for _, forbidden := range []string{"http.NewRequest", "WithSecurity(", "WithSecuritySource(", "WithRetryConfig(", "operations.WithRetries("} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("adapter contains forbidden generated-client configuration %q", forbidden)
		}
	}
}
