package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/formancehq/fctl-v2-poc/pkg/plugin/sdk"
	"github.com/formancehq/reconciliation/pkg/client/models/components"
)

// renderCase is one command that declares a table hint, together with the
// generated type its public result decodes from and a fixture carrying exactly
// that type's always-present properties. The fixture is deliberately minimal:
// a column is only truthful if it resolves against a result that omits every
// optional property.
type renderCase struct {
	commandID   string
	columns     []sdk.TableColumn
	entity      any
	fixture     string
	fullFixture string
}

const (
	policyFixture = `{"id":"pol-1","name":"nightly ledger vs pool","createdAt":"2026-09-14T08:00:00Z",` +
		`"ledgerName":"main","ledgerQuery":{"$match":{"account":"world"}},"paymentsPoolID":"pool-1"}`
	reconciliationFixture = `{"id":"rec-1","policyID":"pol-1","createdAt":"2026-09-14T08:00:00Z",` +
		`"reconciledAtLedger":"2026-09-14T07:00:00Z","reconciledAtPayments":"2026-09-14T07:00:00Z",` +
		`"status":"OK","paymentsBalances":{"USD":100},"ledgerBalances":{"USD":100},"driftBalances":{"USD":0}}`
	ruleFixture = `{"id":"rule-1","name":"pool drift","templateKind":"ledger_vs_pool_drift",` +
		`"templateSpec":{"ledgerName":"main"},"enabled":true,"severity":"high","cadence":"daily",` +
		`"createdAt":"2026-09-14T08:00:00Z","updatedAt":"2026-09-14T09:00:00Z"}`
	evaluationFixture = `{"id":"eval-1","ruleID":"rule-1","startedAt":"2026-09-14T08:00:00Z",` +
		`"endedAt":"2026-09-14T08:00:05Z","result":"FAIL","createdAt":"2026-09-14T08:00:05Z"}`
	alertFixture = `{"id":"alert-1","ruleID":"rule-1","fingerprint":"9f1c","periodID":"2026-09",` +
		`"status":"OPEN","severity":"high","firstSeenAt":"2026-09-14T08:00:00Z",` +
		`"lastSeenAt":"2026-09-14T09:00:00Z","occurrenceCount":3,"lastEvaluationID":"eval-1",` +
		`"createdAt":"2026-09-14T08:00:00Z","updatedAt":"2026-09-14T09:00:00Z"}`
	alertEventFixture = `{"id":"event-1","alertID":"alert-1","type":"fail","newStatus":"OPEN",` +
		`"at":"2026-09-14T09:00:00Z","isReopen":false,"notify":true}`
	fullReconciliationFixture = `{"id":"rec-1","policyID":"pol-1","createdAt":"2026-09-14T08:00:00Z",` +
		`"reconciledAtLedger":"2026-09-14T07:00:00Z","reconciledAtPayments":"2026-09-14T07:00:00Z",` +
		`"status":"ERROR","paymentsBalances":{"USD":100},"ledgerBalances":{"USD":99},"driftBalances":{"USD":1},"error":"source unavailable"}`
	fullRuleFixture = `{"id":"rule-1","name":"pool drift","templateKind":"ledger_vs_pool_drift",` +
		`"templateSpec":{"ledgerName":"main"},"explanationCEL":"ledger == pool","enabled":true,"severity":"high","cadence":"daily",` +
		`"schedule":{"kind":"cron","expr":"0 0 * * *","tz":"UTC","safetyMargin":"30s"},` +
		`"notifications":["ops"],"labels":{"team":"finance"},"createdAt":"2026-09-14T08:00:00Z","updatedAt":"2026-09-14T09:00:00Z"}`
	fullEvaluationFixture = `{"id":"eval-1","ruleID":"rule-1","startedAt":"2026-09-14T08:00:00Z",` +
		`"endedAt":"2026-09-14T08:00:05Z","pitPerSource":{"ledger":"2026-09-14T07:59:00Z"},"result":"ERROR",` +
		`"evidence":{"source":"ledger"},"error":"source unavailable","costUnits":7,"createdAt":"2026-09-14T08:00:05Z"}`
	fullEvaluationArrayFixture = `{"id":"eval-2","ruleID":"rule-1","startedAt":"2026-09-14T08:00:00Z",` +
		`"endedAt":"2026-09-14T08:00:05Z","pitPerSource":{"ledger":"2026-09-14T07:59:00Z"},"result":"PASS",` +
		`"evidence":[{"fingerprint":"asset:USD","passed":true,"proof":{"ledger":"100","pool":"100"},` +
		`"evidence":{"drift":0},"extension":"kept"}],"costUnits":7,"createdAt":"2026-09-14T08:00:05Z"}`
	fullAlertFixture = `{"id":"alert-1","ruleID":"rule-1","fingerprint":"9f1c","periodID":"2026-09",` +
		`"status":"RESOLVED","severity":"high","firstSeenAt":"2026-09-14T08:00:00Z","lastSeenAt":"2026-09-14T09:00:00Z",` +
		`"occurrenceCount":3,"lastEvaluationID":"eval-1","evidence":{"USD":1},` +
		`"ack":{"by":"ops","at":"2026-09-14T09:00:00Z","note":"checking"},` +
		`"resolution":{"kind":"accepted_by_business","by":"ops","at":"2026-09-14T09:30:00Z","note":"accepted","transactionRefs":["tx-1"],"evidenceSnapshot":{"USD":1}},` +
		`"snooze":{"until":"2026-09-15T09:00:00Z","by":"ops","at":"2026-09-14T08:30:00Z","note":"investigating"},` +
		`"labels":{"team":"finance"},"createdAt":"2026-09-14T08:00:00Z","updatedAt":"2026-09-14T09:30:00Z"}`
	fullAlertEventFixture = `{"id":"event-1","alertID":"alert-1","evaluationID":"eval-1","type":"fail",` +
		`"prevStatus":null,"newStatus":"OPEN","payload":{"reason":"drift"},"at":"2026-09-14T09:00:00Z","isReopen":false,"notify":true}`
)

