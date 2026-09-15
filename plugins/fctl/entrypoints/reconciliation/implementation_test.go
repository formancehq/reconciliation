//go:build fctl_component_guest

package export_formance_fctl_plugin_lifecycle

import (
	"testing"

	pb "github.com/formancehq/fctl-v2-poc/pkg/plugin/protocol/componentbridgev1alpha1"
	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
	"google.golang.org/protobuf/proto"
)

func TestDescribeExportsAllReconciliationCommands(t *testing.T) {
	descriptor, err := pb.DecodeDescriptorEnvelope(Describe())
	if err != nil {
		t.Fatalf("decode descriptor: %v", err)
	}
	if descriptor.Metadata.Name != "reconciliation" || len(descriptor.Commands) != 23 {
		t.Fatalf("descriptor identity/commands = %q/%d", descriptor.Metadata.Name, len(descriptor.Commands))
	}
}

func TestLifecycleExecutesACommandAcrossStartResumeAndClose(t *testing.T) {
	executionID := "reconciliation-lifecycle-success"
	t.Cleanup(func() { Close(executionID) })
	start, err := pb.CommandStartFromSDK(sdk.ExecuteRequest{
		CommandID:       "reconciliation.v1.policies.get",
		Arguments:       []string{"policy-1"},
		Target:          sdk.TargetSelection{OrganizationID: "org", StackID: "stack"},
		ServiceVersions: []sdk.ServiceVersion{{Service: sdk.ServiceReconciliation, Version: "1.0.0", Major: 1}},
		Continuation:    sdk.SinglePageContinuationControl(),
	})
	if err != nil {
		t.Fatal(err)
	}

	frames := Start(executionID, lifecycleEnvelope(t, executionID, pb.MessageKind_MESSAGE_KIND_START_EXECUTION, &pb.StartPayload{
		Start: &pb.StartPayload_Command{Command: start},
	}))
	if len(frames) != 1 {
		t.Fatalf("Start frames = %d, want one host request", len(frames))
	}
	hostRequestEnvelope := decodeLifecycleEnvelope(t, frames[0], pb.MessageKind_MESSAGE_KIND_HOST_REQUEST)
	var hostRequest pb.HostRequestPayload
	decodeLifecyclePayload(t, hostRequestEnvelope, &hostRequest)
	product := hostRequest.GetProduct()
	if product.GetOperationId() != "getPolicy" || product.GetService() != pb.Service_SERVICE_RECONCILIATION || product.GetHttp().GetMethod() != "GET" || product.GetHttp().GetPath() != "/policies/policy-1" {
		t.Fatalf("host request = %#v", product)
	}

	frames = Resume(executionID, lifecycleEnvelope(t, executionID, pb.MessageKind_MESSAGE_KIND_HOST_RESPONSE, &pb.HostResponsePayload{
		CorrelationId: hostRequest.GetCorrelationId(),
		Response: &pb.HostResponsePayload_Product{Product: &pb.ProductResponse{
			Status: 200, ContentType: "application/json", Body: []byte(`{"data":{"id":"policy-1"}}`),
		}},
	}))
	if len(frames) != 2 {
		t.Fatalf("Resume frames = %d, want result and termination", len(frames))
	}
	decodeLifecycleEnvelope(t, frames[0], pb.MessageKind_MESSAGE_KIND_EVENT)
	terminalEnvelope := decodeLifecycleEnvelope(t, frames[1], pb.MessageKind_MESSAGE_KIND_TERMINATION)
	var terminal pb.TerminationPayload
	decodeLifecyclePayload(t, terminalEnvelope, &terminal)
	if !terminal.GetSuccess() {
		t.Fatalf("terminal = %#v, want success", &terminal)
	}

	Close(executionID)
	if frames := Resume(executionID, nil); len(frames) != 0 {
		t.Fatalf("Resume after Close frames = %d, want none", len(frames))
	}
}

func TestLifecycleCancelTerminatesWithoutEmittingAResult(t *testing.T) {
	executionID := "reconciliation-lifecycle-cancel"
	t.Cleanup(func() { Close(executionID) })
	start, err := pb.CommandStartFromSDK(sdk.ExecuteRequest{
		CommandID:       "reconciliation.v1.policies.get",
		Arguments:       []string{"policy-1"},
		Target:          sdk.TargetSelection{OrganizationID: "org", StackID: "stack"},
		ServiceVersions: []sdk.ServiceVersion{{Service: sdk.ServiceReconciliation, Version: "1.0.0", Major: 1}},
		Continuation:    sdk.SinglePageContinuationControl(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if frames := Start(executionID, lifecycleEnvelope(t, executionID, pb.MessageKind_MESSAGE_KIND_START_EXECUTION, &pb.StartPayload{Start: &pb.StartPayload_Command{Command: start}})); len(frames) != 1 {
		t.Fatalf("Start frames = %d, want one host request", len(frames))
	}
	frames := Cancel(executionID)
	if len(frames) != 1 {
		t.Fatalf("Cancel frames = %d, want one termination", len(frames))
	}
	terminalEnvelope := decodeLifecycleEnvelope(t, frames[0], pb.MessageKind_MESSAGE_KIND_TERMINATION)
	var terminal pb.TerminationPayload
	decodeLifecyclePayload(t, terminalEnvelope, &terminal)
	if got := terminal.GetFailure().GetCode(); got != string(sdk.FailureCanceled) {
		t.Fatalf("cancel failure = %q, want %q", got, sdk.FailureCanceled)
	}
}

func lifecycleEnvelope(t *testing.T, executionID string, kind pb.MessageKind, payload proto.Message) []byte {
	t.Helper()
	payloadBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(&pb.PluginEnvelope{
		ProtocolMajor: pb.ProtocolMajor,
		FacetKind:     pb.FacetKind_FACET_KIND_COMMAND_PROVIDER,
		MessageKind:   kind,
		ExecutionId:   executionID,
		Payload:       payloadBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func decodeLifecycleEnvelope(t *testing.T, encoded []byte, kind pb.MessageKind) *pb.PluginEnvelope {
	t.Helper()
	var envelope pb.PluginEnvelope
	if err := proto.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.GetProtocolMajor() != pb.ProtocolMajor || envelope.GetFacetKind() != pb.FacetKind_FACET_KIND_COMMAND_PROVIDER || envelope.GetMessageKind() != kind {
		t.Fatalf("envelope = %#v, want command %s", &envelope, kind)
	}
	return &envelope
}

func decodeLifecyclePayload(t *testing.T, envelope *pb.PluginEnvelope, payload proto.Message) {
	t.Helper()
	if err := proto.Unmarshal(envelope.GetPayload(), payload); err != nil {
		t.Fatal(err)
	}
}
