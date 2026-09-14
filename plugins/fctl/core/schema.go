package core

import (
	"encoding/json"
	"sort"

	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
)

var (
	emptyObjectSchema = []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"type":"object"}`)
)

type resultFamily uint8

const (
	resultEmpty resultFamily = iota
	resultPolicy
	resultReconciliation
	resultRule
	resultEvaluation
	resultAlert
	resultAlertEvent
)

// resultProperties describes the stable public shape the plugin itself emits.
// Unknown properties remain valid so a compatible product response extension
// is not rejected merely because it is not part of the human table projection.
var resultProperties = map[resultFamily]map[string]string{
	resultPolicy: {
		"id": "string", "name": "string", "createdAt": "string", "ledgerName": "string",
		"ledgerQuery": "object", "paymentsPoolID": "string",
	},
	resultReconciliation: {
		"id": "string", "policyID": "string", "createdAt": "string", "reconciledAtLedger": "string",
		"reconciledAtPayments": "string", "status": "string", "paymentsBalances": "object",
		"ledgerBalances": "object", "driftBalances": "object",
	},
	resultRule: {
		"id": "string", "name": "string", "templateKind": "string", "templateSpec": "object",
		"enabled": "boolean", "severity": "string", "cadence": "string", "createdAt": "string", "updatedAt": "string",
	},
	resultEvaluation: {
		"id": "string", "ruleID": "string", "startedAt": "string", "endedAt": "string",
		"result": "string", "createdAt": "string",
	},
	resultAlert: {
		"id": "string", "ruleID": "string", "fingerprint": "string", "periodID": "string",
		"status": "string", "severity": "string", "firstSeenAt": "string", "lastSeenAt": "string",
		"occurrenceCount": "integer", "lastEvaluationID": "string", "createdAt": "string", "updatedAt": "string",
	},
	resultAlertEvent: {
		"id": "string", "alertID": "string", "type": "string", "newStatus": "string",
		"at": "string", "isReopen": "boolean", "notify": "boolean",
	},
}

func buildOutputSchema(family resultFamily, collection bool) []byte {
	if family == resultEmpty {
		return append([]byte(nil), emptyObjectSchema...)
	}
	properties := map[string]any{}
	required := make([]string, 0, len(resultProperties[family]))
	for name, kind := range resultProperties[family] {
		properties[name] = map[string]string{"type": kind}
		required = append(required, name)
	}
	sort.Strings(required)
	item := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": true,
	}
	root := item
	if collection {
		root = map[string]any{"type": "array", "items": item}
	}
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	out, _ := json.Marshal(root)
	return out
}

func buildInputSchema(arguments []sdk.Argument, flags []sdk.Flag) []byte {
	properties := map[string]any{}
	required := []string{}
	for _, arg := range arguments {
		properties[arg.Name] = map[string]string{"type": "string"}
		if arg.Required {
			required = append(required, arg.Name)
		}
	}
	for _, flag := range flags {
		typeName := "string"
		if flag.Type == sdk.FlagInt32 {
			typeName = "integer"
		}
		if flag.Type == sdk.FlagBool {
			typeName = "boolean"
		}
		properties[flag.Name] = map[string]string{"type": typeName}
		if flag.Required {
			required = append(required, flag.Name)
		}
	}
	sort.Strings(required)
	schema := map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	out, _ := json.Marshal(schema)
	return out
}