var (
	policyColumns = []sdk.TableColumn{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
		{Header: "Ledger", Field: "ledgerName"},
		{Header: "Payments Pool", Field: "paymentsPoolID"},
		{Header: "Created At", Field: "createdAt"},
	}
	reconciliationColumns = []sdk.TableColumn{
		{Header: "ID", Field: "id"},
		{Header: "Policy ID", Field: "policyID"},
		{Header: "Status", Field: "status"},
		{Header: "Created At", Field: "createdAt"},
	}
	ruleColumns = []sdk.TableColumn{
		{Header: "ID", Field: "id"},
		{Header: "Name", Field: "name"},
		{Header: "Template", Field: "templateKind"},
		{Header: "Enabled", Field: "enabled"},
		{Header: "Severity", Field: "severity"},
		{Header: "Cadence", Field: "cadence"},
	}
	evaluationColumns = []sdk.TableColumn{
		{Header: "ID", Field: "id"},
		{Header: "Rule ID", Field: "ruleID"},
		{Header: "Result", Field: "result"},
		{Header: "Started At", Field: "startedAt"},
		{Header: "Ended At", Field: "endedAt"},
	}
	alertColumns = []sdk.TableColumn{
		{Header: "ID", Field: "id"},
		{Header: "Rule ID", Field: "ruleID"},
		{Header: "Status", Field: "status"},
		{Header: "Severity", Field: "severity"},
		{Header: "Occurrences", Field: "occurrenceCount"},
		{Header: "Last Seen At", Field: "lastSeenAt"},
	}
	alertEventColumns = []sdk.TableColumn{
		{Header: "ID", Field: "id"},
		{Header: "Alert ID", Field: "alertID"},
		{Header: "Type", Field: "type"},
		{Header: "New Status", Field: "newStatus"},
		{Header: "At", Field: "at"},
	}
)

func renderCases() []renderCase {
	policy := func(id string) renderCase {
		return renderCase{id, policyColumns, components.Policy{}, policyFixture, policyFixture}
	}
	reconciliation := func(id string) renderCase {
		return renderCase{id, reconciliationColumns, components.Reconciliation{}, reconciliationFixture, fullReconciliationFixture}
	}
	rule := func(id string) renderCase {
		return renderCase{id, ruleColumns, components.Rule{}, ruleFixture, fullRuleFixture}
	}
	evaluation := func(id string) renderCase {
		return renderCase{id, evaluationColumns, components.Evaluation{}, evaluationFixture, fullEvaluationFixture}
	}
	alert := func(id string) renderCase {
		return renderCase{id, alertColumns, components.Alert{}, alertFixture, fullAlertFixture}
	}
	return []renderCase{
		policy("reconciliation.v1.policies.create"),
		policy("reconciliation.v1.policies.list"),
		policy("reconciliation.v1.policies.get"),
		reconciliation("reconciliation.v1.policies.reconcile"),
		reconciliation("reconciliation.v1.list"),
		reconciliation("reconciliation.v1.get"),
		rule("reconciliation.v1.rules.create"),
		rule("reconciliation.v1.rules.list"),
		rule("reconciliation.v1.rules.get"),
		rule("reconciliation.v1.rules.update"),
		evaluation("reconciliation.v1.rules.evaluate"),
		evaluation("reconciliation.v1.evaluations.list"),
		evaluation("reconciliation.v1.evaluations.get"),
		alert("reconciliation.v1.alerts.list"),
		alert("reconciliation.v1.alerts.get"),
		alert("reconciliation.v1.alerts.ack"),
		alert("reconciliation.v1.alerts.resolve"),
		alert("reconciliation.v1.alerts.accept"),
		alert("reconciliation.v1.alerts.snooze"),
		alert("reconciliation.v1.alerts.unsnooze"),
		{"reconciliation.v1.alerts.events", alertEventColumns, components.AlertEvent{}, alertEventFixture, fullAlertEventFixture},
	}
}

// commandsWithoutRenderableResults are the commands whose public result carries
// no product property at all. Their generated 204 response declares no schema
// and the adapter emits a canonical empty object, so there is nothing to put in
// a column. Resolving them needs a product change, not a catalogue change.
var commandsWithoutRenderableResults = map[string]string{
	"reconciliation.v1.policies.delete": "DELETE /policies/{policyID} declares 204 with no response schema; the adapter emits {}",
	"reconciliation.v1.rules.delete":    "DELETE /rules/{ruleID} declares 204 with no response schema; the adapter emits {}",
}

func TestCatalogueDeclaresTheExactOrderedTableColumns(t *testing.T) {
	declared := map[string]sdk.Command{}
	for _, command := range Catalogue() {
		declared[command.ID] = command
	}
	for _, test := range renderCases() {
		t.Run(test.commandID, func(t *testing.T) {
			command, ok := declared[test.commandID]
			if !ok {
				t.Fatalf("command %q is not in the catalogue", test.commandID)
			}
			if command.Render.Table == nil {
				t.Fatalf("command %q declares no table hint, want columns %v", test.commandID, test.columns)
			}
			if !reflect.DeepEqual(command.Render.Table.Columns, test.columns) {
				t.Fatalf("command %q table columns = %v, want %v", test.commandID, command.Render.Table.Columns, test.columns)
			}
		})
	}
}

