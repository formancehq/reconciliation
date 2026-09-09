package audit

import "sort"

// ProductRevision pins the Reconciliation commit every fact in this package was
// read from.
const ProductRevision = "0221edf2f8727def40368a5e4e2d0d0fafd7d4e7"

// FctlV2Revision pins the fctl-v2 programme tree whose Task 10A/10B text and
// pinned command inventory this preparation is measured against.
const FctlV2Revision = "8de8c4539ea6664351762dd8dd0e865292e3f216"

// Blocker is a recorded reason an operation cannot yet be admitted into the
// plugin catalogue, separate from the verified facts about it.
type Blocker struct {
	// OperationIDs are the operations the blocker applies to.
	OperationIDs []string
	// ID is a stable short handle used in the inventory document.
	ID string
	// Summary states the blocker in one sentence.
	Summary string
	// Evidence names the exact sources the blocker was read from.
	Evidence string
}

// Blockers are the recorded per-operation admission blockers. Each one blocks
// only the operations it lists; no service-wide or family-wide blocking.
var Blockers = []Blocker{
	{
		ID: "B1-get-with-body",
		OperationIDs: []string{
			"listAlerts",
			"listEvaluations",
			"listPolicies",
			"listReconciliations",
			"listRules",
		},
		Summary: "These GET operations carry their filter in a JSON request " +
			"body (the free-form QueryBuilder object). A host transport that " +
			"drops or forbids GET request bodies silently degrades them into " +
			"unfiltered listings, which is a wrong answer rather than an " +
			"error, so the request boundary has to be proven to preserve GET " +
			"bodies before these are admitted. The browser fetch API forbids a " +
			"body on GET outright, so this blocker is load-bearing for the " +
			"dual-host requirement rather than theoretical.",
		Evidence: "openapi.yaml: each listed operation declares " +
			"`requestBody.content.application/json.schema: QueryBuilder` " +
			"alongside method GET. The server reads it in " +
			"internal/api/utils.go getQueryBuilder, which calls io.ReadAll on " +
			"r.Body (bounded to 1 MiB by maxQueryBuilderBodySize) before " +
			"falling back to the undeclared `query` query-string parameter; " +
			"see divergence D3.",
	},
}

