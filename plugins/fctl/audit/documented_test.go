package audit_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/formancehq/reconciliation/plugins/fctl/audit"
	"gopkg.in/yaml.v3"
)

// The tests in this file assert the specific claims the hand-written inventory
// document makes. Each one fails if the claim stops being true of the pinned
// sources, so the prose cannot silently go stale.

func readSpec(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse document: %v", err)
	}
	return doc
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile("../../../" + rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

// TestSecuritySchemeIsReferencedButUndefined asserts divergence D2 and
// generation blocker G3: every operation names an `Authorization` scheme the
// document never defines.
func TestSecuritySchemeIsReferencedButUndefined(t *testing.T) {
	report := build(t)

	if got := report.Document.ReferencedSecuritySchemes; len(got) != 1 || got[0] != "Authorization" {
		t.Fatalf("referenced security schemes = %v, want [Authorization]", got)
	}
	if got := report.Document.DeclaredSecuritySchemes; len(got) != 0 {
		t.Errorf("declared security schemes = %v, want none", got)
	}
	if report.Document.HasRootSecurity {
		t.Error("document declares a root-level security key; D2 assumes it does not")
	}
	if got := report.UndefinedSecuritySchemes; len(got) != 1 || got[0] != "Authorization" {
		t.Fatalf("undefined security schemes = %v, want [Authorization]", got)
	}

	doc := readSpec(t)
	components, _ := doc["components"].(map[string]any)
	if _, ok := components["securitySchemes"]; ok {
		t.Error("components.securitySchemes exists; D2 claims it does not")
	}

	// Every operation must carry the scheme, so the defect is service-wide
	// rather than a stray operation.
	if report.Totals.OperationsWithDeclaredScopes != report.Totals.SpecOperations {
		t.Errorf("%d of %d operations declare a security block; D2 assumes all of them do",
			report.Totals.OperationsWithDeclaredScopes, report.Totals.SpecOperations)
	}
	for _, rec := range report.Operations {
		if len(rec.SecuritySchemes) != 1 || rec.SecuritySchemes[0] != "Authorization" {
			t.Errorf("operation %s references schemes %v, want [Authorization]", rec.OperationID, rec.SecuritySchemes)
		}
	}
}

// TestDeclaredScopesAreTheTwoServiceScopes asserts the scope vocabulary the
// inventory quotes: exactly `reconciliation:read` and `reconciliation:write`,
// one per operation, aligned with the HTTP method's mutability.
func TestDeclaredScopesAreTheTwoServiceScopes(t *testing.T) {
	report := build(t)

	seen := map[string]int{}
	for _, rec := range report.Operations {
		if len(rec.Scopes) != 1 {
			t.Errorf("operation %s declares %d scopes, want exactly 1", rec.OperationID, len(rec.Scopes))
			continue
		}
		scope := rec.Scopes[0]
		seen[scope]++

		wantWrite := rec.Mutating()
		gotWrite := scope == "reconciliation:write"
		if wantWrite != gotWrite {
			t.Errorf("operation %s is %s and declares %s", rec.OperationID, rec.Method, scope)
		}
	}

	var vocabulary []string
	for scope := range seen {
		vocabulary = append(vocabulary, scope)
	}
	sort.Strings(vocabulary)
	if len(vocabulary) != 2 || vocabulary[0] != "reconciliation:read" || vocabulary[1] != "reconciliation:write" {
		t.Fatalf("scope vocabulary = %v, want [reconciliation:read reconciliation:write]", vocabulary)
	}
}

// TestScopesAreNotEnforcedInService asserts divergence D6: the scope strings
// appear nowhere in the service's own Go sources.
func TestScopesAreNotEnforcedInService(t *testing.T) {
	router := readRepoFile(t, "internal/api/router.go")

	for _, scope := range []string{"reconciliation:read", "reconciliation:write"} {
		if strings.Contains(router, scope) {
			t.Errorf("internal/api/router.go mentions %s; D6 claims the service never inspects scopes", scope)
		}
	}
	if !strings.Contains(router, "auth.Middleware(authenticator)") {
		t.Error("internal/api/router.go no longer calls auth.Middleware(authenticator); D6 evidence is stale")
	}
}

// TestInfoProbeIsServedOutsideTheAuthenticatedGroup asserts divergence D1: the
// document requires a scope on GET /_info, but the server registers it above
// the authenticated group.
func TestInfoProbeIsServedOutsideTheAuthenticatedGroup(t *testing.T) {
	report := build(t)

	var probe *audit.Record
	for i := range report.Operations {
		if report.Operations[i].OperationID == "getServerInfo" {
			probe = &report.Operations[i]
			break
		}
	}
	if probe == nil {
		t.Fatal("document no longer declares getServerInfo")
	}
	if !probe.HasSecurity || len(probe.Scopes) != 1 || probe.Scopes[0] != "reconciliation:read" {
		t.Errorf("getServerInfo scopes = %v, want [reconciliation:read]; D1 evidence is stale", probe.Scopes)
	}

	router := readRepoFile(t, "internal/api/router.go")
	infoAt := strings.Index(router, `r.Get("/_info"`)
	authAt := strings.Index(router, "r.Use(auth.Middleware(authenticator))")
	if infoAt < 0 || authAt < 0 {
		t.Fatal("internal/api/router.go no longer registers /_info or the auth middleware; D1 evidence is stale")
	}
	if infoAt > authAt {
		t.Error("/_info is now registered after the auth middleware; D1 no longer holds")
	}
}

// TestHealthcheckIsUndocumented asserts divergence D4.
func TestHealthcheckIsUndocumented(t *testing.T) {
	router := readRepoFile(t, "internal/api/router.go")
	if !strings.Contains(router, `r.Get("/_healthcheck"`) {
		t.Error("internal/api/router.go no longer registers /_healthcheck; D4 evidence is stale")
	}

	doc := readSpec(t)
	paths, _ := doc["paths"].(map[string]any)
	if _, ok := paths["/_healthcheck"]; ok {
		t.Error("the document now declares /_healthcheck; D4 no longer holds")
	}
}

// TestGetWithBodyOperationsAreExactlyBlocked asserts blocker B1: the set of
// GET operations carrying a request body is exactly the set B1 lists.
func TestGetWithBodyOperationsAreExactlyBlocked(t *testing.T) {
	report := build(t)

	var withBody []string
	for _, rec := range report.Operations {
		if rec.Risk.GetWithBody {
			withBody = append(withBody, rec.OperationID)
			if rec.RequestBody != "QueryBuilder" {
				t.Errorf("operation %s carries a GET body of %q, want QueryBuilder", rec.OperationID, rec.RequestBody)
			}
			if len(rec.Blockers) == 0 {
				t.Errorf("operation %s is a GET with a body but carries no blocker", rec.OperationID)
			}
		}
	}
	sort.Strings(withBody)

	want := []string{"listAlerts", "listEvaluations", "listPolicies", "listReconciliations", "listRules"}
	if len(withBody) != len(want) {
		t.Fatalf("GET-with-body operations = %v, want %v", withBody, want)
	}
	for i := range want {
		if withBody[i] != want[i] {
			t.Errorf("GET-with-body[%d] = %q, want %q", i, withBody[i], want[i])
		}
	}

	blocked := audit.BlockedOperationIDs()
	if len(blocked) != len(want) {
		t.Fatalf("blocked operations = %v, want %v", blocked, want)
	}
}

// TestNoQueryStringFilterParameterIsDeclared asserts divergence D3: the server
// accepts an undeclared `query` query-string parameter as the filter fallback.
func TestNoQueryStringFilterParameterIsDeclared(t *testing.T) {
	utils := readRepoFile(t, "internal/api/utils.go")
	if !strings.Contains(utils, `query.ParseJSON(r.URL.Query().Get("query"))`) {
		t.Error("internal/api/utils.go no longer reads the `query` query-string parameter; D3 evidence is stale")
	}

	report := build(t)
	for _, rec := range report.Operations {
		for _, p := range rec.Parameters {
			if p.Name == "query" {
				t.Errorf("operation %s now declares a `query` parameter; D3 no longer holds", rec.OperationID)
			}
		}
	}
}

// TestDocumentVersionIsAPlaceholder asserts divergence D5.
func TestDocumentVersionIsAPlaceholder(t *testing.T) {
	report := build(t)
	if report.Document.Version != "RECONCILIATION_VERSION" {
		t.Errorf("info.version = %q, want the unsubstituted placeholder RECONCILIATION_VERSION", report.Document.Version)
	}
	if report.Document.Title != "Reconciliation API" {
		t.Errorf("info.title = %q, want %q", report.Document.Title, "Reconciliation API")
	}
}

// TestNoSDKNameOverrides asserts divergence D7: no operation carries a
// Speakeasy name override, so an SDK method name derives from the operationId.
func TestNoSDKNameOverrides(t *testing.T) {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	if found := regexp.MustCompile(`x-speakeasy[a-z-]*`).FindAllString(string(raw), -1); len(found) > 0 {
		t.Errorf("document carries Speakeasy extensions %v; D7 claims it carries none", found)
	}

	for _, rec := range build(t).Operations {
		if rec.SDKMethod != rec.OperationID {
			t.Errorf("operation %s has sdkMethod %q; D7 assumes they are equal", rec.OperationID, rec.SDKMethod)
		}
	}
}

// TestNoIdempotencyKeyIsDeclared asserts audit.NoIdempotencyKey: no operation
// exposes an idempotency key, so no POST on this surface is replay-safe.
func TestNoIdempotencyKeyIsDeclared(t *testing.T) {
	if !audit.NoIdempotencyKey {
		t.Fatal("NoIdempotencyKey is false; this test asserts the documented constant")
	}

	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "idempotency") {
		t.Error("the document now mentions idempotency; NoIdempotencyKey is stale")
	}

	for _, rec := range build(t).Operations {
		if rec.Method == "POST" && rec.Risk.ReplaySafe {
			t.Errorf("operation %s is a POST marked replay-safe", rec.OperationID)
		}
	}
}