func TestCommandsWithoutRenderableResultsDeclareNoTableHint(t *testing.T) {
	for _, command := range Catalogue() {
		reason, expected := commandsWithoutRenderableResults[command.ID]
		if !expected {
			continue
		}
		if command.Render.Table != nil {
			t.Errorf("command %q declares a table hint, but %s", command.ID, reason)
		}
	}
}

// TestEveryCommandIsAccountedForByARenderDecision forbids a silent gap: a new
// command has to be given columns or an explicit recorded reason for not
// having them.
func TestEveryCommandIsAccountedForByARenderDecision(t *testing.T) {
	decided := map[string]bool{}
	for _, test := range renderCases() {
		decided[test.commandID] = true
	}
	for id := range commandsWithoutRenderableResults {
		decided[id] = true
	}
	for _, command := range Catalogue() {
		if !decided[command.ID] {
			t.Errorf("command %q has neither declared columns nor a recorded reason for having none", command.ID)
		}
	}
	if len(decided) != len(Catalogue()) {
		t.Fatalf("render decisions cover %d commands, want %d", len(decided), len(Catalogue()))
	}
}

// TestTableColumnFieldsAreAlwaysPresentInTheRealPublicResult executes the real
// adapter against a minimal success fixture and derives the emitted property
// set from the result envelope. Every declared Field must be in it, so a column
// cannot name a property the product does not always return.
func TestTableColumnFieldsAreAlwaysPresentInTheRealPublicResult(t *testing.T) {
	declared := map[string]sdk.Command{}
	for _, command := range Catalogue() {
		declared[command.ID] = command
	}
	for _, test := range renderCases() {
		t.Run(test.commandID, func(t *testing.T) {
			result := emittedResult(t, test.commandID, test.fixture)
			command := declared[test.commandID]
			for _, column := range test.columns {
				if _, ok := valueAtDottedPath(result, column.Field); !ok {
					t.Errorf("column %q field %q does not resolve in the real public result",
						column.Header, column.Field)
				}
				if err := schemaRequiresDottedPath(command.PublicOutputSchema, command.Pagination.Supported, column.Field); err != nil {
					t.Errorf("column %q field %q is not guaranteed by PublicOutputSchema: %v",
						column.Header, column.Field, err)
				}
			}
		})
	}
}

// A future catalogue may use the dotted paths already supported by the public
// TableColumn contract. Keep the product-side proof honest even though today's
// Reconciliation projections only need top-level scalar properties.
func TestRenderHintProofTraversesDottedPaths(t *testing.T) {
	value := map[string]any{"metadata": map[string]any{"name": "nightly"}}
	if got, ok := valueAtDottedPath(value, "metadata.name"); !ok || got != "nightly" {
		t.Fatalf("metadata.name = %#v, %v; want nightly, true", got, ok)
	}
	schema := []byte(`{"type":"object","properties":{"metadata":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}},"required":["metadata"]}`)
	if err := schemaRequiresDottedPath(schema, false, "metadata.name"); err != nil {
		t.Fatalf("metadata.name is not required by nested schema: %v", err)
	}
	if err := schemaRequiresDottedPath(schema, false, "metadata.missing"); err == nil {
		t.Fatal("missing nested field was accepted")
	}
}

// TestFixturesCarryExactlyTheAlwaysPresentGeneratedFields pins each fixture to
// the generated type the adapter actually decodes into, so the coherence check
// above cannot be satisfied by a fixture that has drifted from the contract.
func TestFixturesCarryExactlyTheAlwaysPresentGeneratedFields(t *testing.T) {
	for _, test := range renderCases() {
		t.Run(test.commandID, func(t *testing.T) {
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal([]byte(test.fixture), &decoded); err != nil {
				t.Fatalf("fixture is not a JSON object: %v", err)
			}
			want := alwaysPresentFields(reflect.TypeOf(test.entity))
			if got := sortedKeys(decoded); !reflect.DeepEqual(got, sortedKeys(want)) {
				t.Fatalf("fixture properties = %v, want the always-present %s fields %v",
					got, reflect.TypeOf(test.entity).Name(), sortedKeys(want))
			}
		})
	}
}

// TestTableColumnsExcludeNestedAndUnrenderableFields derives the non-scalar
// properties of each generated result type by reflection and forbids them as
// columns: a map, slice or nested object cannot occupy a cell without printing
// raw JSON. All of them remain available in --output json / --output yaml.
func TestTableColumnsExcludeNestedAndUnrenderableFields(t *testing.T) {
	for _, test := range renderCases() {
		t.Run(test.commandID, func(t *testing.T) {
			nested := nonScalarFields(reflect.TypeOf(test.entity))
			for _, column := range test.columns {
				if nested[column.Field] {
					t.Errorf("column %q renders non-scalar field %q", column.Header, column.Field)
				}
			}
		})
	}
}

// deliberatelyExcludedScalars are scalar properties left out of the tables by
// judgement rather than by the derived rule above: unbounded free text and an
// opaque digest that would dominate the row width.
var deliberatelyExcludedScalars = map[string]string{
	"explanationCEL": "representative CEL program, unbounded free text",
	"error":          "product failure detail, unbounded free text",
	"fingerprint":    "opaque dedup digest, no value to a human scanning rows",
}

func TestTableColumnsExcludeLongValueFields(t *testing.T) {
	for _, test := range renderCases() {
		for _, column := range test.columns {
			if reason, excluded := deliberatelyExcludedScalars[column.Field]; excluded {
				t.Errorf("%s column %q renders %q, excluded because it is %s",
					test.commandID, column.Header, column.Field, reason)
			}
		}
	}
}

// TestRenderHintsDeclareNoSensitiveField re-proves the sensitivity boundary is
// unchanged by this surface: Reconciliation declares no sensitive output, so no
// column can resolve to one.
func TestRenderHintsDeclareNoSensitiveField(t *testing.T) {
	for _, command := range Catalogue() {
		if len(command.SensitiveOutputs) != 0 {
			t.Fatalf("command %q declares sensitive outputs %v; the render hints were chosen on the basis that none exist",
				command.ID, command.SensitiveOutputs)
		}
	}
}

