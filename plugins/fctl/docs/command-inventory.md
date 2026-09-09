# fctl Reconciliation plugin — operation inventory and blocking record

This is the hand-written source of truth for the fctl Reconciliation plugin
preparation (fctl-v2 programme Tasks 10A and 10B). It states the method, the
reasoning, the evidence, and the separation between proven facts and blockers.
The counted tables live in [`operations.generated.md`](operations.generated.md),
which is regenerated from `openapi.yaml` and committed alongside this file.

**Nothing in this directory is a plugin.** There is no runtime, no component
entry point, no HTTP client, no generated SDK, no catalogue, and no ABI. See
[What is not claimed](#what-is-not-claimed).

## Pinned revisions

Every fact below was read at these exact revisions.

| Source | Revision | Role |
|---|---|---|
| `formancehq/reconciliation` | `0221edf2f8727def40368a5e4e2d0d0fafd7d4e7` (`origin/main`, 2026-09-06) | The product: `openapi.yaml`, `internal/api/`, `flake.nix`, `Justfile` |
| `formancehq/fctl` | `693c58e27865f83332e6c3199d61fed81b742f41` | Legacy CLI baseline: `cmd/reconciliation/` |
| `fctl-v2` | `8de8c4539ea6664351762dd8dd0e865292e3f216` | Programme plan, Task 10A/10B text, pinned command inventory |

The Reconciliation checkout used was a non-iCloud clone; the iCloud-backed
working copy hangs on `git status`, so it was neither read from nor modified.

## Method

1. Parse `openapi.yaml` — the repository-root document, which the release
   workflow uploads verbatim as a release asset and which no generator writes.
   It is therefore the public product contract, not an internal or stale copy.
2. Extract every operation with only the facts the document states: method,
   path, tag, declared security block and scopes, parameters, request body
   schema, success code and success body schema, deprecation.
3. Transcribe the legacy fctl `reconciliation` command tree at the pinned
   baseline, counting executable leaves only, and pin each command's aliases and
   declaring file.
4. Map each legacy command onto the operationIds its controller actually calls.
5. Classify every operation into exactly one family, with an explicit table so a
   new spec operation fails a test rather than being absorbed by a prefix rule.
6. Derive per-operation risk, and record separately the things the document does
   not prove.
7. Cross-check the document against `internal/api/` and record every divergence.

Steps 1–7 are executable: `audit/` implements them and `cmd/specaudit`
regenerates the committed artefacts. Every number quoted here is derived by that
code, never transcribed by hand.

## Counts

| Quantity | Value |
|---|---:|
| Operations in the document | 24 |
| Unique operationIds | 24 |
| Operations declaring a security block | 24 |
| Operations marked deprecated | 0 |
| Legacy fctl `reconciliation` commands (executable leaves) | 7 |
| Legacy commands mapped onto a current operation | 7 |
| Legacy commands excluded | 0 |
| Operations in the first tranche | 7 |
| Operations recorded but outside the first tranche | 17 |
| Operations reached by the legacy baseline | 7 |
| Operations with no legacy precedent | 17 |
| Operations carrying a blocker | 5 |
| First-tranche operations carrying a blocker | 2 |
| Operations with no recorded blocker | 19 |
| First-tranche operations with no recorded blocker | 5 |
| Recorded SDK-generation blockers | 3 |
| Recorded spec-versus-server divergences | 7 |

## Families and the first tranche

The document uses a single tag, `reconciliation.v1`, so there is no tag-based
partition of the surface. The split that matters is by family.

| Family | Operations | Tranche |
|---|---:|---|
| `policies` | 4 | First tranche |
| `reconciliations` | 3 | First tranche |
| `rules` | 6 | Recorded |
| `evaluations` | 2 | Recorded |
| `alerts` | 8 | Recorded |
| `server-probe` | 1 | Recorded (host-owned) |

**First tranche = policies + reconciliations (7 operations).** Task 10B states
that Reconciliation "covers reconciliations and policies". Those seven
operations are exactly the seven the legacy CLI reached — the tranche adds
nothing to the parity obligation and drops nothing from it. `TestFrozenTranche
MatchesBaseline` asserts that equality.

**Recorded, not frozen (17 operations).** The `rules`, `evaluations` and
`alerts` families are a genuine current product surface with no legacy CLI
precedent and no mention in the Task 10B interface statement. They are
inventoried here so the later catalogue decision starts from facts, but
admitting them is a scope decision that belongs to whoever owns Task 10B, not to
this preparation.

`getServerInfo` is recorded separately because the fctl host performs the
`GET /_info` probe itself to resolve the product major before any plugin runs.
It is not a plugin operation.

## Legacy baseline

Seven executable leaves at fctl `693c58e2`. The two grouping-only nodes —
`reconciliation` (`cmd/reconciliation/root.go`, built with
`fctl.NewStackCommand`, no controller) and `reconciliation policies`
(`cmd/reconciliation/policies/root.go`, alias `p`) — are not commands and are
not counted.

| Legacy command | Aliases | operationId |
|---|---|---|
| `reconciliation list` | `ls`, `l` | `listReconciliations` |
| `reconciliation get <reconciliationID>` | `sh`, `s` | `getReconciliation` |
| `reconciliation policies list` | `ls`, `l` | `listPolicies` |
| `reconciliation policies get <policyID>` | `sh`, `s` | `getPolicy` |
| `reconciliation policies create <file>\|-` | `cr`, `c` | `createPolicy` |
| `reconciliation policies delete <policyID>` | `d` | `deletePolicy` |
| `reconciliation policies reconcile <policyID> <atLedger> <atPayments>` | `r` | `reconcile` |

All seven map onto a current operation. **No legacy command is excluded, and no
operation is deprecated**, so this surface has no exclusion or deprecation
argument to make — unlike Payments, where deprecated connector paths and
version-branching commands both had to be justified.

Each legacy controller calls a single operation through
`stackClient.Reconciliation.V1.*`. There is no version-probing branch, so unlike
Payments there is no pair of legacy-and-current operationIds per command.

## Risks

Derived per operation and rendered in the generated tables.

- **Destructive: 2.** `deletePolicy` and `deleteRule`, both `DELETE`. Nothing
  else removes or resets state.
- **State transitions: 5.** `ackAlert`, `acceptAlert`, `resolveAlert`,
  `snoozeAlert`, `unsnoozeAlert` change an alert's state without removing it.
  They are not destructive, but they are `POST` and therefore not replay-safe,
  so they are named rather than lumped in with plain writes.
- **Secrets: none.** No declared schema exposes a credential-shaped property,
  in either direction. The domain objects are policies, reconciliations, rules,
  evaluations and alerts; the service holds no PSP or provider credentials.
  Asserted by `TestNoSecretBearingSchemaProperty`.
- **Display-once: none.** No success body carries a one-shot value such as a
  minted link or generated secret. The table is kept explicit so a future
  one-shot value has an obvious place to be recorded.
- **Idempotence: no operation is protected.** The document declares no
  `Idempotency-Key` parameter or header anywhere, so **no `POST` on this
  surface is replay-safe** — including `reconcile`, which launches a
  reconciliation run, and the five alert transitions. The only idempotency key
  in the repository is `internal/events/alert.go`, which stamps an *outbound*
  event envelope for downstream consumer dedup; it gives an fctl caller no
  replay protection.
- **Pagination: 6 operations** expose `cursor` + `pageSize`. Five of them also
  take a `QueryBuilder` filter body; `listAlertEvents` is the exception and
  takes page size only.
- **Streaming: none.** Every operation declares exactly one `2xx` JSON response;
  no operation declares a streaming or chunked media type.

## Blockers

A blocker is a recorded reason an operation cannot yet be admitted into a plugin
catalogue. It is deliberately kept separate from the facts above.

### B1 — GET with a request body (5 operations)

`listPolicies`, `listReconciliations`, `listRules`, `listEvaluations`,
`listAlerts` carry their filter in a JSON request body (the free-form
`QueryBuilder` object) on a `GET`.

A host transport that drops or forbids GET request bodies silently degrades
these into **unfiltered listings** — a wrong answer rather than an error. The
browser `fetch` API forbids a body on `GET` outright, so this is load-bearing
for the programme's dual-host requirement rather than theoretical. The request
boundary has to be proven to preserve GET bodies, or the undeclared query-string
form (divergence D3) has to be added to the contract, before these are admitted.

Two of the five are in the first tranche (`listPolicies`,
`listReconciliations`), which is why the first tranche has 5 unblocked
operations out of 7.

Evidence: each operation declares
`requestBody.content.application/json.schema: QueryBuilder` alongside method
`GET`. The server reads it in `internal/api/utils.go` `getQueryBuilder`, which
calls `io.ReadAll` on `r.Body` (bounded to 1 MiB by `maxQueryBuilderBodySize`)
before falling back to the undeclared `query` query-string parameter.

## Spec-versus-server divergences

Recorded mismatches and document defects that are not per-operation admission
blockers but change what fctl may assume. Each is asserted by a test that fails
when the claim stops being true.

| ID | Summary |
|---|---|
| **D1** | The document requires `reconciliation:read` on `GET /_info`, but the server registers it above the authenticated group, so it answers unauthenticated. fctl needs the unauthenticated read to learn the major; the server behaviour is the one to rely on and the document is the one to fix. |
| **D2** | Every operation references a security scheme named `Authorization` that the document never defines, and there is no root-level `security` either. The scope arrays are readable facts; the mechanism they attach to is described nowhere. |
| **D3** | On the five list endpoints the server also accepts the filter as an undeclared `query` query-string parameter. The document declares only the body form — so the one transport-portable path is invisible to any client generated from this contract. |
| **D4** | The server exposes `GET /_healthcheck`, which the document does not declare. Absence from the document is therefore not evidence that a route does not exist. |
| **D5** | `info.version` is the literal unsubstituted placeholder `RECONCILIATION_VERSION`, and the release workflow uploads the file verbatim. The supported-major mapping must come from the live `/_info` response, not from the document. |
| **D6** | The scope strings appear nowhere in the service's Go sources; `auth.Middleware(authenticator)` authenticates only. An exact-scope catalogue is provable from the document but cannot be validated against this service's behaviour. |
| **D7** | No operation carries an `x-speakeasy-name-override`. Recorded as a fact, not a defect: an adapter may derive the SDK method from the operationId uniformly, with no per-operation exception of the kind the Payments surface has. |

D2 and D3 are the two that a contract fix should address first: together they
mean the document neither describes how to authenticate nor describes the only
filter form a browser host can send.

## Why no Go SDK was generated

Task 10A instructs generating `github.com/formancehq/reconciliation/pkg/client`.
It was **not** generated in this tranche, for three recorded reasons. This is a
recorded decision, not an omission; `TestGenerationBlockersAreRecorded` fails if
the record is removed.

### G1 — MVP4 gates are open

The programme sequences Task 10A after MVP4, and MVP4 is not accepted: the Task
4B portable runtime cutover, the Task 4C example replay, and the Task 4D
deletion of the native gRPC and browser Go-WASM paths are all still open, and
every Task 10A acceptance box is unchecked. Generating and committing an SDK now
would pin a client shape against an unfrozen adapter and capability boundary.

### G2 — Speakeasy is absent from the declared toolchain

Task 10A requires `just generate-client` to regenerate the SDK inside the
declared Nix environment **without a globally installed Speakeasy binary**. This
repository's `flake.nix` provides `ginkgo`, `go_1_26`, `gotools`, `just`,
`golangci-lint` and `goreleaser-pro` — no Speakeasy. The `Justfile` declares no
client recipe. The only Speakeasy invocation in the repository is a CI job
authenticated by the `SPEAKEASY_API_KEY` repository secret. The generation
therefore cannot be reproduced locally or offline without introducing a
credential this preparation must not use.

### G3 — The contract cannot express its own authentication

All 24 operations declare `security: [{Authorization: [...]}]` against a scheme
the document never defines (D2). A generator has no mechanism, type or header
binding to emit for `Authorization`, so the contract should be repaired before a
faithful SDK is produced. This does **not** block the plugin catalogue — Task
10A requires generated auth to stay disabled and the fctl host owns credentials
— it blocks the generation step producing something faithful.

**What Task 10A work this tranche does complete:** its first checkbox, the
source audit. The authoritative OpenAPI path is proven to be the public product
contract (repository root `openapi.yaml`, uploaded verbatim as a release asset,
written by no generator), the `origin/main` commit is pinned, and the
generation-blocking properties of that contract are recorded with tests.

## What is not claimed

- **No operation is accepted into a plugin catalogue.** The inventory records
  proven facts and separately records blockers. "19 operations with no recorded
  blocker" is not an admission claim; the runtime gates are separate and open.
- **No authorisation mechanism is invented.** The scope *strings* are quoted
  from the document. The *scheme* is undefined there (D2) and is recorded as
  such rather than guessed.
- **No product version or supported major is asserted.** The document carries a
  placeholder (D5); the major must come from a live `/_info` response.
- **No transport, component build, OCI installation, or dual-host behaviour** is
  claimed, prepared, or gated here.
- **No generated SDK, no `pkg/client`, no Proto bindings.** See G1–G3.
- **No fctl-v2 code is imported**, and nothing here depends on fctl-v2 at all.
  The module's only dependency is `gopkg.in/yaml.v3`.

## Gates remaining before a portable component

In programme order:

1. **Task 4B** — portable WASM runtime cutover, both guests across both hosts.
2. **Task 4C** — accepted commands and examples replayed against 4B.
3. **Task 4D** — native gRPC and browser Go-WASM paths deleted; MVP4 accepted.
4. **Task 10A** — public Go SDK generated and committed, which additionally
   needs G2 (Speakeasy in the declared toolchain) and should have G3 (a defined
   security scheme) resolved first.
5. **Task 10B** — catalogue, adapter, component entry point, local artefact
   recipe, and one real read plus one real mutation integration scenario against
   a live service, with the `/_info` preflight proven.

B1 must also be resolved — by proving the request boundary preserves GET bodies,
or by adding the query-string filter form to the contract — before the two
blocked first-tranche list operations can be admitted.
