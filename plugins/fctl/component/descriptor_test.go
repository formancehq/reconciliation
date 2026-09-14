package component

import (
	"testing"

	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
	portable "github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk/portable/component"
	"github.com/formancehq/reconciliation/plugins/fctl/core"
)

func TestDescriptorIsAValidCommandOnlyComponent(t *testing.T) {
	descriptor := Descriptor()
	if descriptor.Metadata.Name != "reconciliation" || len(descriptor.Commands) != 23 {
		t.Fatalf("identity/commands = %q/%d", descriptor.Metadata.Name, len(descriptor.Commands))
	}
	if len(descriptor.AuthProviders) != 0 || len(descriptor.TargetProviders) != 0 || len(descriptor.SignerProviders) != 0 {
		t.Fatal("unexpected privileged facet")
	}
	if err := sdk.ValidateCatalogue(descriptor.Commands, descriptor.DocumentationResources); err != nil {
		t.Fatal(err)
	}
	if _, err := portable.NewCommand(core.Plugin{}, descriptor); err != nil {
		t.Fatalf("portable descriptor rejected: %v", err)
	}
}