// TestRenderHintsDoNotNarrowThePublicOutputSchema keeps the hints presentational.
// PublicOutputSchema stays exhaustive and byte-identical to RawOutputSchema, so
// declaring columns never removes a property from the structured output.
func TestRenderHintsDoNotNarrowThePublicOutputSchema(t *testing.T) {
	for _, command := range Catalogue() {
		t.Run(command.ID, func(t *testing.T) {
			if !bytes.Equal(command.RawOutputSchema, command.PublicOutputSchema) {
				t.Fatalf("RawOutputSchema = %s, PublicOutputSchema = %s; want byte-identical",
					command.RawOutputSchema, command.PublicOutputSchema)
			}
			var schema map[string]any
			if err := json.Unmarshal(command.PublicOutputSchema, &schema); err != nil {
				t.Fatalf("decode PublicOutputSchema: %v", err)
			}
			if command.Render.Table != nil && command.Pagination.Supported {
				if _, ok := schema["items"].(map[string]any); !ok {
					t.Fatal("paginated hinted command has no items schema")
				}
			}
		})
	}
}

func TestEveryOutputSchemaExactlyMatchesItsGeneratedResult(t *testing.T) {
	for _, test := range renderCases() {
		t.Run(test.commandID, func(t *testing.T) {
			command, _, ok := commandAndSpec(test.commandID)
			if !ok {
				t.Fatalf("command %q is not in the catalogue", test.commandID)
			}
			assertExactGeneratedSchema(t, command.PublicOutputSchema, command.Pagination.Supported, reflect.TypeOf(test.entity))
		})
	}

	for commandID := range commandsWithoutRenderableResults {
		t.Run(commandID, func(t *testing.T) {
			command, _, ok := commandAndSpec(commandID)
			if !ok {
				t.Fatalf("command %q is not in the catalogue", commandID)
			}
			assertStrictEmptyObjectSchema(t, command.PublicOutputSchema)
		})
	}
}

func TestEveryRealAdapterResultValidatesAgainstItsPublicSchema(t *testing.T) {
	for _, test := range renderCases() {
		t.Run(test.commandID, func(t *testing.T) {
			command, _, _ := commandAndSpec(test.commandID)
			if err := validateJSONSchemaValue(decodeEmittedPayload(t, test.commandID, test.fullFixture), command.PublicOutputSchema); err != nil {
				t.Fatalf("real adapter result does not match PublicOutputSchema: %v", err)
			}
		})
	}
	for commandID := range commandsWithoutRenderableResults {
		t.Run(commandID, func(t *testing.T) {
			command, _, _ := commandAndSpec(commandID)
			if err := validateJSONSchemaValue(decodeEmittedPayload(t, commandID, ""), command.PublicOutputSchema); err != nil {
				t.Fatalf("canonical 204 result does not match PublicOutputSchema: %v", err)
			}
		})
	}
}

func TestOutputSchemaCheckerRejectsStructuralMutations(t *testing.T) {
	command, _, _ := commandAndSpec("reconciliation.v1.rules.list")

	tests := map[string]func(map[string]any){
		"missing non-table property": func(schema map[string]any) {
			item := schema["items"].(map[string]any)
			delete(item["properties"].(map[string]any), "templateSpec")
		},
		"missing optional non-table property": func(schema map[string]any) {
			item := schema["items"].(map[string]any)
			delete(item["properties"].(map[string]any), "explanationCEL")
		},
		"wrong optional property type": func(schema map[string]any) {
			item := schema["items"].(map[string]any)
			item["properties"].(map[string]any)["explanationCEL"] = map[string]any{"type": "integer"}
		},
		"missing non-table required entry": func(schema map[string]any) {
			item := schema["items"].(map[string]any)
			item["required"] = removeStringValue(item["required"].([]any), "templateSpec")
		},
		"wrong property type": func(schema map[string]any) {
			item := schema["items"].(map[string]any)
			item["properties"].(map[string]any)["id"].(map[string]any)["type"] = "integer"
		},
		"wrong collection root type": func(schema map[string]any) {
			schema["type"] = "object"
		},
		"wrong items type": func(schema map[string]any) {
			schema["items"].(map[string]any)["type"] = "string"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal(command.PublicOutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			mutate(schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			if err := exactGeneratedSchemaError(encoded, true, reflect.TypeOf(components.Rule{})); err == nil {
				t.Fatal("mutated schema still matched the generated result contract")
			}
		})
	}
}

func TestOutputSchemaValidatorRejectsInvalidValues(t *testing.T) {
	command, _, _ := commandAndSpec("reconciliation.v1.policies.list")

	tests := map[string]func(any) any{
		"missing required value": func(value any) any {
			delete(value.([]any)[0].(map[string]any), "ledgerQuery")
			return value
		},
		"wrong scalar value type": func(value any) any {
			value.([]any)[0].(map[string]any)["id"] = float64(42)
			return value
		},
		"wrong root value type": func(value any) any {
			return value.([]any)[0]
		},
		"wrong item value type": func(value any) any {
			value.([]any)[0] = "not an object"
			return value
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := decodeEmittedPayload(t, command.ID, policyFixture)
			if err := validateJSONSchemaValue(mutate(value), command.PublicOutputSchema); err == nil {
				t.Fatal("invalid value passed recursive schema validation")
			}
		})
	}
}

