// Package component assembles the immutable Reconciliation portable descriptor.
package component

import (
	pb "github.com/formancehq/fctl-v2-poc/pkg/plugin/protocol/componentbridgev1alpha1"
	"github.com/formancehq/reconciliation/plugins/fctl/core"
)

func Descriptor() pb.Descriptor {
	plugin := core.Plugin{}
	return pb.Descriptor{Metadata: plugin.Metadata(), Commands: plugin.Commands(), DocumentationResources: plugin.DocumentationResources()}
}