// TestNoSecretBearingSchemaProperty asserts audit.NoSecretBearingOperation: no
// declared schema exposes a credential-shaped property.
func TestNoSecretBearingSchemaProperty(t *testing.T) {
	if !audit.NoSecretBearingOperation {
		t.Fatal("NoSecretBearingOperation is false; this test asserts the documented constant")
	}

	doc := readSpec(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if len(schemas) == 0 {
		t.Fatal("document declares no schemas; the evidence for NoSecretBearingOperation is stale")
	}

	credential := regexp.MustCompile(`(?i)secret|password|token|apikey|api_key|credential|privatekey|passphrase`)
	for name, schema := range schemas {
		object, _ := schema.(map[string]any)
		properties, _ := object["properties"].(map[string]any)
		for property := range properties {
			if credential.MatchString(property) {
				t.Errorf("schema %s declares credential-shaped property %q", name, property)
			}
		}
	}

	for _, rec := range build(t).Operations {
		if rec.Risk.Secret != audit.SecretNone {
			t.Errorf("operation %s is marked secret-bearing (%s)", rec.OperationID, rec.Risk.Secret)
		}
	}
}

// TestNoDisplayOnceOperation asserts the empty display-once table.
func TestNoDisplayOnceOperation(t *testing.T) {
	for _, rec := range build(t).Operations {
		if rec.Risk.DisplayOnce {
			t.Errorf("operation %s is marked display-once but the table is documented as empty", rec.OperationID)
		}
	}
}

// TestDestructiveOperationsAreTheTwoDeletes asserts the risk table's
// destructive derivation: exactly deletePolicy and deleteRule.
func TestDestructiveOperationsAreTheTwoDeletes(t *testing.T) {
	var destructive []string
	for _, rec := range build(t).Operations {
		if rec.Risk.Destructive {
			destructive = append(destructive, rec.OperationID)
		}
	}
	sort.Strings(destructive)

	want := []string{"deletePolicy", "deleteRule"}
	if len(destructive) != len(want) {
		t.Fatalf("destructive operations = %v, want %v", destructive, want)
	}
	for i := range want {
		if destructive[i] != want[i] {
			t.Errorf("destructive[%d] = %q, want %q", i, destructive[i], want[i])
		}
	}
}

// TestAlertLifecycleOperationsAreStateTransitions asserts that the five alert
// lifecycle operations are recorded as non-destructive, non-replay-safe state
// transitions rather than being silently lumped in with plain writes.
func TestAlertLifecycleOperationsAreStateTransitions(t *testing.T) {
	var transitions []string
	for _, rec := range build(t).Operations {
		if !rec.Risk.StateTransition {
			continue
		}
		transitions = append(transitions, rec.OperationID)
		if rec.Risk.Destructive {
			t.Errorf("operation %s is both a state transition and destructive", rec.OperationID)
		}
		if rec.Risk.ReplaySafe {
			t.Errorf("operation %s is a POST state transition marked replay-safe", rec.OperationID)
		}
	}
	sort.Strings(transitions)

	want := []string{"ackAlert", "acceptAlert", "resolveAlert", "snoozeAlert", "unsnoozeAlert"}
	sort.Strings(want)
	if len(transitions) != len(want) {
		t.Fatalf("state transitions = %v, want %v", transitions, want)
	}
	for i := range want {
		if transitions[i] != want[i] {
			t.Errorf("stateTransition[%d] = %q, want %q", i, transitions[i], want[i])
		}
	}
}

// TestPaginatedOperationsExposeBothCursorParameters asserts the pagination
// derivation and the documented exception: listAlertEvents paginates but takes
// no query filter.
func TestPaginatedOperationsExposeBothCursorParameters(t *testing.T) {
	report := build(t)

	var paginated []string
	for _, rec := range report.Operations {
		if !rec.Risk.Paginated {
			continue
		}
		paginated = append(paginated, rec.OperationID)

		var cursor, pageSize bool
		for _, p := range rec.Parameters {
			if p.In != "query" {
				continue
			}
			switch p.Name {
			case "cursor":
				cursor = true
			case "pageSize":
				pageSize = true
			}
		}
		if !cursor || !pageSize {
			t.Errorf("operation %s is marked paginated without both query parameters", rec.OperationID)
		}
	}
	sort.Strings(paginated)

	want := []string{"listAlertEvents", "listAlerts", "listEvaluations", "listPolicies", "listReconciliations", "listRules"}
	if len(paginated) != len(want) {
		t.Fatalf("paginated operations = %v, want %v", paginated, want)
	}
	for i := range want {
		if paginated[i] != want[i] {
			t.Errorf("paginated[%d] = %q, want %q", i, paginated[i], want[i])
		}
	}

	// listAlertEvents is the one paginated read that takes no QueryBuilder
	// body, which is why it is paginated but not blocked by B1.
	for _, rec := range report.Operations {
		if rec.OperationID == "listAlertEvents" {
			if rec.HasRequestBody() {
				t.Error("listAlertEvents now declares a request body; the B1 exception is stale")
			}
			if len(rec.Blockers) != 0 {
				t.Errorf("listAlertEvents carries blockers %v; it was documented as unblocked", rec.Blockers)
			}
		}
	}
}

// TestGenerationBlockersAreRecorded asserts that the reasons the Task 10A SDK
// was not generated are present and evidenced, so "no client" stays a recorded
// decision rather than an omission.
func TestGenerationBlockersAreRecorded(t *testing.T) {
	if len(audit.GenerationBlockers) == 0 {
		t.Fatal("no generation blockers recorded; the absent SDK would be unexplained")
	}

	seen := map[string]struct{}{}
	for _, g := range audit.GenerationBlockers {
		if g.ID == "" || g.Summary == "" || g.Evidence == "" {
			t.Errorf("generation blocker %q is incomplete", g.ID)
		}
		if _, dup := seen[g.ID]; dup {
			t.Errorf("duplicate generation blocker ID %q", g.ID)
		}
		seen[g.ID] = struct{}{}
	}

	for _, id := range []string{"G1-mvp4-gates-open", "G2-speakeasy-absent-from-toolchain", "G3-undefined-security-scheme"} {
		if _, ok := seen[id]; !ok {
			t.Errorf("generation blocker %s is missing", id)
		}
	}
}

// TestSpeakeasyIsAbsentFromTheToolchain asserts generation blocker G2 against
// the repository's own declared environment.
func TestSpeakeasyIsAbsentFromTheToolchain(t *testing.T) {
	flake := readRepoFile(t, "flake.nix")
	if strings.Contains(strings.ToLower(flake), "speakeasy") {
		t.Error("flake.nix now provides Speakeasy; G2 no longer holds")
	}

	justfile := readRepoFile(t, "Justfile")
	if strings.Contains(strings.ToLower(justfile), "speakeasy") {
		t.Error("the Justfile now declares a Speakeasy recipe; G2 no longer holds")
	}
}

// TestDivergencesAreRecorded asserts every documented divergence is present and
// carries evidence.
func TestDivergencesAreRecorded(t *testing.T) {
	seen := map[string]struct{}{}
	for _, d := range audit.Divergences {
		if d.ID == "" || d.Summary == "" || d.Evidence == "" {
			t.Errorf("divergence %q is incomplete", d.ID)
		}
		if _, dup := seen[d.ID]; dup {
			t.Errorf("duplicate divergence ID %q", d.ID)
		}
		seen[d.ID] = struct{}{}
	}

	for _, id := range []string{
		"D1-info-probe-auth",
		"D2-undefined-security-scheme",
		"D3-undeclared-query-parameter",
		"D4-healthcheck-undocumented",
		"D5-version-placeholder",
		"D6-scopes-not-enforced-in-service",
		"D7-no-sdk-name-overrides",
	} {
		if _, ok := seen[id]; !ok {
			t.Errorf("divergence %s is missing", id)
		}
	}
}

// TestNoOperationIsDeprecated asserts the inventory's exclusion section: the
// document marks nothing deprecated, so no operation is excluded on that basis.
func TestNoOperationIsDeprecated(t *testing.T) {
	report := build(t)
	if report.Totals.DeprecatedOperations != 0 {
		t.Errorf("deprecated operations = %d, want 0", report.Totals.DeprecatedOperations)
	}
	for _, rec := range report.Operations {
		if rec.Deprecated {
			t.Errorf("operation %s is marked deprecated", rec.OperationID)
		}
	}
}

// TestServerRoutesCoverEveryDocumentedOperation is the spec-versus-server
// completeness check: every path the document declares is registered by the
// router, so no documented operation is unimplemented.
func TestServerRoutesCoverEveryDocumentedOperation(t *testing.T) {
	router := readRepoFile(t, "internal/api/router.go")

	for _, rec := range build(t).Operations {
		// The router uses chi path syntax, which matches the OpenAPI template
		// for this document ({policyID}, {alertID}, …).
		needle := `"` + rec.Path + `"`
		if !strings.Contains(router, needle) {
			t.Errorf("operation %s declares path %s, which internal/api/router.go does not register",
				rec.OperationID, rec.Path)
		}
	}
}