func TestOutputSchemaValidatorRejectsInvalidOptionalValues(t *testing.T) {
	rules, _, _ := commandAndSpec("reconciliation.v1.rules.list")
	events, _, _ := commandAndSpec("reconciliation.v1.alerts.events")
	reconciliations, _, _ := commandAndSpec("reconciliation.v1.list")
	tests := []struct {
		name    string
		command sdk.Command
		fixture string
		mutate  func(any)
	}{
		{
			name:    "optional scalar type",
			command: rules,
			fixture: fullRuleFixture,
			mutate: func(value any) {
				value.([]any)[0].(map[string]any)["explanationCEL"] = float64(42)
			},
		},
		{
			name:    "optional array item type",
			command: rules,
			fixture: fullRuleFixture,
			mutate: func(value any) {
				value.([]any)[0].(map[string]any)["notifications"].([]any)[0] = float64(42)
			},
		},
		{
			name:    "nullable field alternative",
			command: events,
			fixture: fullAlertEventFixture,
			mutate: func(value any) {
				value.([]any)[0].(map[string]any)["evaluationID"] = true
			},
		},
		{
			name:    "typed optional map value",
			command: rules,
			fixture: fullRuleFixture,
			mutate: func(value any) {
				value.([]any)[0].(map[string]any)["labels"].(map[string]any)["team"] = float64(42)
			},
		},
		{
			name:    "typed required map value",
			command: reconciliations,
			fixture: fullReconciliationFixture,
			mutate: func(value any) {
				value.([]any)[0].(map[string]any)["paymentsBalances"].(map[string]any)["USD"] = "100"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := decodeEmittedPayload(t, test.command.ID, test.fixture)
			test.mutate(value)
			if err := validateJSONSchemaValue(value, test.command.PublicOutputSchema); err == nil {
				t.Fatal("invalid optional value passed recursive schema validation")
			}
		})
	}
}

func TestNestedGeneratedStructSchemasAreExhaustive(t *testing.T) {
	rules, _, _ := commandAndSpec("reconciliation.v1.rules.list")
	alerts, _, _ := commandAndSpec("reconciliation.v1.alerts.list")
	evaluations, _, _ := commandAndSpec("reconciliation.v1.evaluations.list")

	tests := []struct {
		name    string
		command sdk.Command
		path    []string
		want    map[string]any
	}{
		{
			name:    "schedule",
			command: rules,
			path:    []string{"schedule"},
			want: closedObjectSchemaForTest(map[string]any{
				"kind":         map[string]any{"type": "string"},
				"expr":         map[string]any{"type": "string"},
				"tz":           map[string]any{"type": "string"},
				"safetyMargin": map[string]any{"type": "string"},
			}, "kind"),
		},
		{
			name:    "ack",
			command: alerts,
			path:    []string{"ack"},
			want: closedObjectSchemaForTest(map[string]any{
				"by":   map[string]any{"type": "string"},
				"at":   map[string]any{"type": "string"},
				"note": map[string]any{"type": "string"},
			}, "at", "by"),
		},
		{
			name:    "resolution",
			command: alerts,
			path:    []string{"resolution"},
			want: closedObjectSchemaForTest(map[string]any{
				"kind":             map[string]any{"type": "string"},
				"by":               map[string]any{"type": "string"},
				"at":               map[string]any{"type": "string"},
				"note":             map[string]any{"type": "string"},
				"transactionRefs":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"evidenceSnapshot": map[string]any{"type": "object", "additionalProperties": true},
			}, "at", "by", "kind"),
		},
		{
			name:    "snooze",
			command: alerts,
			path:    []string{"snooze"},
			want: closedObjectSchemaForTest(map[string]any{
				"until": map[string]any{"type": "string"},
				"by":    map[string]any{"type": "string"},
				"at":    map[string]any{"type": "string"},
				"note":  map[string]any{"type": "string"},
			}, "at", "by", "until"),
		},
		{
			name:    "evaluation evidence array item",
			command: evaluations,
			path:    []string{"evidence", "items"},
			want: objectSchemaForTest(map[string]any{
				"fingerprint": map[string]any{"type": "string"},
				"passed":      map[string]any{"type": "boolean"},
				"proof":       map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"evidence":    map[string]any{"type": "object", "additionalProperties": true},
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := outputSchemaNode(test.command.PublicOutputSchema, test.command.Pagination.Supported, test.path...)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("schema node = %s, want %s", compactJSON(got), compactJSON(test.want))
			}
		})
	}
}

func TestBothEvaluationEvidenceUnionArmsValidateFromTheRealAdapter(t *testing.T) {
	command, _, _ := commandAndSpec("reconciliation.v1.evaluations.list")
	tests := map[string]struct {
		fixture string
		assert  func(*testing.T, any)
	}{
		"object": {fullEvaluationFixture, func(t *testing.T, value any) {
			evidence := value.([]any)[0].(map[string]any)["evidence"].(map[string]any)
			if evidence["source"] != "ledger" {
				t.Fatalf("object-arm evidence = %#v", evidence)
			}
		}},
		"array": {fullEvaluationArrayFixture, func(t *testing.T, value any) {
			evidence := value.([]any)[0].(map[string]any)["evidence"].([]any)[0].(map[string]any)
			want := []string{"evidence", "extension", "fingerprint", "passed", "proof"}
			if got := sortedKeys(evidence); !reflect.DeepEqual(got, want) {
				t.Fatalf("array-arm evidence properties = %v, want %v", got, want)
			}
		}},
	}
	for name, fixture := range tests {
		t.Run(name, func(t *testing.T) {
			value := decodeEmittedPayload(t, command.ID, fixture.fixture)
			fixture.assert(t, value)
			if err := validateJSONSchemaValue(value, command.PublicOutputSchema); err != nil {
				t.Fatalf("real adapter result does not match PublicOutputSchema: %v", err)
			}
		})
	}
}

