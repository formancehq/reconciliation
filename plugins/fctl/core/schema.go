package core

import (
	"encoding/json"
	"sort"

	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
)

var (
	objectSchema     = []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	collectionSchema = []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"array"}`)
)

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
