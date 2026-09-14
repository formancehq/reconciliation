package core

import (
	"reflect"
	"testing"

	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
	"github.com/formancehq/reconciliation/plugins/fctl/audit"
)

func TestCatalogueMatchesEveryPublicProductOperation(t *testing.T) {
	commands := Catalogue()
	if len(commands) != 23 {
		t.Fatalf("Catalogue() has %d commands, want 23", len(commands))
	}
	if err := sdk.ValidateCatalogue(commands, nil); err != nil {
		t.Fatalf("ValidateCatalogue: %v", err)
	}
	report, err := audit.Build("../../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]audit.Record{}
	for _, operation := range report.Operations {
		if operation.OperationID != "getServerInfo" {
			want[operation.OperationID] = operation
		}
	}
	seen := map[string]bool{}
	for _, command := range commands {
		if len(command.Operations) != 1 {
			t.Fatalf("%s has %d operations", command.ID, len(command.Operations))
		}
		operation := command.Operations[0]
		source, ok := want[operation.ID]
		if !ok {
			t.Fatalf("unexpected operation %q", operation.ID)
		}
		if operation.Service != sdk.ServiceReconciliation || operation.HTTP == nil || operation.HTTP.GeneratedClient == nil || operation.HTTP.Method != source.Method || operation.HTTP.GeneratedClient.PathTemplate != source.Path || !reflect.DeepEqual(operation.Scopes, source.Scopes) {
			t.Errorf("%s drifted from inventory: %#v vs %#v", command.ID, operation, source)
		}
		if command.Pagination.Supported != source.Risk.Paginated {
			t.Errorf("%s pagination drift", command.ID)
		}
		seen[operation.ID] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("covered %d operations, want %d", len(seen), len(want))
	}
}

func TestPluginFacetRequiresOnlyGeneratedHTTP(t *testing.T) {
	plugin := Plugin{}
	if plugin.Metadata().Name != Name || plugin.Metadata().Version != Version {
		t.Fatal("plugin identity drifted")
	}
	if err := sdk.ValidateCommandFacetHostRequirements(plugin.Metadata().Facets, plugin.Commands()); err != nil {
		t.Fatal(err)
	}
	if plugin.DocumentationResources() != nil {
		t.Fatal("unexpected bundled documentation")
	}
}

func TestEveryCommandDeclaresExactScopeAndBoundedHostTraffic(t *testing.T) {
	for _, command := range Catalogue() {
		t.Run(command.ID, func(t *testing.T) {
			operation := command.Operations[0]
			wantScope := "reconciliation:write"
			wantRequests := uint32(1)
			if operation.HTTP.Method == "GET" {
				wantScope = "reconciliation:read"
			}
			if command.Pagination.Supported {
				wantRequests = sdk.DefaultAllPagesMaxPages
			}
			if !reflect.DeepEqual(operation.Scopes, []string{wantScope}) {
				t.Fatalf("scopes = %#v, want exact %q", operation.Scopes, wantScope)
			}
			if command.ExecutionPolicy == nil || command.ExecutionPolicy.MaxHostRequests != wantRequests {
				t.Fatalf("MaxHostRequests = %#v, want %d", command.ExecutionPolicy, wantRequests)
			}
			generated := operation.HTTP.GeneratedClient
			if generated.MaxRequestBytes != maxRequestBytes || generated.ResponseLimits.MaxMessageBytes != maxResponseBytes || generated.ResponseLimits.MaxMessages != 1 || generated.ResponseLimits.MaxAggregateBytes != maxResponseBytes {
				t.Fatalf("HTTP budgets = %#v", generated)
			}
		})
	}
}