func TestNestedSchemaCheckerRejectsKnownPropertyMutations(t *testing.T) {
	tests := []struct {
		name      string
		commandID string
		entity    reflect.Type
		mutate    func(map[string]any)
	}{
		{
			name:      "missing optional schedule property",
			commandID: "reconciliation.v1.rules.list",
			entity:    reflect.TypeOf(components.Rule{}),
			mutate: func(schema map[string]any) {
				schedule, _ := outputSchemaNodeFromMap(schema, true, "schedule")
				delete(schedule["properties"].(map[string]any), "expr")
			},
		},
		{
			name:      "wrong resolution array item type",
			commandID: "reconciliation.v1.alerts.list",
			entity:    reflect.TypeOf(components.Alert{}),
			mutate: func(schema map[string]any) {
				resolution, _ := outputSchemaNodeFromMap(schema, true, "resolution")
				transactionRefs := resolution["properties"].(map[string]any)["transactionRefs"].(map[string]any)
				transactionRefs["items"].(map[string]any)["type"] = "integer"
			},
		},
		{
			name:      "missing optional evidence item property",
			commandID: "reconciliation.v1.evaluations.list",
			entity:    reflect.TypeOf(components.Evaluation{}),
			mutate: func(schema map[string]any) {
				item, _ := outputSchemaNodeFromMap(schema, true, "evidence", "items")
				delete(item["properties"].(map[string]any), "passed")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, _, _ := commandAndSpec(test.commandID)
			var schema map[string]any
			if err := json.Unmarshal(command.PublicOutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			test.mutate(schema)
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			if err := exactGeneratedSchemaError(encoded, true, test.entity); err == nil {
				t.Fatal("nested schema mutation still matched the generated result contract")
			}
		})
	}
}

func TestOutputSchemaValidatorRejectsInvalidNestedGeneratedValues(t *testing.T) {
	rules, _, _ := commandAndSpec("reconciliation.v1.rules.list")
	alerts, _, _ := commandAndSpec("reconciliation.v1.alerts.list")
	evaluations, _, _ := commandAndSpec("reconciliation.v1.evaluations.list")
	tests := []struct {
		name    string
		command sdk.Command
		fixture string
		mutate  func(map[string]any)
	}{
		{"schedule required property type", rules, fullRuleFixture, func(item map[string]any) {
			item["schedule"].(map[string]any)["kind"] = float64(42)
		}},
		{"schedule undeclared property", rules, fullRuleFixture, func(item map[string]any) {
			item["schedule"].(map[string]any)["undeclared"] = true
		}},
		{"ack required property type", alerts, fullAlertFixture, func(item map[string]any) {
			item["ack"].(map[string]any)["at"] = false
		}},
		{"resolution array item type", alerts, fullAlertFixture, func(item map[string]any) {
			item["resolution"].(map[string]any)["transactionRefs"].([]any)[0] = float64(42)
		}},
		{"snooze required property missing", alerts, fullAlertFixture, func(item map[string]any) {
			delete(item["snooze"].(map[string]any), "until")
		}},
		{"evidence optional scalar type", evaluations, fullEvaluationArrayFixture, func(item map[string]any) {
			item["evidence"].([]any)[0].(map[string]any)["passed"] = "true"
		}},
		{"evidence proof map value type", evaluations, fullEvaluationArrayFixture, func(item map[string]any) {
			item["evidence"].([]any)[0].(map[string]any)["proof"].(map[string]any)["ledger"] = float64(100)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := decodeEmittedPayload(t, test.command.ID, test.fixture)
			test.mutate(value.([]any)[0].(map[string]any))
			if err := validateJSONSchemaValue(value, test.command.PublicOutputSchema); err == nil {
				t.Fatal("invalid nested value passed recursive schema validation")
			}
		})
	}
}

func TestOutputSchemaCheckerRejectsOptionalNullabilityMutation(t *testing.T) {
	command, _, _ := commandAndSpec("reconciliation.v1.alerts.events")
	for _, field := range []string{"evaluationID", "prevStatus"} {
		t.Run(field, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal(command.PublicOutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			item := schema["items"].(map[string]any)
			item["properties"].(map[string]any)[field].(map[string]any)["type"] = "string"
			encoded, err := json.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			if err := exactGeneratedSchemaError(encoded, true, reflect.TypeOf(components.AlertEvent{})); err == nil {
				t.Fatal("removing nullability still matched the generated result contract")
			}
		})
	}
}

func assertExactGeneratedSchema(t *testing.T, raw []byte, collection bool, entity reflect.Type) {
	t.Helper()
	if err := exactGeneratedSchemaError(raw, collection, entity); err != nil {
		t.Fatal(err)
	}
}

func exactGeneratedSchemaError(raw []byte, collection bool, entity reflect.Type) error {
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("decode output schema: %w", err)
	}
	want := generatedSchema(entity, collection)
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("output schema = %s, want exact generated-result schema %s", compactJSON(got), compactJSON(want))
	}
	return nil
}

func generatedSchema(entity reflect.Type, collection bool) map[string]any {
	properties := map[string]any{}
	required := []any{}
	for index := range entity.NumField() {
		field := entity.Field(index)
		name, _, optional := jsonTag(field)
		if name == "" {
			continue
		}
		kind := generatedJSONType(field.Type)
		if entity == reflect.TypeOf(components.Evaluation{}) && name == "evidence" {
			properties[name] = map[string]any{
				"type":                 []any{"array", "object"},
				"items":                generatedObjectSchema(reflect.TypeOf(components.Evidence{})),
				"additionalProperties": true,
			}
			continue
		}
		if strings.Contains(field.Type.String(), "optionalnullable.OptionalNullable[") {
			kind = []any{"string", "null"}
		}
		property := generatedPropertySchema(entity, name, kind)
		if entity == reflect.TypeOf(components.Rule{}) && name == "notifications" {
			property["items"] = map[string]any{"type": "string"}
		}
		properties[name] = property
		if !optional {
			required = append(required, name)
		}
	}
	sort.Slice(required, func(i, j int) bool { return required[i].(string) < required[j].(string) })
	item := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": true,
	}
	if !collection {
		item["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		return item
	}
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "array",
		"items":   item,
	}
}

