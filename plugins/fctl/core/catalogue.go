package core

import (
	"strings"

	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
)

const (
	productMajor     uint32 = 1
	maxRequestBytes  int64  = 256 << 10
	maxResponseBytes int64  = 512 << 10
)

type commandSpec struct {
	path                              []string
	aliases                           [][]string
	summary                           string
	operationID, method, route, scope string
	pathArguments                     []string
	body                              bodyMode
	paginated                         bool
}

type bodyMode uint8

const (
	bodyNone bodyMode = iota
	bodyOptional
	bodyRequired
	bodyQuery
)

func Catalogue() []sdk.Command {
	specs := catalogueSpecs()
	out := make([]sdk.Command, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.command())
	}
	return out
}

func (spec commandSpec) command() sdk.Command {
	args := make([]sdk.Argument, 0, len(spec.pathArguments))
	for _, name := range spec.pathArguments {
		args = append(args, sdk.Argument{Name: name, Usage: strings.ReplaceAll(name, "-", " "), Type: sdk.ArgumentString, Required: true, Completion: sdk.CompletionSpec{Kind: sdk.CompletionNone}})
	}
	flags := []sdk.Flag{}
	if spec.body != bodyNone {
		name, usage := "body", "JSON request body"
		if spec.body == bodyQuery {
			name, usage = "query", "JSON QueryBuilder filter"
		}
		flags = append(flags, sdk.Flag{Name: name, Usage: usage, Type: sdk.FlagString, Required: spec.body == bodyRequired, Completion: sdk.CompletionSpec{Kind: sdk.CompletionNone}})
	}
	if spec.paginated {
		flags = append(flags,
			sdk.Flag{Name: "cursor", Usage: "Opaque page cursor", Type: sdk.FlagString, Completion: sdk.CompletionSpec{Kind: sdk.CompletionNone}},
			sdk.Flag{Name: "page-size", Usage: "Page size (1-100)", Type: sdk.FlagInt32, Completion: sdk.CompletionSpec{Kind: sdk.CompletionNone}},
		)
	}
	requestContentTypes := []string(nil)
	if spec.body == bodyOptional || spec.body == bodyRequired {
		requestContentTypes = []string{"application/json"}
	}
	risk := sdk.RiskRead
	if spec.method != "GET" {
		risk = sdk.RiskMutation
	}
	maxRequests := uint32(1)
	output := objectSchema
	if spec.paginated {
		maxRequests, output = sdk.DefaultAllPagesMaxPages, collectionSchema
	}
	return sdk.Command{
		ID: "reconciliation.v1." + strings.Join(spec.path, "."), ExecutionKind: sdk.ExecutionKindService,
		AuthMode: sdk.AuthModeCapability, Path: append([]string(nil), spec.path...), PathAliases: clonePaths(spec.aliases),
		Target: sdk.TargetRequirement{Kind: sdk.TargetStack}, Summary: spec.summary,
		Long: spec.summary + ". The fctl host supplies endpoint, credentials and transport.", Example: strings.Join(spec.path, " ") + " --help",
		Arguments: args, Flags: flags, Auth: []sdk.AuthRequirement{{Capability: "auth.stack"}},
		Operations: []sdk.OperationPolicy{{ID: spec.operationID, Service: sdk.ServiceReconciliation, Scopes: []string{spec.scope}, HTTP: &sdk.HTTPOperationPolicy{Method: spec.method, GeneratedClient: &sdk.HTTPGeneratedClientPolicy{
			PathTemplate: spec.route, RequestContentTypes: requestContentTypes, RequestHeaders: []string{"Accept"}, MaxRequestBytes: maxRequestBytes,
			ResponseLimits: sdk.ResponseLimits{MaxMessageBytes: maxResponseBytes, MaxMessages: 1, MaxAggregateBytes: maxResponseBytes},
		}}}},
		Compatibility: []sdk.ServiceCompatibility{{Service: sdk.ServiceReconciliation, Majors: []uint32{productMajor}}},
		Risk:          risk, InputSchema: buildInputSchema(args, flags), RawOutputSchema: output, PublicOutputSchema: output,
		Pagination: sdk.PaginationSpec{Supported: spec.paginated}, OutputMediaType: "application/json",
		ExecutionPolicy: &sdk.CommandExecutionPolicy{MaxHostRequests: maxRequests},
	}
}

