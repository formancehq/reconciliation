# fctl Reconciliation plugin — operation inventory and blocking record

This is the hand-written source of truth for the Reconciliation operation
inventory. It states the method, the evidence, and the separation between
proven product facts and the blockers recorded when the inventory was pinned.
The counted tables live in [`operations.generated.md`](operations.generated.md),
which is regenerated from `openapi.yaml` and committed alongside this file.

The product plugin now lives beside this inventory. Its catalogue admits 23 of
the 24 operations the document declares; the host-owned `GET /_info` probe is
the single deliberate exclusion. The implementation uses the public fctl SDK and `producthttp`, and
ships a reconstructible WIT lifecycle component. The pinned audit below remains
the provenance for the operation set and historical CLI mapping.

## Pinned revisions

Every fact below was read at these exact revisions.

| Source | Revision | Role |
|---|---|---|
| `formancehq/reconciliation` | `0221edf2f8727def40368a5e4e2d0d0fafd7d4e7` (`origin/main` at audit time, 2026-09-06) | Base product and server audit revision |
| generated-client `openapi.yaml` | SHA-256 `fa4475f2a100cc21ea021fc94509fedb5ff38bd6fb456b1e8d5cc6f582732a6f` | Current contract including the browser-portable `query` parameter and the `periodType` migration |
| `formancehq/fctl` | `693c58e27865f83332e6c3199d61fed81b742f41` | Legacy CLI baseline: `cmd/reconciliation/` |
| `fctl-v2` | `e9b1395f46f3100b381dbe00f5213de28e6df0e1` | Pinned integration-branch SDK and programme boundary used by the plugin. Reachable from `origin/codex/mvp5-integration`, not from `origin/main` (`01fccf28`), so a default clone of the pinned repository does not contain it until that branch is fetched |

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
| Operations carrying a blocker | 0 |
| First-tranche operations carrying a blocker | 0 |
| Operations with no recorded blocker | 24 |
| First-tranche operations with no recorded blocker | 7 |
| Recorded SDK-generation blockers | 0 |
| Recorded spec-versus-server divergences | 6 |

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
  take a JSON-encoded `QueryBuilder` in the declared `query` parameter;
  `listAlertEvents` is the exception and takes page size only.
- **Streaming: none.** Every operation declares exactly one `2xx` JSON response;
  no operation declares a streaming or chunked media type.

## Blockers

There are no remaining per-operation admission blockers. The former B1 browser
transport blocker was resolved by declaring the server's existing `query`
query-string input in OpenAPI and regenerating the client. The five filtered
list commands now send no GET body. Their JSON is forwarded as an opaque string,
which preserves integers beyond JavaScript's safe range without decoding and
re-encoding them.

## Spec-versus-server divergences

Recorded mismatches and document defects that are not per-operation admission
blockers but change what fctl may assume. Each is asserted by a test that fails
when the claim stops being true.

| ID | Summary |
|---|---|
| **D1** | The document requires `reconciliation:read` on `GET /_info`, but the server registers it above the authenticated group, so it answers unauthenticated. fctl needs the unauthenticated read to learn the major; the server behaviour is the one to rely on and the document is the one to fix. |
| **D2** | Every operation references a security scheme named `Authorization` that the document never defines, and there is no root-level `security` either. The scope arrays are readable facts; the mechanism they attach to is described nowhere. |
| **D4** | The server exposes `GET /_healthcheck`, which the document does not declare. Absence from the document is therefore not evidence that a route does not exist. |
| **D5** | `info.version` is the literal unsubstituted placeholder `RECONCILIATION_VERSION`, and the release workflow uploads the file verbatim. The supported-major mapping must come from the live `/_info` response, not from the document. |
| **D6** | The scope strings appear nowhere in the service's Go sources; `auth.Middleware(authenticator)` authenticates only. An exact-scope catalogue is provable from the document but cannot be validated against this service's behaviour. |
| **D7** | No operation carries an `x-speakeasy-name-override`. Recorded as a fact, not a defect: an adapter may derive the SDK method from the operationId uniformly, with no per-operation exception of the kind the Payments surface has. |

D2 remains the contract defect to address next: authentication is referenced
but its mechanism is not defined.

## Generated Go client

`pkg/client` is generated from this repository's `openapi.yaml` by Speakeasy
1.761.1, pinned in `flake.nix`. `just generate-client` is the sole regeneration
entrypoint and `audit.ClientSpecSHA256` makes an OpenAPI change fail the audit
until the generated client and receipt move together.

The repository-owned generation postprocessor changes the generated generic
JSON decoder to use `json.Decoder.UseNumber`. This preserves free-form numeric
tokens in request `ledgerQuery` and `templateSpec` objects and response
`evidence` and `payload` objects, including integers above 2^53. The isolated
generation check covers the postprocessed result byte-for-byte.

The undefined `Authorization` security scheme remains divergence D2. Speakeasy
can infer a header-shaped security input, but the plugin never configures it:
the adapter constructs the generated client only with the host-bound
`producthttp` client. It likewise supplies no generated retry configuration.
Credentials, endpoint resolution, retry policy and transport therefore remain
host-owned; the generated code owns DTOs, scalars and HTTP serialization.

## Current implementation boundary

- **23 of the 24 declared operations are accepted into the plugin catalogue.**
  `getServerInfo` (`GET /_info`) is the single exclusion: it remains host-owned
  and is not a product command.
- **No authorisation mechanism is invented.** The scope *strings* are quoted
  from the document. The *scheme* is undefined there (D2) and is recorded as
  such rather than guessed.
- **The catalogue declares contract major 1.** That value comes from the
  `reconciliation.v1` operation surface; actual service compatibility remains
  host-owned and must be established by the live `/_info` preflight (D5).
- **The source includes a `producthttp` transport adapter and portable component
  build.** Install, OCI publication, live-service and dual-host acceptance are
  separate release receipts and are not claimed by this inventory.
- **No Proto bindings.** The adapter uses the generated Go client and
  `producthttp`; execution transport remains the portable host ABI. The adapter
  delegates bounded HTTP serialization to the client generated from the pinned
  OpenAPI contract.
- **The current plugin imports only public fctl-v2 SDK contracts.** The pinned
  inventory and its audit package remain independent of runtime internals.
  `fctl-sdk.lock.json` records exact module, repository, commit, SDK NAR hash,
  committed-snapshot NAR hash and canonical WIT hash provenance. The wrapper
  falls back to that committed snapshot when `FCTL_SDK_ROOT` is unset,
  validates that content and any available Git metadata, then uses an ephemeral
  `go.work` replacement; no workstation-specific path is committed. Tidy runs
  through an isolated alternate modfile, removes its temporary SDK replacement
  before comparison or copy-back, and preserves the tracked relative
  Reconciliation client replacement.

## Remaining release evidence

The source now contains the catalogue, adapter, component entrypoint and local
artifact recipe. Release still needs a deterministic component build receipt,
installation and same-byte execution in the native and browser hosts, and one
real read plus one real mutation against a live service with the `/_info`
preflight proven.

The adapter resolves the historical B1 transport risk with the declared
JSON-encoded `query` parameter and never emits a GET body. Opaque continuation
requests then send the cursor alone, without replaying the filter or page size.
