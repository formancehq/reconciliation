package core

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
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
	commandID string
	columns   []sdk.TableColumn
	entity    any
	fixture   string
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
		return renderCase{id, policyColumns, components.Policy{}, policyFixture}
	}
	reconciliation := func(id string) renderCase {
		return renderCase{id, reconciliationColumns, components.Reconciliation{}, reconciliationFixture}
	}
	rule := func(id string) renderCase {
		return renderCase{id, ruleColumns, components.Rule{}, ruleFixture}
	}
	evaluation := func(id string) renderCase {
		return renderCase{id, evaluationColumns, components.Evaluation{}, evaluationFixture}
	}
	alert := func(id string) renderCase {
		return renderCase{id, alertColumns, components.Alert{}, alertFixture}
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
		{"reconciliation.v1.alerts.events", alertEventColumns, components.AlertEvent{}, alertEventFixture},
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
	for _, test := range renderCases() {
		t.Run(test.commandID, func(t *testing.T) {
			present := emittedProperties(t, test.commandID, test.fixture)
			for _, column := range test.columns {
				if !present[column.Field] {
					t.Errorf("column %q field %q is not a property of the real public result %v",
						column.Header, column.Field, sortedKeys(present))
				}
			}
		})
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
			want := objectSchema
			if command.Pagination.Supported {
				want = collectionSchema
			}
			if string(command.PublicOutputSchema) != string(want) {
				t.Fatalf("PublicOutputSchema = %s, want the exhaustive %s", command.PublicOutputSchema, want)
			}
			if string(command.RawOutputSchema) != string(command.PublicOutputSchema) {
				t.Fatalf("RawOutputSchema = %s, PublicOutputSchema = %s; want byte-identical",
					command.RawOutputSchema, command.PublicOutputSchema)
			}
		})
	}
}

func emittedProperties(t *testing.T, commandID, fixture string) map[string]bool {
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
	present := map[string]bool{}
	switch result.Shape {
	case sdk.ResultCollection:
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(result.Data, &items); err != nil {
			t.Fatalf("decode collection result: %v (%s)", err, result.Data)
		}
		if len(items) == 0 {
			t.Fatalf("collection result is empty: %s", result.Data)
		}
		for _, item := range items {
			for key := range item {
				present[key] = true
			}
		}
	default:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(result.Data, &object); err != nil {
			t.Fatalf("decode object result: %v (%s)", err, result.Data)
		}
		for key := range object {
			present[key] = true
		}
	}
	return present
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