func catalogueSpecs() []commandSpec {
	r, w := "reconciliation:read", "reconciliation:write"
	return []commandSpec{
		{[]string{"policies", "create"}, [][]string{{"p"}, {"cr", "c"}}, "Create a policy", "createPolicy", "POST", "/policies", w, nil, bodyRequired, false},
		{[]string{"policies", "list"}, [][]string{{"p"}, {"ls", "l"}}, "List policies", "listPolicies", "GET", "/policies", r, nil, bodyQuery, true},
		{[]string{"policies", "get"}, [][]string{{"p"}, {"sh", "s"}}, "Get a policy", "getPolicy", "GET", "/policies/{policyID}", r, []string{"policy-id"}, bodyNone, false},
		{[]string{"policies", "delete"}, [][]string{{"p"}, {"d"}}, "Delete a policy", "deletePolicy", "DELETE", "/policies/{policyID}", w, []string{"policy-id"}, bodyNone, false},
		{[]string{"policies", "reconcile"}, [][]string{{"p"}, {"r"}}, "Reconcile using a policy", "reconcile", "POST", "/policies/{policyID}/reconciliation", w, []string{"policy-id"}, bodyRequired, false},
		{[]string{"list"}, [][]string{{"ls", "l"}}, "List reconciliations", "listReconciliations", "GET", "/reconciliations", r, nil, bodyQuery, true},
		{[]string{"get"}, [][]string{{"sh", "s"}}, "Get a reconciliation", "getReconciliation", "GET", "/reconciliations/{reconciliationID}", r, []string{"reconciliation-id"}, bodyNone, false},
		{[]string{"rules", "create"}, nil, "Create a rule", "createRule", "POST", "/rules", w, nil, bodyRequired, false},
		{[]string{"rules", "list"}, nil, "List rules", "listRules", "GET", "/rules", r, nil, bodyQuery, true},
		{[]string{"rules", "get"}, nil, "Get a rule", "getRule", "GET", "/rules/{ruleID}", r, []string{"rule-id"}, bodyNone, false},
		{[]string{"rules", "update"}, nil, "Patch a rule", "patchRule", "PATCH", "/rules/{ruleID}", w, []string{"rule-id"}, bodyRequired, false},
		{[]string{"rules", "delete"}, nil, "Delete a rule", "deleteRule", "DELETE", "/rules/{ruleID}", w, []string{"rule-id"}, bodyNone, false},
		{[]string{"rules", "evaluate"}, nil, "Evaluate a rule now", "evaluateRule", "POST", "/rules/{ruleID}/evaluate", w, []string{"rule-id"}, bodyOptional, false},
		{[]string{"evaluations", "list"}, nil, "List evaluations", "listEvaluations", "GET", "/evaluations", r, nil, bodyQuery, true},
		{[]string{"evaluations", "get"}, nil, "Get an evaluation", "getEvaluation", "GET", "/evaluations/{evaluationID}", r, []string{"evaluation-id"}, bodyNone, false},
		{[]string{"alerts", "list"}, nil, "List alerts", "listAlerts", "GET", "/alerts", r, nil, bodyQuery, true},
		{[]string{"alerts", "get"}, nil, "Get an alert", "getAlert", "GET", "/alerts/{alertID}", r, []string{"alert-id"}, bodyNone, false},
		{[]string{"alerts", "events"}, nil, "List alert events", "listAlertEvents", "GET", "/alerts/{alertID}/events", r, []string{"alert-id"}, bodyNone, true},
		{[]string{"alerts", "ack"}, nil, "Acknowledge an alert", "ackAlert", "POST", "/alerts/{alertID}/ack", w, []string{"alert-id"}, bodyRequired, false},
		{[]string{"alerts", "resolve"}, nil, "Resolve an alert", "resolveAlert", "POST", "/alerts/{alertID}/resolve", w, []string{"alert-id"}, bodyRequired, false},
		{[]string{"alerts", "accept"}, nil, "Accept an alert", "acceptAlert", "POST", "/alerts/{alertID}/accept", w, []string{"alert-id"}, bodyRequired, false},
		{[]string{"alerts", "snooze"}, nil, "Snooze alert notifications", "snoozeAlert", "POST", "/alerts/{alertID}/snooze", w, []string{"alert-id"}, bodyRequired, false},
		{[]string{"alerts", "unsnooze"}, nil, "Lift an alert snooze", "unsnoozeAlert", "POST", "/alerts/{alertID}/unsnooze", w, []string{"alert-id"}, bodyRequired, false},
	}
}

func clonePaths(in [][]string) [][]string {
	out := make([][]string, len(in))
	for i := range in {
		out[i] = append([]string(nil), in[i]...)
	}
	return out
}