func generatedPropertySchema(entity reflect.Type, name string, kind any) map[string]any {
	key := entity.Name() + "." + name
	switch key {
	case "Policy.ledgerQuery", "Rule.templateSpec", "Alert.evidence", "AlertEvent.payload":
		return map[string]any{"type": "object", "additionalProperties": true}
	case "Reconciliation.paymentsBalances", "Reconciliation.ledgerBalances", "Reconciliation.driftBalances":
		return map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer"}}
	case "Rule.labels", "Alert.labels", "Evaluation.pitPerSource":
		return map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
	case "Rule.schedule":
		return generatedObjectSchema(reflect.TypeOf(components.Schedule{}))
	case "Alert.ack":
		return generatedObjectSchema(reflect.TypeOf(components.Ack{}))
	case "Alert.resolution":
		return generatedObjectSchema(reflect.TypeOf(components.Resolution{}))
	case "Alert.snooze":
		return generatedObjectSchema(reflect.TypeOf(components.Snooze{}))
	default:
		return map[string]any{"type": kind}
	}
}

func generatedObjectSchema(entity reflect.Type) map[string]any {
	properties := map[string]any{}
	required := []any{}
	additionalProperties := false
	for index := range entity.NumField() {
		field := entity.Field(index)
		if field.Tag.Get("additionalProperties") == "true" {
			additionalProperties = true
		}
		name, _, optional := jsonTag(field)
		if name == "" {
			continue
		}
		properties[name] = generatedValueSchema(field.Type)
		if !optional {
			required = append(required, name)
		}
	}
	sort.Slice(required, func(i, j int) bool { return required[i].(string) < required[j].(string) })
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": additionalProperties,
	}
}

func generatedValueSchema(fieldType reflect.Type) map[string]any {
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	if fieldType == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string"}
	}
	switch fieldType.Kind() {
	case reflect.Map:
		if fieldType.Elem().Kind() == reflect.Interface {
			return map[string]any{"type": "object", "additionalProperties": true}
		}
		return map[string]any{"type": "object", "additionalProperties": generatedValueSchema(fieldType.Elem())}
	case reflect.Array, reflect.Slice:
		return map[string]any{"type": "array", "items": generatedValueSchema(fieldType.Elem())}
	case reflect.Struct:
		return generatedObjectSchema(fieldType)
	default:
		return map[string]any{"type": generatedJSONType(fieldType)}
	}
}

func generatedJSONType(fieldType reflect.Type) any {
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	if fieldType == reflect.TypeOf(time.Time{}) {
		return "string"
	}
	switch fieldType.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Map, reflect.Struct, reflect.Interface:
		return "object"
	case reflect.Array, reflect.Slice:
		return "array"
	default:
		return ""
	}
}

func assertStrictEmptyObjectSchema(t *testing.T, raw []byte) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode empty output schema: %v", err)
	}
	want := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty output schema = %s, want %s", compactJSON(got), compactJSON(want))
	}
}

func validateJSONSchemaValue(value any, raw []byte) error {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	return validateSchemaNode(value, schema, "$")
}

func validateSchemaNode(value any, schema map[string]any, path string) error {
	kind, err := matchingSchemaType(value, schema["type"])
	if err != nil {
		return fmt.Errorf("%s %w", path, err)
	}
	switch kind {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is %T, want object", path, value)
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, required := range schemaStrings(schema["required"]) {
			if _, present := object[required]; !present {
				return fmt.Errorf("%s.%s is required", path, required)
			}
		}
		for name, child := range object {
			property, declared := properties[name]
			if !declared {
				switch additional := schema["additionalProperties"].(type) {
				case bool:
					if !additional {
						return fmt.Errorf("%s.%s is not declared", path, name)
					}
				case map[string]any:
					if err := validateSchemaNode(child, additional, path+"."+name); err != nil {
						return err
					}
				}
				continue
			}
			childSchema, ok := property.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s schema is not an object", path, name)
			}
			if err := validateSchemaNode(child, childSchema, path+"."+name); err != nil {
				return err
			}
		}
		return nil
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s is %T, want array", path, value)
		}
		items, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s array schema has no object items schema", path)
		}
		for index, item := range array {
			if err := validateSchemaNode(item, items, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s is %T, want string", path, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s is %T, want boolean", path, value)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number {
			return fmt.Errorf("%s is %#v, want integer", path, value)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s is %T, want number", path, value)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s is %T, want null", path, value)
		}
	default:
		return fmt.Errorf("%s schema has unsupported type %q", path, kind)
	}
	return nil
}

func matchingSchemaType(value any, declaration any) (string, error) {
	if kind, ok := declaration.(string); ok {
		return kind, nil
	}
	kinds := schemaStrings(declaration)
	for _, kind := range kinds {
		switch kind {
		case "null":
			if value == nil {
				return kind, nil
			}
		case "object":
			if _, ok := value.(map[string]any); ok {
				return kind, nil
			}
		case "array":
			if _, ok := value.([]any); ok {
				return kind, nil
			}
		case "string":
			if _, ok := value.(string); ok {
				return kind, nil
			}
		}
	}
	return "", fmt.Errorf("is %T, want one of %v", value, kinds)
}

