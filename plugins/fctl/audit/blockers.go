package audit

import "sort"

// ProductRevision pins the Reconciliation base commit used for the product and
// server audit. ClientSpecSHA256 separately pins the amended generated-client
// contract because that browser-portability correction is not in this commit.
const ProductRevision = "0221edf2f8727def40368a5e4e2d0d0fafd7d4e7"

// FctlV2Revision pins the fctl-v2 programme tree whose Task 10A/10B text and
// pinned command inventory this preparation is measured against.
const FctlV2Revision = "e9b1395f46f3100b381dbe00f5213de28e6df0e1"

// ClientSpecSHA256 pins the OpenAPI bytes used to generate pkg/client. A spec
// change must regenerate the client and update this receipt in the same change.
const ClientSpecSHA256 = "fa4475f2a100cc21ea021fc94509fedb5ff38bd6fb456b1e8d5cc6f582732a6f"

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
var Blockers = []Blocker{}

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
// generated and committed from this repository. These are
// task-level, not per-operation: they block the generation step itself.
type GenerationBlocker struct {
	ID       string
	Summary  string
	Evidence string
}

// GenerationBlockers is empty because pkg/client is generated reproducibly by
// the Nix-pinned Speakeasy recipe. Contract defects remain Divergences.
var GenerationBlockers = []GenerationBlocker{}

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
			"contract. Speakeasy infers an Authorization API key, but the fctl " +
			"adapter deliberately configures neither generated security nor " +
			"generated retries because the host owns both boundaries.",
		Evidence: "openapi.yaml components contains parameters, responses, " +
			"requestBodies and schemas only; there is no securitySchemes key " +
			"and no top-level `security:` key. Asserted by " +
			"TestSecuritySchemeIsReferencedButUndefined.",
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
