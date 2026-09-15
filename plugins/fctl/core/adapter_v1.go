package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk/producthttp"
	reconciliationclient "github.com/formancehq/reconciliation/pkg/client"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

const generatedServerURL = "https://product.invalid"

func executeV1(ctx context.Context, request sdk.ExecuteRequest, host sdk.Host) error {
	command, spec, ok := commandAndSpec(request.CommandID)
	if !ok {
		return failure(sdk.FailureInvalidArgument, "unknown command")
	}
	if err := sdk.ValidateExecuteRequest(command, request); err != nil {
		return failure(sdk.FailureInvalidArgument, "invalid execution request: "+err.Error())
	}
	if err := sdk.ValidateTargetSelection(command.Target, request.Target); err != nil {
		return failure(sdk.FailureInvalidArgument, "invalid target: "+err.Error())
	}
	if len(request.Arguments) != len(spec.pathArguments) {
		return failure(sdk.FailureInvalidArgument, "invalid argument count")
	}
	flags, err := collectFlags(command, request.Flags)
	if err != nil {
		return err
	}
	body, err := requestBody(spec, flags)
	if err != nil {
		return err
	}
	bound, err := producthttp.New(host, command.Operations[0], "auth.stack")
	if err != nil {
		return fmt.Errorf("reconciliation: configure HTTP adapter: %w", err)
	}
	// Endpoint resolution, credentials, retries, and transport remain host-owned.
	// The generated client owns only request/response serialization.
	generated := reconciliationclient.New(generatedServerURL, reconciliationclient.WithClient(bound))
	v1 := generated.Reconciliation.V1
	arg := func(index int) string { return request.Arguments[index] }

	switch request.CommandID {
	case "reconciliation.v1.policies.create":
		payload, err := decodeBody[components.PolicyRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.CreatePolicy(ctx, payload)
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetPolicyResponse() *components.PolicyResponse
		}) any {
			if wrapper := value.GetPolicyResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.policies.list":
		return executeGeneratedPages(ctx, host, command.ID, flags, request.Continuation, func(size *int64, cursor, query *string) (generatedPage[components.Policy], error) {
			response, err := v1.ListPolicies(ctx, size, cursor, query)
			if err != nil {
				return generatedPage[components.Policy]{}, err
			}
			if response == nil || response.PoliciesCursorResponse == nil {
				return generatedPage[components.Policy]{}, malformedResponse(spec.operationID)
			}
			page := response.PoliciesCursorResponse.Cursor
			return generatedPage[components.Policy]{Data: page.Data, Next: page.Next, HasMore: page.HasMore}, nil
		})
	case "reconciliation.v1.policies.get":
		response, err := v1.GetPolicy(ctx, arg(0))
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetPolicyResponse() *components.PolicyResponse
		}) any {
			if wrapper := value.GetPolicyResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.policies.delete":
		response, err := v1.DeletePolicy(ctx, arg(0))
		return emitDelete(host, command.ID, spec.operationID, response, err)
	case "reconciliation.v1.policies.reconcile":
		payload, err := decodeBody[components.ReconciliationRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.Reconcile(ctx, arg(0), payload)
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetReconciliationResponse() *components.ReconciliationResponse
		}) any {
			if wrapper := value.GetReconciliationResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.list":
		return executeGeneratedPages(ctx, host, command.ID, flags, request.Continuation, func(size *int64, cursor, query *string) (generatedPage[components.Reconciliation], error) {
			response, err := v1.ListReconciliations(ctx, size, cursor, query)
			if err != nil {
				return generatedPage[components.Reconciliation]{}, err
			}
			if response == nil || response.ReconciliationsCursorResponse == nil {
				return generatedPage[components.Reconciliation]{}, malformedResponse(spec.operationID)
			}
			page := response.ReconciliationsCursorResponse.Cursor
			return generatedPage[components.Reconciliation]{Data: page.Data, Next: page.Next, HasMore: page.HasMore}, nil
		})
	case "reconciliation.v1.get":
		response, err := v1.GetReconciliation(ctx, arg(0))
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetReconciliationResponse() *components.ReconciliationResponse
		}) any {
			if wrapper := value.GetReconciliationResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.rules.create":
		payload, err := decodeBody[components.RuleRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.CreateRule(ctx, payload)
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetRuleResponse() *components.RuleResponse
		}) any {
			if wrapper := value.GetRuleResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.rules.list":
		return executeGeneratedPages(ctx, host, command.ID, flags, request.Continuation, func(size *int64, cursor, query *string) (generatedPage[components.Rule], error) {
			response, err := v1.ListRules(ctx, size, cursor, query)
			if err != nil {
				return generatedPage[components.Rule]{}, err
			}
			if response == nil || response.RulesCursorResponse == nil {
				return generatedPage[components.Rule]{}, malformedResponse(spec.operationID)
			}
			page := response.RulesCursorResponse.Cursor
			return generatedPage[components.Rule]{Data: page.Data, Next: page.Next, HasMore: page.HasMore}, nil
		})
	case "reconciliation.v1.rules.get":
		response, err := v1.GetRule(ctx, arg(0))
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetRuleResponse() *components.RuleResponse
		}) any {
			if wrapper := value.GetRuleResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.rules.update":
		payload, err := decodeBody[components.RulePatchRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.PatchRule(ctx, arg(0), payload)
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetRuleResponse() *components.RuleResponse
		}) any {
			if wrapper := value.GetRuleResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.rules.delete":
		response, err := v1.DeleteRule(ctx, arg(0))
		return emitDelete(host, command.ID, spec.operationID, response, err)
	case "reconciliation.v1.rules.evaluate":
		payload, err := decodeOptionalBody[components.EvaluateRuleRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.EvaluateRule(ctx, arg(0), payload)
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetEvaluationResponse() *components.EvaluationResponse
		}) any {
			if wrapper := value.GetEvaluationResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.evaluations.list":
		return executeGeneratedPages(ctx, host, command.ID, flags, request.Continuation, func(size *int64, cursor, query *string) (generatedPage[components.Evaluation], error) {
			response, err := v1.ListEvaluations(ctx, size, cursor, query)
			if err != nil {
				return generatedPage[components.Evaluation]{}, err
			}
			if response == nil || response.EvaluationsCursorResponse == nil {
				return generatedPage[components.Evaluation]{}, malformedResponse(spec.operationID)
			}
			page := response.EvaluationsCursorResponse.Cursor
			return generatedPage[components.Evaluation]{Data: page.Data, Next: page.Next, HasMore: page.HasMore}, nil
		})
	case "reconciliation.v1.evaluations.get":
		response, err := v1.GetEvaluation(ctx, arg(0))
		return emitObject(host, command.ID, spec.operationID, response, err, func(value interface {
			GetEvaluationResponse() *components.EvaluationResponse
		}) any {
			if wrapper := value.GetEvaluationResponse(); wrapper != nil {
				return wrapper.Data
			}
			return nil
		})
	case "reconciliation.v1.alerts.list":
		return executeGeneratedPages(ctx, host, command.ID, flags, request.Continuation, func(size *int64, cursor, query *string) (generatedPage[components.Alert], error) {
			response, err := v1.ListAlerts(ctx, size, cursor, query)
			if err != nil {
				return generatedPage[components.Alert]{}, err
			}
			if response == nil || response.AlertsCursorResponse == nil {
				return generatedPage[components.Alert]{}, malformedResponse(spec.operationID)
			}
			page := response.AlertsCursorResponse.Cursor
			return generatedPage[components.Alert]{Data: page.Data, Next: page.Next, HasMore: page.HasMore}, nil
		})
	case "reconciliation.v1.alerts.get":
		response, err := v1.GetAlert(ctx, arg(0))
		return emitAlert(host, command.ID, spec.operationID, response, err)
	case "reconciliation.v1.alerts.events":
		return executeGeneratedPages(ctx, host, command.ID, flags, request.Continuation, func(size *int64, cursor, _ *string) (generatedPage[components.AlertEvent], error) {
			response, err := v1.ListAlertEvents(ctx, arg(0), size, cursor)
			if err != nil {
				return generatedPage[components.AlertEvent]{}, err
			}
			if response == nil || response.AlertEventsCursorResponse == nil {
				return generatedPage[components.AlertEvent]{}, malformedResponse(spec.operationID)
			}
			page := response.AlertEventsCursorResponse.Cursor
			return generatedPage[components.AlertEvent]{Data: page.Data, Next: page.Next, HasMore: page.HasMore}, nil
		})
	case "reconciliation.v1.alerts.ack":
		payload, err := decodeBody[components.AckAlertRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.AckAlert(ctx, arg(0), payload)
		return emitAlert(host, command.ID, spec.operationID, response, err)
	case "reconciliation.v1.alerts.resolve":
		payload, err := decodeBody[components.ResolveAlertRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.ResolveAlert(ctx, arg(0), payload)
		return emitAlert(host, command.ID, spec.operationID, response, err)
	case "reconciliation.v1.alerts.accept":
		payload, err := decodeBody[components.AcceptAlertRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.AcceptAlert(ctx, arg(0), payload)
		return emitAlert(host, command.ID, spec.operationID, response, err)
	case "reconciliation.v1.alerts.snooze":
		payload, err := decodeBody[components.SnoozeAlertRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.SnoozeAlert(ctx, arg(0), payload)
		return emitAlert(host, command.ID, spec.operationID, response, err)
	case "reconciliation.v1.alerts.unsnooze":
		payload, err := decodeBody[components.UnsnoozeAlertRequest](body)
		if err != nil {
			return err
		}
		response, err := v1.UnsnoozeAlert(ctx, arg(0), payload)
		return emitAlert(host, command.ID, spec.operationID, response, err)
	default:
		return failure(sdk.FailureInvalidArgument, "unknown command")
	}
}

type generatedPage[T any] struct {
	Data    []T
	Next    *string
	HasMore bool
}

func executeGeneratedPages[T any](ctx context.Context, host sdk.Host, commandID string, flags map[string][]string, control sdk.ContinuationControl, fetch func(*int64, *string, *string) (generatedPage[T], error)) error {
	var pageSize *int64
	if value := first(flags["page-size"]); value != "" {
		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil || size < 1 || size > 100 {
			return failure(sdk.FailureInvalidArgument, "page-size must be between 1 and 100")
		}
		pageSize = &size
	}
	var query *string
	if value := first(flags["query"]); value != "" {
		query = &value
	}
	var cursor *string
	if value := first(flags["cursor"]); value != "" {
		cursor = &value
	}
	all := control.Mode == sdk.ContinuationAllPages
	seen := map[string]struct{}{}
	items := make([]T, 0)
	// The aggregate is emitted as one JSON array: count both brackets up front,
	// then every encoded item and the commas between items.
	aggregateBytes := uint64(2)
	if all && aggregateBytes > control.MaxBytes {
		return failure(sdk.FailurePaginationLimitExceeded, "pagination aggregate limit exceeded")
	}
	for pageNumber := uint32(0); ; pageNumber++ {
		if pageNumber >= commandMaxPages(control) {
			return failure(sdk.FailurePaginationLimitExceeded, "pagination page limit exceeded")
		}
		if cursor != nil {
			if _, duplicate := seen[*cursor]; duplicate {
				return failure(sdk.FailureProductResponseFailed, "product repeated a pagination cursor")
			}
			seen[*cursor] = struct{}{}
		}
		page, err := fetch(pageSize, cursor, query)
		if err != nil {
			return fmt.Errorf("reconciliation: generated request: %w", err)
		}
		if page.HasMore != (page.Next != nil && *page.Next != "") {
			return failure(sdk.FailureProductResponseFailed, "product returned incoherent pagination cursor metadata")
		}
		if !all {
			return emitJSON(host, commandID, sdk.ResultCollection, page.Data, &sdk.PageInfo{NextCursor: dereference(page.Next), HasMore: page.HasMore})
		}
		for _, item := range page.Data {
			if uint32(len(items)+1) > control.MaxItems {
				return failure(sdk.FailurePaginationLimitExceeded, "pagination aggregate limit exceeded")
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				return fmt.Errorf("reconciliation: marshal generated result: %w", err)
			}
			separatorBytes := uint64(0)
			if len(items) > 0 {
				separatorBytes = 1
			}
			if aggregateBytes+separatorBytes+uint64(len(encoded)) > control.MaxBytes {
				return failure(sdk.FailurePaginationLimitExceeded, "pagination aggregate limit exceeded")
			}
			aggregateBytes += separatorBytes + uint64(len(encoded))
			items = append(items, item)
		}
		if !page.HasMore {
			return emitJSON(host, commandID, sdk.ResultCollection, items, nil)
		}
		cursor, pageSize, query = page.Next, nil, nil
	}
}

func emitObject[T, V any](host sdk.Host, commandID, operationID string, response T, err error, data func(V) any) error {
	if err != nil {
		return fmt.Errorf("reconciliation: %s: %w", operationID, err)
	}
	typed, ok := any(response).(V)
	if !ok {
		return malformedResponse(operationID)
	}
	value := data(typed)
	if value == nil {
		return malformedResponse(operationID)
	}
	return emitJSON(host, commandID, sdk.ResultObject, value, nil)
}

func emitAlert[T interface {
	GetAlertResponse() *components.AlertResponse
}](host sdk.Host, commandID, operationID string, response T, err error) error {
	return emitObject(host, commandID, operationID, response, err, func(value T) any {
		if wrapper := value.GetAlertResponse(); wrapper != nil {
			return wrapper.Data
		}
		return nil
	})
}

func emitDelete[T interface {
	GetHTTPMeta() components.HTTPMetadata
}](host sdk.Host, commandID, operationID string, response T, err error) error {
	if err != nil {
		return fmt.Errorf("reconciliation: %s: %w", operationID, err)
	}
	if response.GetHTTPMeta().Response == nil || response.GetHTTPMeta().Response.StatusCode != 204 {
		return malformedResponse(operationID)
	}
	return emitJSON(host, commandID, sdk.ResultObject, struct{}{}, nil)
}

func emitJSON(host sdk.Host, operation string, shape sdk.ResultShape, value any, page *sdk.PageInfo) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("reconciliation: marshal generated result: %w", err)
	}
	return host.Emit(sdk.Event{Kind: sdk.EventResult, Result: &sdk.ResultEnvelope{OperationID: operation, Shape: shape, MediaType: "application/json", Data: data, Page: page}})
}

func decodeBody[T any](body []byte) (T, error) {
	var value T
	if err := decodeJSONLossless(body, &value); err != nil {
		return value, failure(sdk.FailureInvalidArgument, "body does not match the generated request schema")
	}
	return value, nil
}

func decodeJSONLossless(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func decodeOptionalBody[T any](body []byte) (*T, error) {
	if len(body) == 0 {
		return nil, nil
	}
	value, err := decodeBody[T](body)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func requestBody(spec commandSpec, flags map[string][]string) ([]byte, error) {
	name := "body"
	if spec.body == bodyQuery {
		name = "query"
	}
	value := first(flags[name])
	if value == "" {
		if spec.body == bodyRequired {
			return nil, failure(sdk.FailureInvalidArgument, name+" is required")
		}
		return nil, nil
	}
	if !json.Valid([]byte(value)) {
		return nil, failure(sdk.FailureInvalidArgument, name+" must be valid JSON")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &object) != nil || object == nil {
		return nil, failure(sdk.FailureInvalidArgument, name+" must be a JSON object")
	}
	return []byte(value), nil
}

func collectFlags(command sdk.Command, occurrences []sdk.FlagOccurrence) (map[string][]string, error) {
	declared := map[string]sdk.Flag{}
	for _, value := range command.Flags {
		declared[value.Name] = value
	}
	out := map[string][]string{}
	for _, occurrence := range occurrences {
		flag, ok := declared[occurrence.Name]
		if !ok {
			return nil, failure(sdk.FailureInvalidArgument, "unknown flag "+occurrence.Name)
		}
		if flag.Type != sdk.FlagStringArray && len(out[occurrence.Name]) > 0 {
			return nil, failure(sdk.FailureInvalidArgument, "flag repeated: "+occurrence.Name)
		}
		out[occurrence.Name] = append(out[occurrence.Name], occurrence.Value)
	}
	for _, flag := range command.Flags {
		if flag.Required && len(out[flag.Name]) == 0 {
			return nil, failure(sdk.FailureInvalidArgument, "missing required flag "+flag.Name)
		}
	}
	return out, nil
}

func commandMaxPages(control sdk.ContinuationControl) uint32 {
	if control.Mode == sdk.ContinuationAllPages {
		return control.MaxPages
	}
	return 1
}

func malformedResponse(operationID string) error {
	return failure(sdk.FailureProductResponseFailed, "product returned malformed "+operationID+" response")
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func commandAndSpec(id string) (sdk.Command, commandSpec, bool) {
	commands, specs := Catalogue(), catalogueSpecs()
	for index := range commands {
		if commands[index].ID == id {
			return commands[index], specs[index], true
		}
	}
	return sdk.Command{}, commandSpec{}, false
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func failure(code sdk.FailureCode, message string) error {
	return sdk.Failure{Code: string(code), Message: message}
}