func schemaStrings(value any) []string {
	values, _ := value.([]any)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func compactJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func objectSchemaForTest(properties map[string]any, required ...string) map[string]any {
	sort.Strings(required)
	values := make([]any, len(required))
	for index, name := range required {
		values[index] = name
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             values,
		"additionalProperties": true,
	}
}

func closedObjectSchemaForTest(properties map[string]any, required ...string) map[string]any {
	schema := objectSchemaForTest(properties, required...)
	schema["additionalProperties"] = false
	return schema
}

func outputSchemaNode(raw []byte, collection bool, path ...string) (map[string]any, error) {
	var current map[string]any
	if err := json.Unmarshal(raw, &current); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	return outputSchemaNodeFromMap(current, collection, path...)
}

func outputSchemaNodeFromMap(current map[string]any, collection bool, path ...string) (map[string]any, error) {
	if collection {
		items, ok := current["items"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("collection schema has no object items schema")
		}
		current = items
	}
	for _, segment := range path {
		if segment == "items" {
			next, ok := current["items"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("schema node has no items schema")
			}
			current = next
			continue
		}
		properties, ok := current["properties"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema node has no properties map before %q", segment)
		}
		next, ok := properties[segment].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema has no property %q", segment)
		}
		current = next
	}
	return current, nil
}

func removeStringValue(values []any, remove string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}

func emittedResult(t *testing.T, commandID, fixture string) any {
	t.Helper()
	value := decodeEmittedPayload(t, commandID, fixture)
	command, _, _ := commandAndSpec(commandID)
	if command.Pagination.Supported {
		items := value.([]any)
		if len(items) == 0 {
			t.Fatal("collection result is empty")
		}
		return items[0]
	}
	return value
}

func decodeEmittedPayload(t *testing.T, commandID, fixture string) any {
	t.Helper()
	command, spec, ok := commandAndSpec(commandID)
	if !ok {
		t.Fatalf("command %q is not in the catalogue", commandID)
	}
	body := `{"data":` + fixture + `}`
	if command.Pagination.Supported {
		body = `{"cursor":{"pageSize":1,"hasMore":false,"data":[` + fixture + `]}}`
	}
	status := int32(200)
	if spec.operationID == "createPolicy" || spec.operationID == "createRule" {
		status = 201
	}
	if spec.result == resultEmpty {
		status, body = 204, ""
	}
	host := sdk.NewMemoryHost(func(context.Context, sdk.Request) (sdk.Responses, error) {
		return sdk.NewResponseStream(sdk.Response{Status: status, ContentType: "application/json", Body: []byte(body)}), nil
	})
	flags := []sdk.FlagOccurrence{}
	for _, flag := range command.Flags {
		if flag.Required {
			flags = append(flags, sdk.FlagOccurrence{Name: flag.Name, Value: `{}`})
		}
	}
	if err := (Plugin{}).Execute(context.Background(), validRequest(commandID, flags, sdk.SinglePageContinuationControl()), host); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(host.Events()) != 1 || host.Events()[0].Result == nil {
		t.Fatalf("events = %#v", host.Events())
	}
	result := host.Events()[0].Result
	var value any
	if err := json.Unmarshal(result.Data, &value); err != nil {
		t.Fatalf("decode result: %v (%s)", err, result.Data)
	}
	return value
}

func valueAtDottedPath(value any, path string) (any, bool) {
	current := value
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func schemaRequiresDottedPath(raw []byte, collection bool, path string) error {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	current := root
	if collection {
		items, ok := current["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("collection schema has no object items schema")
		}
		current = items
	}
	for _, segment := range strings.Split(path, ".") {
		if !stringArrayContains(current["required"], segment) {
			return fmt.Errorf("segment %q is not required", segment)
		}
		properties, ok := current["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("segment %q has no properties map", segment)
		}
		next, ok := properties[segment].(map[string]any)
		if !ok {
			return fmt.Errorf("segment %q has no property schema", segment)
		}
		current = next
	}
	return nil
}

func stringArrayContains(value any, want string) bool {
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// alwaysPresentFields returns the JSON property names a generated result type
// emits unconditionally: every exported field whose tag carries no omitzero or
// omitempty option.
func alwaysPresentFields(entity reflect.Type) map[string]bool {
	out := map[string]bool{}
	for index := range entity.NumField() {
		name, options, optional := jsonTag(entity.Field(index))
		if name == "" || optional {
			continue
		}
		_ = options
		out[name] = true
	}
	return out
}

// nonScalarFields returns the JSON property names a table cell cannot hold:
// maps, slices, nested objects and interfaces. Pointers are unwrapped, and
// time.Time counts as scalar because it renders as one RFC 3339 token.
func nonScalarFields(entity reflect.Type) map[string]bool {
	out := map[string]bool{}
	for index := range entity.NumField() {
		name, _, _ := jsonTag(entity.Field(index))
		if name == "" {
			continue
		}
		if !scalarRenderable(entity.Field(index).Type) {
			out[name] = true
		}
	}
	return out
}

func scalarRenderable(fieldType reflect.Type) bool {
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	if fieldType == reflect.TypeOf(time.Time{}) {
		return true
	}
	switch fieldType.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func jsonTag(field reflect.StructField) (name string, options []string, optional bool) {
	tag, ok := field.Tag.Lookup("json")
	if !ok || tag == "-" {
		return "", nil, false
	}
	parts := splitTag(tag)
	name = parts[0]
	options = parts[1:]
	for _, option := range options {
		if option == "omitzero" || option == "omitempty" {
			optional = true
		}
	}
	if name == "-" {
		return "", nil, false
	}
	return name, options, optional
}

func splitTag(tag string) []string {
	parts := []string{""}
	for _, character := range tag {
		if character == ',' {
			parts = append(parts, "")
			continue
		}
		parts[len(parts)-1] += string(character)
	}
	return parts
}

func sortedKeys[T any](values map[string]T) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
