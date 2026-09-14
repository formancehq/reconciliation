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

// resultProperties describes every known public property the plugin itself emits.
// Unknown properties remain valid so a compatible product response extension
// is not rejected merely because it is not part of the human table projection.
var resultProperties = map[resultFamily]map[string]any{
	resultPolicy: {
		"id": "string", "name": "string", "createdAt": "string", "ledgerName": "string",
		"ledgerQuery": freeObjectSchema(), "paymentsPoolID": "string",
	},
	resultReconciliation: {
		"id": "string", "policyID": "string", "createdAt": "string", "reconciledAtLedger": "string",
		"reconciledAtPayments": "string", "status": "string", "paymentsBalances": typedMapSchema("integer"),
		"ledgerBalances": typedMapSchema("integer"), "driftBalances": typedMapSchema("integer"), "error": "string",
	},
	resultRule: {
		"id": "string", "name": "string", "templateKind": "string", "templateSpec": freeObjectSchema(),
		"explanationCEL": "string", "enabled": "boolean", "severity": "string",
		"periodType": "string", "cadence": "string",
		"schedule": closedObjectSchema(map[string]any{
			"kind": "string", "expr": "string", "tz": "string", "safetyMargin": "string",
		}, []string{"kind"}),
		"notifications": map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
		"labels":        typedMapSchema("string"), "createdAt": "string", "updatedAt": "string",
	},
	resultEvaluation: {
		"id": "string", "ruleID": "string", "startedAt": "string", "endedAt": "string",
		"pitPerSource": typedMapSchema("string"), "result": "string", "evidence": map[string]any{
			"type": []string{"array", "object"}, "items": knownObjectSchema(map[string]any{
				"fingerprint": "string", "passed": "boolean", "proof": typedMapSchema("string"), "evidence": freeObjectSchema(),
			}, nil), "additionalProperties": true,
		},
		"error": "string", "costUnits": "integer", "createdAt": "string",
	},
	resultAlert: {
		"id": "string", "ruleID": "string", "fingerprint": "string", "periodID": "string",
		"status": "string", "severity": "string", "firstSeenAt": "string", "lastSeenAt": "string",
		"occurrenceCount": "integer", "lastEvaluationID": "string", "evidence": freeObjectSchema(),
		"ack": closedObjectSchema(map[string]any{
			"by": "string", "at": "string", "note": "string",
		}, []string{"at", "by"}),
		"resolution": closedObjectSchema(map[string]any{
			"kind": "string", "by": "string", "at": "string", "note": "string",
			"transactionRefs":  map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
			"evidenceSnapshot": freeObjectSchema(),
		}, []string{"at", "by", "kind"}),
		"snooze": closedObjectSchema(map[string]any{
			"until": "string", "by": "string", "at": "string", "note": "string",
		}, []string{"at", "by", "until"}),
		"labels": typedMapSchema("string"), "createdAt": "string", "updatedAt": "string",
	},
	resultAlertEvent: {
		"id": "string", "alertID": "string", "evaluationID": []string{"string", "null"}, "type": "string",
		"prevStatus": []string{"string", "null"}, "newStatus": "string", "payload": freeObjectSchema(),
		"at": "string", "isReopen": "boolean", "notify": "boolean",
	},
}

func freeObjectSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func typedMapSchema(kind string) map[string]any {
	return map[string]any{"type": "object", "additionalProperties": map[string]string{"type": kind}}
}

func knownObjectSchema(properties map[string]any, required []string) map[string]any {
	declared := make(map[string]any, len(properties))
	for name, schema := range properties {
		if nested, ok := schema.(map[string]any); ok {
			declared[name] = nested
		} else {
			declared[name] = map[string]any{"type": schema}
		}
	}
	if required == nil {
		required = []string{}
	}
	sort.Strings(required)
	return map[string]any{
		"type":                 "object",
		"properties":           declared,
		"required":             required,
		"additionalProperties": true,
	}
}

func closedObjectSchema(properties map[string]any, required []string) map[string]any {
	schema := knownObjectSchema(properties, required)
	schema["additionalProperties"] = false
	return schema
}

var optionalResultProperties = map[resultFamily]map[string]bool{
	resultReconciliation: {"error": true},
	resultRule: {
		"explanationCEL": true, "periodType": true, "cadence": true,
		"schedule": true, "notifications": true, "labels": true,
	},
	resultEvaluation: {"pitPerSource": true, "evidence": true, "error": true, "costUnits": true},
	resultAlert:      {"evidence": true, "ack": true, "resolution": true, "snooze": true, "labels": true},
	resultAlertEvent: {"evaluationID": true, "prevStatus": true, "payload": true},
}

func buildOutputSchema(family resultFamily, collection bool) []byte {
	if family == resultEmpty {
		return append([]byte(nil), emptyObjectSchema...)
	}
	properties := map[string]any{}
	required := make([]string, 0, len(resultProperties[family]))
	for name, kind := range resultProperties[family] {
		if schema, ok := kind.(map[string]any); ok {
			properties[name] = schema
		} else {
			properties[name] = map[string]any{"type": kind}
		}
		if !optionalResultProperties[family][name] {
			required = append(required, name)
		}
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
