package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// oasSchemas loads components.schemas from the checked-in spec.
func oasSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi.yaml"))
	require.NoError(t, err, "openapi.yaml must be readable from internal/api")

	var doc struct {
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Components.Schemas)
	return doc.Components.Schemas
}

// TestOpenAPI_PeriodTypeAliasIsSDKSafe guards the three ways this deprecation
// has already broken generated SDKs. Every assertion here corresponds to a real
// regression found by regenerating the Go SDK with Speakeasy, not to a
// hypothetical — so please read the reasons before relaxing any of them.
//
// This cannot run Speakeasy, so it guards the spec shape that generation
// depends on rather than the generated output. The properties it pins are
// exactly the ones whose innocuous-looking edits produced compile or runtime
// breaks.
func TestOpenAPI_PeriodTypeAliasIsSDKSafe(t *testing.T) {
	t.Parallel()
	s := oasSchemas(t)

	for _, name := range []string{"PeriodType", "Cadence"} {
		schema, ok := s[name]
		require.True(t, ok, "%s component must exist: dropping it deletes the generated type and its constants", name)

		// Speakeasy materializes a schema `default` into the serialized body,
		// so a nil field is sent as "continuous" rather than omitted. With a
		// default on both of these, setting only periodType also transmits
		// cadence, the two disagree, and every non-continuous rule creation is
		// rejected with 400. The server owns the default instead.
		require.NotContains(t, schema, "default",
			"%s must not declare a schema default: generators serialize it, which sends both keys and trips the conflict check", name)

		require.Equal(t,
			[]any{"continuous", "daily", "weekly", "monthly"}, schema["enum"],
			"%s enum must stay in lock-step with models.PeriodType", name)
	}

	require.Equal(t, true, s["Cadence"]["deprecated"],
		"the Cadence component carries the deprecation, since its properties are bare $refs and cannot")

	// A property-level `allOf` (even just to attach a description) makes
	// Speakeasy synthesise a per-property type — RuleRequestCadence instead of
	// Cadence — so existing code assigning CadenceMonthly.ToPointer() stops
	// compiling. Bare $ref reuses the named type. Prose belongs on the
	// component, which is why those descriptions live there.
	for _, tc := range []struct{ schema, property, want string }{
		{"Rule", "periodType", "#/components/schemas/PeriodType"},
		{"Rule", "cadence", "#/components/schemas/Cadence"},
		{"RuleRequest", "periodType", "#/components/schemas/PeriodType"},
		{"RuleRequest", "cadence", "#/components/schemas/Cadence"},
	} {
		props, ok := s[tc.schema]["properties"].(map[string]any)
		require.True(t, ok, "%s must have properties", tc.schema)
		prop, ok := props[tc.property].(map[string]any)
		require.True(t, ok, "%s.%s must exist", tc.schema, tc.property)

		require.Equal(t, []string{"$ref"}, keysOf(prop),
			"%s.%s must be a bare $ref: any sibling key forces an allOf wrapper, which makes the generator mint a per-property type and break existing assignments",
			tc.schema, tc.property)
		require.Equal(t, tc.want, prop["$ref"])
	}

	required := s["Rule"]["required"].([]any)
	require.Contains(t, required, "cadence",
		"cadence has been required since 2.4.0; dropping it breaks clients generated before 2.5.0")
	require.NotContains(t, required, "periodType",
		"periodType stays optional during the deprecation window: making it required forces every consumer that constructs a Rule (fixtures, models) to change source in a minor release. Make it required at the next API major, when cadence goes")
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