// BlockedOperationIDs returns the sorted, de-duplicated set of operationIds
// carrying at least one blocker.
func BlockedOperationIDs() []string {
	seen := map[string]struct{}{}
	for _, b := range Blockers {
		for _, id := range b.OperationIDs {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// GenerationBlocker is a recorded reason the Task 10A public Go SDK cannot be
// generated and committed from this repository at ProductRevision. These are
// task-level, not per-operation: they block the generation step itself.
type GenerationBlocker struct {
	ID       string
	Summary  string
	Evidence string
}

// GenerationBlockers are the recorded reasons `pkg/client` was not generated in
// this tranche. They are facts about the current tree and programme state, not
// a judgement about whether the SDK should eventually exist.
var GenerationBlockers = []GenerationBlocker{
	{
		ID: "G1-mvp4-gates-open",
		Summary: "The fctl-v2 programme sequences Task 10A after MVP4, and " +
			"MVP4 is not accepted: the Task 4B portable runtime cutover, the " +
			"Task 4C example replay, and the Task 4D deletion of the native " +
			"gRPC and browser Go-WASM paths are all still open. Generating and " +
			"committing an SDK now would pin a client shape against an " +
			"unfrozen adapter and capability boundary.",
		Evidence: "fctl-v2 " + FctlV2Revision + " " +
			"docs/superpowers/plans/2026-08-26-fctl-complete-program.md: every " +
			"Task 10A acceptance box is unchecked, the Task 4B/4C/4D boxes at " +
			"lines 1709, 1760, 1809, 1829 and 1932 are unchecked, and the " +
			"sequencing paragraph states \"After MVP4, Tasks 7, 8, 9, Task 10A, " +
			"and the Wallets and Flows product subtasks in Task 10B may run in " +
			"parallel.\"",
	},
	{
		ID: "G2-speakeasy-absent-from-toolchain",
		Summary: "Task 10A requires `just generate-client` to regenerate the " +
			"SDK inside the declared Nix environment without a globally " +
			"installed Speakeasy binary. This repository's development shell " +
			"does not provide Speakeasy, and the only Speakeasy invocation in " +
			"the repository is a CI job authenticated by a repository secret, " +
			"so the generation cannot be reproduced locally or offline without " +
			"introducing a credential this preparation must not use.",
		Evidence: "flake.nix devShells.default packages are ginkgo, go_1_26, " +
			"gotools, just, golangci-lint and goreleaser-pro; Speakeasy is " +
			"absent. Justfile declares no client or SDK recipe. " +
			".github/workflows/main.yml and .github/workflows/releases.yml pass " +
			"SPEAKEASY_API_KEY from repository secrets into the release job.",
	},
	{
		ID: "G3-undefined-security-scheme",
		Summary: "All 24 operations declare `security: [{Authorization: [...]}]`, " +
			"but the document defines no components.securitySchemes at all and " +
			"no root-level security. A generator has no mechanism, type, or " +
			"header binding to emit for `Authorization`, so the contract must " +
			"be repaired before a faithful SDK can be produced. This does not " +
			"block the plugin catalogue, because Task 10A requires generated " +
			"auth to stay disabled and the fctl host owns credentials; it " +
			"blocks the generation step.",
		Evidence: "openapi.yaml components declares only parameters, responses, " +
			"requestBodies and schemas. Asserted by " +
			"TestSecuritySchemeIsReferencedButUndefined and by the report's " +
			"document.undefinedSecuritySchemes field.",
	},
}

// Divergence is a recorded mismatch between the document and the server, or a
// document defect, that is not itself a per-operation admission blocker but
// changes what fctl may assume.
type Divergence struct {
	ID       string
	Summary  string
	Evidence string
}

// Divergences are the recorded spec-versus-server mismatches and document
// defects.
var Divergences = []Divergence{
	{
		ID: "D1-info-probe-auth",
		Summary: "The document declares `getServerInfo` (GET /_info) under " +
			"`Authorization: [reconciliation:read]`, but the server registers " +
			"it on the root router above every authenticated group, so it " +
			"answers unauthenticated. fctl needs the unauthenticated read to " +
			"learn the product major before it can pick a provider, so the " +
			"server behaviour is the one to rely on and the document is the one " +
			"to fix.",
		Evidence: "openapi.yaml GET /_info declares the security block. " +
			"internal/api/router.go registers r.Get(\"/_info\", " +
			"api.InfoHandler(serviceInfo)) at line 36, while " +
			"r.Use(auth.Middleware(authenticator)) appears at line 39 inside " +
			"the r.Group(...) that wraps every other route.",
	},
	{
		ID: "D2-undefined-security-scheme",
		Summary: "Every operation references a security scheme named " +
			"`Authorization` that the document never defines, and the document " +
			"declares no root-level security either. The per-operation scope " +
			"arrays are therefore readable facts, but the authentication " +
			"mechanism they attach to is not described anywhere in the " +
			"contract.",
		Evidence: "openapi.yaml components contains parameters, responses, " +
			"requestBodies and schemas only; there is no securitySchemes key " +
			"and no top-level `security:` key. Asserted by " +
			"TestSecuritySchemeIsReferencedButUndefined.",
	},
	{
		ID: "D3-undeclared-query-parameter",
		Summary: "On the five list endpoints the server accepts the filter " +
			"either as a JSON request body or as an undeclared `query` " +
			"query-string parameter. The document declares only the body form. " +
			"The query-string form is the one a browser host can actually " +
			"send, so the transport-portable path exists on the server but is " +
			"invisible to any client generated from this contract.",
		Evidence: "internal/api/utils.go getQueryBuilder reads r.Body first and " +
			"returns query.ParseJSON(r.URL.Query().Get(\"query\")) when the " +
			"body is empty. openapi.yaml declares no `query` parameter on any " +
			"path; components.parameters holds only PageSize, Cursor, PolicyID, " +
			"ReconciliationID, RuleID, EvaluationID and AlertID. Asserted by " +
			"TestNoQueryStringFilterParameterIsDeclared.",
	},
	{
		ID: "D4-healthcheck-undocumented",
		Summary: "The server exposes GET /_healthcheck, which the document does " +
			"not declare. It is not an fctl operation, but it means the " +
			"document is not a complete description of the served surface, so " +
			"absence from the document is not evidence that a route does not " +
			"exist.",
		Evidence: "internal/api/router.go line 35 registers " +
			"r.Get(\"/_healthcheck\", healthController.Check); openapi.yaml " +
			"declares no /_healthcheck path.",
	},
	{
		ID: "D5-version-placeholder",
		Summary: "The document's `info.version` is the literal build-time " +
			"placeholder `RECONCILIATION_VERSION`, never substituted in the " +
			"committed file. The contract therefore carries no product version, " +
			"and the supported-major mapping Task 10B requires has to come from " +
			"the live host-owned /_info response, not from the document.",
		Evidence: "openapi.yaml info.version is `RECONCILIATION_VERSION`. The " +
			"release workflow uploads the file verbatim as a release asset " +
			"(.github/workflows/releases.yml: gh release upload \"$TAG_NAME\" " +
			"./openapi.yaml#openapi.yaml), so the placeholder reaches consumers " +
			"unsubstituted. Asserted by TestDocumentVersionIsAPlaceholder.",
	},
	{
		ID: "D6-scopes-not-enforced-in-service",
		Summary: "The `reconciliation:read` / `reconciliation:write` scope " +
			"arrays are a declared contract, not a reconciliation-service " +
			"check. The service authenticates only; it never inspects scopes. " +
			"An exact-scope catalogue is therefore provable from the document " +
			"but cannot be validated against this service's own behaviour.",
		Evidence: "The repository contains zero occurrences of " +
			"`reconciliation:read` or `reconciliation:write` in Go sources. " +
			"internal/api/router.go line 39 uses auth.Middleware(authenticator) " +
			"only, with no scope argument and no per-route scope binding.",
	},
	{
		ID: "D7-no-sdk-name-overrides",
		Summary: "No operation carries an `x-speakeasy-name-override`, so a " +
			"generated Go SDK method name would derive from the operationId " +
			"alone. This is recorded as a fact rather than a defect: it means " +
			"an adapter may derive the client method from the operationId " +
			"uniformly, with no per-operation exception of the kind the " +
			"Payments surface has.",
		Evidence: "openapi.yaml contains no `x-speakeasy-` extension key of any " +
			"kind. Asserted by TestNoSDKNameOverrides; every record's sdkMethod " +
			"in the golden report equals its operationId.",
	},
}
