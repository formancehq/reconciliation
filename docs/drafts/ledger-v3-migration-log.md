# Ledger-native migration — development log

Traceability record for the Reconciliation → Ledger v3 storage migration. One row per
phase/step with status + commit; SDLC reviews and tracked follow-ups below. Design lives in
the [RFC](./rfc-ledger-native-storage.md) and [ADR-002](../prd/adr-002-pit-consistency.md).

**Branch:** `feat/reconciliation-ledger-v3` (based on `feat/ledger-clarity-v1`; rebase onto
`main` after PR #83 merges). Dedicated PR to follow.

---

## 👋 Handoff — resume here

**Done:** Phase 1 steps 0–4 + **5a** (checkpoint mechanism, `6a7e110`) + **6a-1** (secure ledger
transport, `5f4ab4b`). The `LedgerStore` implements the full rules + alert-lifecycle surface; the
`CheckpointReader` reads data ledgers at a query checkpoint (ADR-002 cut, it-proven). Chart is
**finalized** — 4 account types, per-rule pool, numscripts in the ledger library (don't re-litigate;
see §4.1.1/§4.1.2 + the phase table).

**Scope decided with the owner (2026-07-04) — see "Step 6 — scope decisions" below.** The goal is
a **Postgres-free** reconciliation: the ledger becomes the *sole* `Store` (no composite, no dual-run
flag). This drops the legacy policy/cash-pool surface, makes evaluations non-durable, and evaporates
step 6b's Postgres migration. Step 6 splits into **6a** (non-invasive: transport + simplification +
ledger-only wiring) then **6b** (invasive engine flip).

**Next: finish step 6a (ledger-only Store).** 6a-1 ✅ done. Remaining:
- **6a-2** — drop legacy `/policies` + `/reconciliations` (routes, handlers, `Policy`/`Reconciliation`
  models, 7 Store methods) + the `ledger_vs_pool_drift` template (pool-tied, subsumed by
  `source_parity`). Keep templates: `source_parity`, `ledger_invariant`, `account_threshold`.
- **6a-3** — evaluations non-durable (§4.4.2): drop `Create/Get/List Evaluation` + `/evaluations`
  routes; `EvaluateRule` writes `last_evaluation` onto the alert/rule (removes the cross-store `inTx`
  concern — no separate evaluation write).
- **6a-4** — `ListAlertEvents` → empty + documented TODO (SAVED_METADATA sink in Phase 3-4).
- **6a-5** — bind `LedgerStore` as the sole `service.Store` (`var _ service.Store`), wire
  `ledger.Client` via `ledgerauth` (fx provider + flags, provision `_recon` at boot, `Ping`→ledger),
  **remove the Postgres wiring** from `serve.go` → boot DB-less. Closes the wiring half of F2.

**Then step 6b (invasive, engine flip — absorbs 5b).** Change `engine.LedgerResolver`
(`AggregateBalance`/`ListAccounts`) `pit`→`checkpointID`. Design fork to settle with the owner:
(A) split into two resolver interfaces + per-source Tier-1/Tier-2 dispatch, or (B) a `ReadAnchor`
union. `CheckpointReader` is already the Tier-1 signature but **lacks `ListAccounts`** (add it);
`SDKLedgerResolver` stays Tier-2 (pit/latest). Service pins ONE checkpoint per evaluation
(`AcquireCheckpoint` → `Evaluate` → `Release`, cancellation-surviving ctx — **F26**). **No Postgres
migration** — the `checkpointID` anchor lands on `alert:item` (`last_evaluation`), not a column. Add a
reaper/ring for orphaned checkpoints (**F26**).

**Watch:** open findings F1/F2/F8/F17/F22/F23/F25/F26/F27 (details below). **Don't touch:**
`feat/ledger-clarity-v1`; untracked V1 files (`docs/drafts/v1-epic-*`, `v1-stories/`); the
uncommitted `Justfile` change (orphaned `generate-ledger-proto`, leave unstaged); `ledger-local/`.
**Build/test:** `export PATH=$PATH:$(go env GOPATH)/bin` then `GOROOT= go build ./...`,
`GOROOT= go test -race ./internal/ledger{,store,schema}/...`, it-tests `GOROOT= go test -tags it
-p 1 -run TestIntegration ./internal/ledgerstore/... ./internal/ledger/...` (**`-p 1`**: packages
share one live ledger; F8: bump the it control-ledger name — now `recon-it4` — on any chart change). Conventions: `feat(ledger-v3):` commits, update this log
+ SDLC review per sub-step, stamp commit refs.

---

## Phase status

| Phase | Step | Scope | Status | Commit |
|---|---|---|---|---|
| 0 | — | Reads first: point "pool" at a ledger, PIT → checkpoints | ⬜ todo | — |
| **1** | **0** | **Ledger v3 gRPC transport** (proto + BucketService client) | ✅ done · reviewed | `5583a69` |
| 1 | 1 | Chart-of-accounts / schema definition (`internal/ledgerschema`) | ✅ done | `c51be9a` |
| 1 | 2 | Bootstrap provisioner (CreateLedger + account-types AUDIT + typed metadata + prepared queries) | ✅ done | `bd95a35` |
| 1 | 3 | `LedgerStore` behind the `Store` interface (rules/alerts as Numscript batches) | 🚧 in progress | `8e75db4` |
| 1 | 3a | ↳ store skeleton + rule serialization + CreateRule/GetRule | ✅ done | `8e75db4` |
| 1 | 3b | ↳ PatchRule/DeleteRule (+ `ParseRuleAccount`, `DeleteAccountMetadata`) | ✅ done | `d023826` |
| 1 | 3c | ↳ alert lifecycle | ✅ done | — |
| 1 | 3c-1 | ↳ address reorder (per→fp), `MetaID`, `Alert↔metadata` serialization (OCC from balance, status mirror) | ✅ done | `f959ea1` |
| 1 | 3c-2 | ↳ `OpenOrUpdateAlert` (mint from pool → st:open, OCC, mirror, idempotency) | ✅ done · reviewed | `66b64e2` |
| 1 | 3c-2b | ↳ chart merge (`alert:issued`+`alert:occ` → `alert:pool`, 5→4 types) + Numscript **library** (SaveNumscript + ScriptReference) | ✅ done | `739efe7` |
| 1 | 3c-3 | ↳ alert lifecycle: reads + guarded transitions + snooze | ✅ done | — |
| 1 | 3c-3a | ↳ id→address resolution (`QueryAccounts` stream + `findAlertItem`, `id` metadata index) + `GetAlert` | ✅ done | `59e4d7b` |
| 1 | 3c-3b | ↳ guarded transitions (Ack/Resolve/Accept/AutoResolve) + `ListActiveAlertFingerprints` + `alert_move` script | ✅ done | `9cc6025` |
| 1 | 3c-3c | ↳ Snooze/UnsnoozeAlert (metadata-only) | ✅ done | `32603fa` |
| 1 | 3c-4 | ↳ burn-on-close (resolve burns the marker → pool → EPHEMERAL purge; reopen re-mints; markers only for active states) | ✅ done | `2f3ffc3` |
| 1 | 4 | Filter translator (`query.Builder`→filter) + **`ListRules`/`ListAlerts`** (ListAccounts streaming + trailer cursor → `bunpaginate.Cursor`) | ✅ done · reviewed | `916fee3` |
| 1 | 5 | Resolver change `pit` → `checkpointID` + checkpoint acquisition | 🚧 mechanism done | — |
| 1 | 5a | ↳ checkpoint mechanism: client (`CreateQueryCheckpoint`/`Delete` + `AggregateVolumes`) + `Checkpoint` lifecycle + `CheckpointReader` (data-ledger reads at a checkpoint) | ✅ done | `6a7e110` |
| 1 | 5b | ↳ engine interface flip (`LedgerResolver` pit→checkpointID) + anchor + service acquisition | ⬜ folded into step 6b | — |
| 1 | **6a** | **Ledger-only `Store`** (transport + simplification + wiring; Postgres removed) | 🚧 in progress | — |
| 1 | 6a-1 | ↳ secure transport (`internal/ledgerauth`: Ed25519 signing + TLS + F2 insecure guard) | ✅ done · reviewed | `5f4ab4b` |
| 1 | 6a-2 | ↳ drop legacy `/policies`+`/reconciliations` + `ledger_vs_pool_drift` template (+ openapi) | ✅ done · reviewed | `299b7a7` |
| 1 | 6a-2b | ↳ sync product docs to the ledger-only surface (delete v1-vs-legacy, purge legacy refs) | ✅ done | `7acda74` |
| 1 | 6a-3 | ↳ evaluations non-durable — drop the read surface (`Get/ListEvaluation` + `/evaluations`); `CreateEvaluation` kept (no-op on ledger @ 6a-5) | ✅ done · reviewed | `e282f78` |
| 1 | 6a-4 | ↳ `ListAlertEvents` → empty + TODO (SAVED_METADATA sink deferred) | ⬜ todo | — |
| 1 | 6a-5 | ↳ bind `LedgerStore` as sole `Store` + `ledger.Client` fx/flags + remove Postgres (boot DB-less) | ⬜ todo | — |
| 1 | 6b | engine flip (`LedgerResolver` pit→checkpointID) + per-source dispatch + checkpoint acquisition (no migration) | ⬜ todo | — |
| 2 | — | Flip reads to the ledger; Postgres as shadow | ⬜ todo | — |
| 3 | — | Drop Postgres + own message bus (ledger event sink) | ⬜ todo | — |
| 4 | — | Semantic events / replay (generic event-log) | ⬜ todo | — |

Docs baseline commit: `c54dc4d` (RFC + ADR-002 rewrite).

**✅ Integration-validated (2026-07-03)** against a live Ledger v3.0.0-alpha.3 (local, insecure
`127.0.0.1:8888`): `store_it_test.go` (`-tags it`) provisions the control-ledger and runs the
full rule CRUD (create → get → patch+label-prune → delete → not-found). `ledgerctl account-types
list` confirms the 5 account types landed with correct patterns (alert-state=EPHEMERAL). Proves
the gRPC transport, provisioner, and typed metadata round-trip end-to-end.

---

## Reviews

### Phase 1 step 0 — SDLC review (2026-07-03)

**Scope:** `5583a69` — non-generated files (`internal/infra/ledger/client.go`, `justfile`,
`proto/ledger/*`, `go.mod`); generated `internal/ledgerpb/*` inspected for handling only.
**Checks:** `go build ./...` ✅ · `go vet ./internal/infra/ledger/...` ✅ · `golangci-lint` 0
issues ✅ · conventional commit ✅ · not on `main` ✅ · additive only (no existing code changed
→ no regression to the Postgres path) ✅ · no OpenAPI change (internal transport) ✅.

**Findings** (severity · status):

| # | Sev | Finding | Action | Status |
|---|---|---|---|---|
| F1 | HIGH | CI must regenerate proto: `just generate` now depends on `generate-ledger-proto` (needs `protoc` + `protoc-gen-go`/`-grpc`/`-vtproto` in the Nix env). Without pinned plugins the dirty-check can fail or drift. | Add protoc + pinned gen plugins to the Nix flake; pin versions matching ledger-connect. | ⬜ open |
| F2 | HIGH | Transport defaults to `insecure.NewCredentials()`. Reconciliation writes financial-control data → prod must enforce **TLS + Ed25519 request signing**; insecure must not be a silent prod default. | Wire TLS + signing in config (step 6); refuse insecure outside local/dev. | ⬜ open |
| F3 | MED | No unit tests on the wrapper (Formance: tests mandatory, 80% target). | **Integration-tested** — `store_it_test.go` (`-tags it`) exercises Apply/CreateLedger/SaveAccountMetadataValues/GetAccount/DeleteAccountMetadata against a real ledger. bufconn unit test still nice-to-have. | 🟡 partially (it-test) |
| F4 | MED | Confirm golangci excludes generated `internal/ledgerpb/*` (files carry `// Code generated by protoc` headers → auto-skipped by default, but verify CI config). | Verify/adjust lint config for generated dirs. | ⬜ open |
| F5 | MED | Proto re-sync procedure undocumented (manual copy from ledger-connect @ ledger version). | Document "how to update the ledger proto" (see below). | ✅ documented here |
| F6 | LOW | `internal/infra/ledger/` deviates from recon's flat `internal/<domain>` layout (recon has no `infra/`). Mirrors ledger-connect, but inconsistent with host repo. | Moved to `internal/ledger/` (flat layout). | ✅ resolved (`git mv`) |
| F7 | LOW | `GetAccount` returned the raw gRPC error (unwrapped) vs wrapping elsewhere. | Wrapped: `fmt.Errorf("get account %s@%s: %w", …)`. | ✅ resolved |

**Verdict:** solid scaffolding step, safe to build on. No CRITICAL. The two HIGH items (F1 CI
toolchain, F2 secure transport) must be closed before this leaves POC / before a shared
environment; both are deferred to later steps (CI setup / step 6 config), not blockers for
steps 1–5.

---

### Phase 1 step 2 — provisioner (notes)

`internal/ledger/provisioner.go` — idempotent bootstrap: `CreateLedger` (metadata schema +
account types + **AUDIT** enforcement) then registers the fixed prepared queries. QueryFilter
protos built by hand (`ledgerschema.Filter*`, option (a)) since the ledger's filterexpr text
parser is server-side. Unit-tested with gomock (`provisionAPI` interface + generated mock) and
the filter builders/prepared-queries proto shapes. build/vet/lint/gofmt clean.

Tracked follow-ups:

| # | Sev | Finding | Status |
|---|---|---|---|
| F8 | MED | Schema **evolution** on an already-created ledger is not handled: `CreateLedger` applies the full schema only on first boot; adding a metadata field / account type later needs idempotent `SetMetadataFieldType` / `AddAccountType` passes in `Provision`. | ⬜ open (POC creates once) |
| F9 | LOW | Provisioner has no integration test against a real ledger (only gomock unit tests). | ✅ resolved — `store_it_test.go` provisions the control-ledger against a live ledger. |

Step 2 SDLC review (2026-07-03) — coverage: `ledgerschema` 93.5%, provisioner 83–100% (the
`ledger` package's 39.3% is the untested gRPC wrapper, = F3); lint/vet/gofmt clean; conventional
commit; not on main; no OpenAPI change; `CreateLedger` sig change has no external callers.

| # | Sev | Finding | Status |
|---|---|---|---|
| F10 | MED | No compile-time proof that `*Client` satisfies `provisionAPI`. | ✅ resolved — `var _ provisionAPI = (*Client)(nil)` |
| F11 | MED | Provisioner happy-path test matched schema args with `gomock.Any()` → would pass on an empty/wrong chart. | ✅ resolved — `DoAndReturn` asserts `alert-state`=EPHEMERAL + non-empty schema + both PQ names |
| F12 | LOW | Two exported helpers are unused until step 4 (`FilterMetadataString`, `IssuedByRulePrefix`) — speculative surface; keep only if step 4 consumes them, else drop (YAGNI). | ⬜ open |

---

### Phase 1 step 3a — LedgerStore rules (SDLC review, 2026-07-03)

Coverage `ledgerstore` 88.3% (>80%); lint/vet/gofmt/-race clean; conventional commit; not on
main; no OpenAPI change; typed round-trip + gomock create/get/not-found tests. Solid — no
CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| F13 | MED | `CreateRule` is an LWW **upsert** vs Postgres create-fails-on-duplicate. | ✅ resolved (documented) — doc comment states the upsert semantics; guarding would cost a read per create and IDs are generated UUIDs. |
| F14 | MED | Address **parse** logic duplicated the `rule:{id}` format. | ✅ resolved — `schema.ParseRuleAccount` (inverse of `RuleAccount`), used by `ruleFromAccount`. |
| F15 | LOW | `ruleToMetadata` swallowed `json.Marshal` errors. | ✅ resolved — now returns `(map, error)`; callers propagate. |
| F16 | LOW | `ledgerstore` imports `internal/storage` only for `ErrNotFound` → heavy dep for one sentinel. Consider a leaf errors package shared by both stores. | ⬜ open |

---

### Phase 1 step 3c-2 — OpenOrUpdateAlert (SDLC review, 2026-07-03)

**Checks:** `go build ./...` ✅ · `go vet` ✅ · `golangci-lint run --build-tags it` 0 issues ✅ ·
`gofmt` clean ✅ · `go test -race` (unit) ✅ · it-test green against live ledger ✅ · `go mod
tidy` clean ✅ · conventional commit `feat(ledger-v3)` ✅ · not on `main` ✅ · no OpenAPI change
(internal storage) ✅ · `OpenAlertResult`/`OpenAlertInput` signatures unchanged → no regression
to the Postgres path or the `Store` interface ✅ · mock regenerated (`go generate`) ✅. Coverage
`ledgerstore` 84.7%, `ledgerschema` 84.6% (both >80%). No CRITICAL/HIGH.

`internal/ledgerstore/alert.go` — the dedup-aware failing-outcome write, ported from the
Postgres three-path model. Client gains `CreateTransaction` (Numscript + atomic
`account_metadata` set/delete under an idempotency key); the guarded/overdraft Numscript
fragments live in `internal/ledgerschema/scripts.go` (shared with 3c-3's ack/resolve).

Flow: read the item account → branch. **new-open** mints the ALERT marker from the issuance
pool (overdraft) into `st:open` + the first OCC unit, sets the descriptive metadata + `status`
mirror (`Created`). **repeat** (marker already at `st:open`) mints one OCC unit + refreshes
`last_seen`/evidence. **reopen** (`st:resolved`) / **resurface** (`st:ack`) do a *guarded*
marker move `st:{from}→st:open` (bare source = CAS, fails the batch if the marker moved),
+OCC, mirror back to OPEN; a reopen also deletes the prior `resolution`/`ack` keys (only those
present — the ledger rejects deleting an absent key). Marker (guarded source-of-truth) +
`status` mirror land in one atomic batch, so they never diverge. Idempotency key =
`sha256(len-prefixed rule|fingerprint|period|evaluationID)`.

Tests: gomock unit tests for all four branches (new/repeat/reopen/resurface) + key
determinism; `scripts_test.go` golden-tests the Numscript; it-test opens an alert against the
live ledger and asserts marker `st:open`=1, OCC counter, `status`/`id` mirror, issuance pool
gauge=−1, a real repeat (OCC→2, still one marker), and client-level idempotent replay.
Coverage: `ledgerstore` 84.7%, `ledgerschema` 84.6%. build/vet/lint(0)/gofmt/-race clean.

| # | Sev | Finding | Status |
|---|---|---|---|
| F17 | MED | Ledger idempotency is **content-sensitive**: same key + *different* batch content → `AlreadyExists` ("key used with different request content"), not a replay. Safe in the current flow — `LedgerStore` has no `RunInTx`, so `Service.inTx` runs one attempt and each `OpenOrUpdateAlert(rule,fp,period,eval)` is submitted once; only gRPC retransmits (identical batch) replay, and those dedup cleanly. **But** if a full-evaluation retry is ever introduced, a re-call that re-reads advanced state builds a *different* batch under the same key → spurious error. Then `OpenOrUpdateAlert` must catch `codes.AlreadyExists` and return the committed state as an idempotent success. | ⬜ open (deferred; no retry today) |
| F18 | LOW | On an idempotent-replay of the *same* evaluation (only reachable via the retry in F17), the returned `Alert.ID` (new UUID) / `OccurrenceCount` (read+1) would not match the deduped ledger state. Callers (`driveAlerts`) discard the result; `openEngineErrorAlert` uses only `Alert`. Resolve together with F17 (re-read on conflict). | ⬜ open (deferred) |
| F19 | LOW | Marker↔mirror consistency is a **recon-level invariant** (both written atomically here), not ledger-checker-verified — the ledger checker does not validate recon's projections. A future recon self-check could compare `status` mirror vs marker position. | ⬜ open (POC accepts) |

### Phase 1 step 3c-2b — chart merge + Numscript library (2026-07-03)

Two refinements requested before 3c-3, done together (both touch the scripts + chart).

**Chart merge (5 → 4 account types).** `alert:issued` (ALERT source) and `alert:occ` (OCC
source) collapse into a single `alert:pool:rule:{id}:per:{period}` (NORMAL) that mints **both**
assets. Rationale: the isolation was neither EPHEMERAL-driven (both pools were NORMAL — only the
`alert:st:*` markers are EPHEMERAL) nor required by multi-asset limits (an account holds ALERT
and OCC with independent balances). Both free gauges survive: `−balance(pool, ALERT)` = live
alerts, `−balance(pool, OCC)` = total occurrences (the latter also derivable by aggregating item
OCC). Chose merge over dropping the pool entirely (mint from `@world`) to keep the per-
(rule,period) scoped gauges and avoid depending on `@world`'s STRICT treatment.

**Numscript library.** The three transition programs are now registered in the ledger's
numscript library at provisioning (`SaveNumscript`, validated at save time, pinned `v1.0.0`,
immutable) and referenced by name + account `vars` (`ScriptReference`) instead of inlining the
source per `CreateTransaction`. Programs: `alert_open` (mint marker + OCC), `alert_bump` (OCC
only), `alert_reopen` (guarded move + OCC). One `script_reference` resolves ONE program, so the
fragment-composition (`joinScripts`) is gone; each operation is a complete parameterised script.
Bump the semver + the reference in lockstep when a program changes.

Touched: `ledgerschema` (addresses `PoolAccount`/`PoolByRulePrefix`; schema `AccountTypeAlertPool`;
`scripts.go` → `Numscripts()` library defs + var-name consts), `ledger` (`CreateTransactionInput`
→ ScriptReference; provisioner registers numscripts, `SaveNumscript` added to `provisionAPI` +
mock), `ledgerstore` (alert.go uses ScriptName+Vars). Tests updated (unit + golden numscript +
provisioner SaveNumscript ×3); it-test on a fresh `recon-it2` ledger validates the 4-type chart,
the 3 registered numscripts, and the full lifecycle. build/vet/lint(0, `--build-tags it`)/gofmt/
-race clean. Verified via `ledgerctl account-types list` (4 types incl. `alert-pool`) and
`numscripts list`/`get` (3 programs @ v1.0.0). RFC §4.1.2/§4.1.3 updated.

Finding F12 (unused exported helpers) update: `IssuedByRulePrefix` → `PoolByRulePrefix` (still
unused until step 4's filter/list work; `FilterMetadataString` idem). Keep pending step 4; drop
if unconsumed.

### Chart finalized (2026-07-03) — item / state kept separate

Reviewed whether `alert:item:*` (canonical, NORMAL) and `alert:st:{state}:*` (marker, EPHEMERAL)
should merge. **Decision: keep separate.** They resolve a genuine addressing conflict: a
Numscript CAS guard keys off address+asset, so the state must sit at an address that *changes*
per transition (open→ack→resolved) — while metadata needs a *stable* address (permanent record +
O(1) point-read). One address can't be both moving and fixed → two accounts. Bonus: EPHEMERAL
auto-purges drained state accounts, and it matches the Payments-plugin idiom (value moves between
state accounts). Unlike the pool merge (which removed a *redundant* account), this separation is
*structural* and stays.

**Considered & rejected — Option G (state-as-asset, one account):** encode state as a burnable
asset (`S_OPEN`/`S_ACK`/`S_RESOLVED`) on the item; guard via burn instead of move; counts via
`−balance(pool, S_state)`. It would cut to 3 account types (drop `alert:st:*`), ~halve live
accounts, and remove EPHEMERAL — preserving guards + counts. Rejected because at recon's low
control-plane write volume those wins are marginal, while it loses the self-describing chart
(§4.1.3) and diverges from the platform idiom, and adds a posting per transition. Revisit only if
minimising account count / removing EPHEMERAL becomes a goal. Metadata-only (no marker) stays
rejected (loses the atomic guard, §4.1.1).

**Final chart — 4 account types:** `rule:{id}` (NORMAL) · `alert:item:rule:{id}:per:{p}:fp:{h}`
(NORMAL, metadata + OCC + status mirror) · `alert:st:{state}:rule:{id}:per:{p}:fp:{h}` (EPHEMERAL,
ALERT marker) · `alert:pool:rule:{id}:per:{p}` (NORMAL, sources ALERT + OCC). Assets: `ALERT`,
`OCC` (both precision 0). This is the baseline for 3c-3.

### Phase 1 step 3c-3a — id resolution + GetAlert (2026-07-03)

The Store addresses alerts by UUID (`GetAlert`/`AckAlert`/… take an `id`), but the ledger keys
items by `rule/period/fp`. The only id→address path is a metadata lookup on the indexed `id`
field. Added: client `QueryAccounts` (streams `ListAccounts` with a `QueryFilter`, collects — for
bounded sets; step 4 adds the cursor-paginated public lists), schema `FilterAny`/`ItemPrefix`/
`ItemByRulePeriodPrefix`, store `findAlertItem`+`GetAlert`. `ledgerClient` gains `QueryAccounts`
(mock regenerated). Tested: gomock (found / not-found) + it-test resolves the opened alert by id.

| # | Sev | Finding | Status |
|---|---|---|---|
| F20 | MED | `SetMetadataFieldType` declares a field's TYPE but does **not** make it queryable — a `metadata[k]==v` filter needs an explicit `CreateIndex` (else `FailedPrecondition: index not found`). The RFC §4.3.1 wording ("SetMetadataFieldType … builds its forward index") is misleading. Fixed: the provisioner now creates the `id` account-metadata index (`schema.MetadataIndexes()`); it builds async and a query gets `codes.Unavailable` (INDEX_BUILDING) until ready — absorbed by the client retry policy. **Step 4 must add indexes for every field its lists filter on** (status, severity, rule_id, period, enabled) before executing those queries. | 🟡 id done; step-4 fields pending |
| F21 | LOW | `QueryAccounts` collects the whole stream in memory — fine for id lookup (≤1) and the per-(rule,period) sweep, but the public `ListAlerts`/`ListRules` (step 4) MUST stream with the opaque `x-next-cursor` trailer instead. | ⬜ open (step 4) |

### Phase 1 step 3c-3b — guarded transitions + active-fingerprint sweep (2026-07-03)

`internal/ledgerstore/alert_transition.go` — the guarded {ack, resolve, accept, auto-resolve}
transitions, all built on a new `alert_move` library script (a pure guarded marker move
`st:{from}→st:{to}`, no OCC — the bare source is the CAS). Each runs as one atomic, idempotent
`CreateTransaction`: the marker move + the `status` mirror / resolution / ack metadata set (and a
live snooze deleted on close) land together.

Semantics ported from the Postgres store: **Ack** OPEN→ACK (idempotent no-op on already-ACK,
`ErrNotFound` on RESOLVED); **Resolve/Accept** {OPEN,ACK}→RESOLVED (`ErrNotFound` if already
resolved; Accept requires a note); **AutoResolve** structural by (rule,fp,period), no-op `(nil,
nil)` when nothing active. `ListActiveAlertFingerprints` scans the (rule,period) item accounts by
address prefix (builtin index) and filters status client-side — the raw fingerprint lives on the
item, not the hash-only marker address, so no status index is needed.

Idempotency keys now carry an **action discriminator** (`alertActionKey("ack"|"resolve"|
"accept"|"autoresolve"|"openorupdate", …)`) so distinct operations on the same entity never share
a key (which, under the content-sensitive idempotency of F17, would conflict). Operator actions
key on the action timestamp (ack.At / resolution.At) — a gRPC retransmit dedups; a genuinely new
action (e.g. after reopen) gets a fresh key. The guard is the retransmit backstop: without the
key, a re-sent committed move would fail its CAS and surface a spurious error.

Tests: gomock for every branch (ack open→ack / no-op / not-found; resolve incl. snooze-clear +
wrong-kind + already-resolved; accept note-required; auto-resolve open→resolved / no-account /
already-resolved; active-fingerprint filter) + it-test (`TestIntegration_AlertTransitions`) driving
the ack→resolve marker moves, the re-resolve guard, auto-resolve, and the sweep against the live
ledger. Coverage `ledgerstore` 83.7%. build/vet/lint(0)/gofmt/-race clean.

### Phase 1 step 3c-3c — snooze / unsnooze (2026-07-03)

`internal/ledgerstore/alert_snooze.go` — status-neutral notification mute, metadata-only (no
marker move). **Snooze** rejects a non-future `until` and a RESOLVED alert; sets the `snooze`
metadata key on the item (a targeted LWW write, naturally retransmit-safe); re-snooze overwrites.
**Unsnooze** is idempotent: no snooze → unchanged no-op; else deletes the `snooze` key, swallowing
NotFound (already-gone = done, and gRPC-retransmit-safe). `by` is accepted but not persisted —
DELETED_METADATA has no actor field; actor attribution waits for the semantic event-log (RFC §4.4).

Tests: gomock (snooze set / past-until reject / resolved not-found; unsnooze delete / no-op) +
the it-test now drives snooze→unsnooze on the live ledger (metadata appears/clears, status stays
OPEN, second unsnooze is a no-op). Coverage `ledgerstore` 83.6%. build/vet/lint(0)/gofmt/-race clean.

**Step 3c-3 (alert lifecycle) complete.** The `LedgerStore` now covers the full alert surface
except the paginated lists (`ListAlerts`/`ListAlertEvents`) and evaluations, which are step 4 /
the deliberate no-durable-evaluations decision (RFC §4.4.2). No `var _ Store = (*LedgerStore)(nil)`
assertion yet — the interface is intentionally not fully implemented until step 4.

### Phase 1 step 3c-3 — SDLC review (consolidated, 2026-07-03)

**Scope:** `59e4d7b` + `9cc6025` + `32603fa` (3c-3a/b/c). **Checks:** `go build ./...` ✅ · `go
vet` ✅ · `golangci-lint --build-tags it` 0 issues ✅ · `gofmt` clean ✅ · `go test -race` (unit) ✅
· full it-suite green vs live ledger ✅ · `go mod tidy` clean ✅ · conventional commits ✅ · not on
`main` ✅ · no OpenAPI change (internal storage) ✅. Coverage `ledgerstore` 83.6% (>80%).

**Regression:** the whole diff is **additive** — new files + additive methods on the `ledgerClient`
/ `provisionAPI` interfaces (ledger-store-only). `internal/storage` (Postgres) is untouched → no
regression to the live path. `OpenAlertResult`/`Store` shapes unchanged. No CRITICAL/HIGH.

**Functionality:** transitions match the Postgres store's observable semantics (verified
method-by-method against `internal/storage/alert.go`): ack no-op/not-found, resolve/accept
active-only + snooze-clear, auto-resolve structural no-op, snooze future-only, unsnooze idempotent.

| # | Sev | Finding | Status |
|---|---|---|---|
| F22 | LOW | On **concurrent** transitions the ledger CAS loser gets a raw `FailedPrecondition` (guard miss), where Postgres (SELECT FOR UPDATE) yields a clean no-op / `ErrNotFound`. The store pre-checks status, so this only bites a genuine race; operator actions are low-concurrency and evaluation-driven auto-resolve is serialized per rule (RFC §5.1). Future: on a guard-miss, re-read and map to no-op/`ErrNotFound`. | ⬜ open (rare; deferred) |

**Verdict:** step 3c-3 is complete and solid. Open follow-ups are all deferred/non-blocking:
F20 (step-4 query indexes), F21 (cursor pagination for public lists), F22 (concurrent-CAS error
shape). Next: step 4 (filter translator + `ListRules`/`ListAlerts` with cursor pagination).

### Phase 1 step 4 — filter translator + ListRules/ListAlerts (SDLC review, 2026-07-03)

`internal/ledgerstore/filter.go` — translates recon's `go-libs/query.Builder` to a
`commonpb.QueryFilter`. The Builder's node types are unexported and `Walk()` flattens the tree, so
we go through its JSON form (Builder marshals to the `{$and|$or|$not|$match|…}` shape) and recurse,
preserving the boolean structure. Per-resource leaf mappers: alert keys → metadata (id/status/
severity/fingerprint/rule_id/period) or datetime `IntCondition` range (first/last seen); rule
`id` → address, others → metadata/bool/datetime. Unsupported key/operator/type combinations
return `storage.ErrInvalidQuery` (no silent mis-mapping). Datetime values are RFC3339 strings →
int64 micros (matches how the ledger stores + range-queries datetime metadata).

`internal/ledgerstore/pagination.go` + `ListRules`/`ListAlerts` — the ledger lists by address with
an opaque cursor; recon's Store is offset-paginated and time-ordered. We fetch the full matching
set (`QueryAccounts`, now cursor-following — see F below), decode, sort (rules created_at DESC,
alerts last_seen_at DESC), then offset-slice, emitting `bunpaginate.Cursor` with previous/next
encoded exactly like `bunpaginate.usingOffset` so recon's HTTP layer round-trips them unchanged.

**Checks:** build/vet/`golangci-lint --build-tags it` (0)/gofmt/-race clean; coverage `ledgerstore`
81.6%, `ledgerschema` 85.5% (>80%); conventional commit; not on `main`; no OpenAPI change; additive
(Postgres path untouched). it-test `TestIntegration_Lists` validates filter translation + indexes
+ sort + offset paging + combined AND filters (ruleID + status) against the live ledger. No
CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| F20 | — | **Resolved.** The provisioner now creates the account-metadata index for every filtered field (id, status, severity, rule_id, period, fingerprint, first/last_seen_at, name, template_kind, enabled, created/updated_at). Note: right after provisioning *new* indexes the first filtered query can briefly exceed the retry budget while the index builds (INDEX_BUILDING→Unavailable); it self-heals on retry (observed once, then stable). | ✅ resolved |
| F12 | — | **Resolved.** `FilterMetadataString` is now consumed by the filter translator; `PoolByRulePrefix` remains the only speculative helper (drop if unused by GA). | 🟡 mostly |
| F23 | MED | `ListRules`/`ListAlerts` fetch the **whole matching set** to sort + offset-slice client-side (the ledger streams address-ordered, no server-side time sort or offset). Cheap for filtered lists, O(matches) for an unfiltered one. A cursor-based Store interface + server-side ordering (or an ordered read index) removes it. Supersedes F21. | ⬜ open (deferred) |
| F24 | LOW | Datetime filters accept only RFC3339 **string** values (numeric epoch values are rejected). Matches how clients send timestamps; revisit if a caller sends epoch numbers. | ⬜ open (POC) |

### Post-step-4 — per-rule source pool + read-after-write finding (2026-07-03)

**Chart refinement — pool keyed by rule, not rule+period** (`alert:pool:rule:{ruleId}`). The pool
is only a mint source (+ an optional, currently-unread gauge); the period scoping added
O(#rules×#periods) NORMAL accounts (never purged) for a gauge already derivable by aggregating the
EPHEMERAL `st:` markers (live count — how `PQOpenCount` works) or item OCC (occurrences). Source
footprint drops to **O(#rules)**. Considered `@world` (STRICT-exempt, verified — drops the pool
type entirely) but kept a declared per-rule pool for a self-describing chart. Scripts unchanged
(only the `$pool` address the store passes changes); `PoolByRulePrefix` dropped (a rule now has one
pool, nothing to aggregate). Chart stays 4 types. RFC §4.1.2 updated.

| # | Sev | Finding | Status |
|---|---|---|---|
| F25 | MED | The ledger's read-side **metadata index is eventually consistent** with writes: a metadata-filtered read (GetAlert-by-id, ListRules/ListAlerts) right after the write may briefly not see it. The **machine path is unaffected** — `AutoResolveAlert` + the sweep read by structural address (`GetAccount`), which is consistent; only the **operator path** (id-resolution) uses the index, and it is human-paced (ms lag ≪ operator reaction). Surfaced as an it-test flake (immediate GetAlert after open); fixed with `require.EventuallyWithT`. A strict production fix would thread `ReadOptions.min_log_sequence` from the write into the read, or bounded-retry `findAlertItem` on miss. | ⬜ open (deferred; POC-safe) |

### Phase 1 step 5a — checkpoint mechanism (SDLC review, 2026-07-03)

The ADR-002 core (read A and B at one globally-consistent cut) built as a standalone, tested
component — **without** touching the engine or Postgres (scope: "mechanism first"). The engine
`LedgerResolver` interface flip + `Evaluation` anchor + service acquisition move to step 6, done
behind the dual-run flag (step 5b).

- **Client**: `AggregateVolumes(ledger, filter, checkpointID)` (per-asset input−output),
  `CreateQueryCheckpoint` (returns id + pinned max_sequence, read from the `CreatedQueryCheckpointLog`
  in the Apply response), `DeleteQueryCheckpoint` (NotFound-tolerant).
- **`Checkpoint` lifecycle** (`checkpoint.go`): `AcquireCheckpoint` → `Release` (create → use →
  delete; checkpoints aren't auto-cleaned and pin SSTs, so the caller owns the lifecycle).
- **`CheckpointReader`** (`resolver.go`): `AggregateBalance(ctx, ledger, query, checkpointID)` —
  signature mirrors the future `engine.LedgerResolver` with checkpointID replacing pit, so it drops
  into the engine at step 5b. Translates the data-ledger source query (`{$match:{address|metadata[k]}}`)
  via the shared `schema.TranslateQuery`.
- **Query translator factored to `ledgerschema`** (`query.go`: `TranslateQuery` + the and/or/not
  walker), shared by the recon Store (`alertLeaf`/`ruleLeaf`) and the data-ledger reader
  (`dataLedgerLeaf`) — DRY; `FilterAddressExact` added for exact-account matches.

**Checks:** build/vet/`golangci-lint --build-tags it` (0)/gofmt/-race clean; coverage `ledgerschema`
82.1%, `ledgerstore` 82.2% (>80%); `ledger` 32.7% is the it-tested gRPC wrapper (F3). it-test
`TestIntegration_CheckpointConsistentReads` proves the consistent cut end-to-end: two ledgers read
equal at one checkpoint; a later write to A leaves the checkpoint read frozen (100) while the live
read diverges (150). No CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| F26 | MED | Checkpoint **lifecycle ownership** is the caller's: an evaluation must `Release` its checkpoint or it leaks (pins SSTs → disk growth). Step 5b/6 must `defer Release` with a cancellation-surviving context, and add a bounded reaper/ring for crash-orphaned checkpoints (ADR-002 §7). | ⬜ open (step 5b/6) |
| F27 | LOW | `AggregateVolumes` reads are eventually consistent live (same class as F25); the it-test waits via `require.EventuallyWithT` before pinning the checkpoint so the snapshot includes the writes. Checkpoint reads themselves are deterministic (frozen snapshot). | ⬜ noted |

### Step 6 — scope decisions (2026-07-04)

The owner steered step 6 away from the handoff's "composite store + dual-run flag" framing. Decisions
(these reshape the whole step — recorded here as the source of truth):

1. **Postgres-free goal.** The ledger becomes the **sole `Store`**; there is no composite (Postgres
   for some domains + ledger for others) and no dual-run/bascule flag. Reconciliation becomes
   stateless. This collapses the RFC's phased "Phase 3 = drop Postgres" into step 6a.
2. **Drop legacy `/policies` + cash-pool.** The V0 policy = "ledger accounts vs a Payments pool"
   (`models.Policy` = `{LedgerName, LedgerQuery}` vs `{PaymentsPoolID}`). Now that Payments sync lives
   in a ledger (ledger-connect), reconciliation is **Ledger↔Ledger**, so the standalone policy/cash-pool
   concept no longer holds — it is a `source_parity(ledgerA, ledgerB)` **rule**, not a storage surface.
   Confirmed in code: `source_parity`'s doc already states it "expresses ledger↔pool (today's
   `ledger_vs_pool_drift` use case), ledger↔ledger, and pool↔pool". → remove routes, handlers,
   `Policy`/`Reconciliation` models, the 7 legacy Store methods, and the `ledger_vs_pool_drift`
   template (subsumed). Retiring the `/policies` API ≠ losing pool comparison: `source_parity` keeps
   `SourcePaymentsPool` as a Tier-2 side for the transition.
3. **Templates kept:** `source_parity` (the Ledger↔Ledger reconciliation core), `ledger_invariant`
   (single-ledger net-to-zero), `account_threshold` (single-ledger threshold). Dropped:
   `ledger_vs_pool_drift`.
4. **Evaluations non-durable (RFC §4.4.2, option A+D).** Drop `Create/Get/List Evaluation` + the
   `/evaluations` routes; `EvaluateRule` records `last_evaluation` (result/pit/evidence/cost) onto the
   alert (and/or `rule:{id}`), not a durable table. Queryable run-history → a sink later (Phase 3+).
   **Side effect:** removes the cross-store `inTx` problem — there is no separate evaluation write to
   keep atomic with the alert transitions; correctness rests on idempotency + guards (RFC §5.1).
5. **`ListAlertEvents` deferred** — returns empty + a documented TODO; history rides the `_recon`
   `SAVED_METADATA` stream / a sink in Phase 3-4 (§4.4). Machine + operator paths don't depend on it.
6. **6b loses its Postgres migration.** With no `evaluation` table, the `checkpointID` anchor lands on
   `alert:item` (`last_evaluation` metadata), not a new column — migration #10 evaporates; 6b is purely
   an engine change.
7. **Sequencing:** 6a (non-invasive: transport → simplification/interface-shrink → ledger-only wiring +
   Postgres removal) as its own reviewed increments, then 6b (engine flip) separately, with the
   resolver-interface design fork (split interfaces vs `ReadAnchor` union) settled at that point.

### Phase 1 step 6a-1 — secure ledger transport (SDLC review, 2026-07-04)

`internal/ledgerauth` — Ed25519/EdDSA JWT request signing (`TokenProvider` as gRPC
`PerRPCCredentials`) + `TLSConfig.Build` + `Config.Build` (assembles creds + per-RPC dial options and
enforces **F2**: refuse a plaintext transport unless `AllowInsecure` is explicitly set). Ported from
ledger-connect's `internal/auth`; named `ledgerauth` to avoid colliding with `go-libs/auth`. **Not
yet wired** — the fx provider + flags land in 6a-5 when the `LedgerStore` consumes the client (mirrors
5a's mechanism-first split). `ledger.NewClient(address, creds, dialOpts...)` is already the matching
signature.

**Checks:** build/vet/`golangci-lint` (0)/gofmt/-race clean; coverage `ledgerauth` **94.0%** (>80%);
conventional commit `feat(ledger-v3)`; not on `main`; additive (no existing code touched → no
regression to the Postgres path); no OpenAPI change (internal transport); `go mod tidy` clean
(`go-jose/go-jose/v4` promoted to a direct dep). Unit tests cover token sign/verify/god-claim/expiry/
metadata/seed-parsing (raw+hex+invalid), TLS build/CA/mTLS/errors, and the F2 guard
(refuse-by-default / opt-in / TLS / signing-over-TLS / signing-over-insecure / key XOR / malformed key).
No CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| F2 | HIGH | Secure transport (TLS + Ed25519 signing) + refuse-insecure-outside-dev. **Enforcement half done here** (`Config.Build` + `ErrInsecureTransport`, tested). Wiring half (serve.go flags + fx provider constructing the client from config) lands in **6a-5**. | 🟡 enforcement done; wiring in 6a-5 |
| F2-a | LOW | `RequireTransportSecurity()=false` + `god=true` claim inherited from ledger-connect → tokens are full-privilege and usable over plaintext; mitigated by the F2 guard (plaintext needs explicit `AllowInsecure`) + 5-min TTL. The signing key is a high-value secret (deployment concern). | ⬜ noted |
| F2-b | LOW | `TLSConfig.InsecureSkipVerify` = encrypted-but-unauthenticated TLS (MITM risk); its own explicit flag, dev-only. Flag in ops docs. | ⬜ noted |
| F2-c | LOW | No bufconn integration test proving the dial options attach to a live gRPC conn (ledger-connect has `grpc_test.go`); exercised by the 6a-5 wiring instead. Mirrors F3. | ⬜ deferred (6a-5) |

### Phase 1 step 6a-2 — drop legacy pool/policy surface + ledger_vs_pool_drift (SDLC review, 2026-07-04)

Removes the entire legacy vertical (see "Step 6 — scope decisions" #2/#3): API routes (`/policies*`,
`/reconciliations*`) + handlers; `CreatePolicy…ListReconciliations` from the `Store` and `backend.Service`
interfaces; `Policy`/`Reconciliation` models + Postgres stores + tests; the now-dead `service/utils.go`
balance helpers and `storageError`; and the `ledger_vs_pool_drift` template (subsumed by `source_parity`,
whose own doc already names the ledger↔pool case). Registry: `source_parity` + `ledger_invariant` +
`account_threshold`. `openapi.yaml` legacy paths + components removed (re-validated: YAML parses, no
dangling `$ref`s). Interface shrinks 28 → 21 methods.

**Orchestration tests converted drift → `source_parity(ledger, pool)`.** These test the service's
evaluate→alert lifecycle (open/update/auto-resolve/reopen/period-scoping), with the template incidental.
`source_parity` checks `abs(left − right)` vs drift's `abs(ledgerSign·ledger + pool)` (ledgerSign +1), so
the pool fixtures are negated: `abs(ledger − (−pool)) == abs(ledger + pool)` preserves **every** PASS/FAIL
and the `asset:USD/2` fingerprints (verified: `source_parity` aggregate unions assets, missing→0, tol
default 0, `<=`, `fingerprintFor("asset",asset)`). Negating the pool is also the natural parity framing
(the magnitude the ledger should match). `service_test.go` deleted (its `mockStore` only served the
removed legacy tests; `v1_orchestration_test.go` has its own `fakeV1Store`).

**Checks:** build/vet/`golangci-lint --build-tags it` (0 issues)/gofmt clean (the 4 pre-existing
gofmt-dirty files — `engine/{budget,source,types}.go`, `templates/ledger_invariant.go` — are untouched
by this step); `-race` unit tests green for `api`/`service`/`templates`/`models`; `storage` unit tests
need Docker (env-unavailable, unaffected by this change); conventional commit; not on `main`; **OpenAPI
updated** to match the removed routes; mock regenerated (`go generate ./internal/api/backend/`). No
CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| F28 | — | **Resolved (6a-2b, `7acda74`).** Deleted `v1-vs-legacy.md` + purged the legacy `/policies`/`ledger_vs_pool_drift` references across README/prd/technical docs (incl. the POLICY/RECONCILIATION ER entities in architecture.md). Historical records (ADR-001, RFC, this log's history, v1-stories drafts) keep their references intentionally. | ✅ resolved |

### Phase 1 step 6a-3 — evaluations non-durable (SDLC review, 2026-07-04)

Evaluations are a deterministic projection, not a durable queryable entity (RFC §4.4.2). Removed the
**read surface**: `GET /evaluations` + `GET /evaluations/{id}` routes/handlers, the dead
`getPaginatedQueryOptionsEvaluations` helper, the two endpoint tests, and `GetEvaluation`/
`ListEvaluations` from both the `Store` and `backend.Service` interfaces (+ service passthroughs +
`fakeV1Store` stubs). `openapi.yaml`: `/evaluations` paths + `Evaluations` response +
`EvaluationsCursorResponse` schema + `EvaluationID` param removed; `Evaluation` + `EvaluationResponse`
kept (`POST /rules/{id}/evaluate` still returns them). Store interface 21 → 18.

**Key call — `CreateEvaluation` kept (not removed).** The FK `alert_last_eval_fk`
(`alert.last_evaluation_id NOT NULL → evaluation.id`) means dropping the evaluation *insert* while
Postgres is still the live store would break every alert insert. So `CreateEvaluation` stays in the
interface: Postgres keeps persisting the row (FK satisfied, `EvaluateRule` unchanged) until the
ledger-native store no-ops it in 6a-5 — no broken interim. "last_evaluation on the alert" (the RFC
default) is already satisfied by `driveAlerts` writing `Evidence` + `LastEvaluationID`. This refines
the handoff's "drop Create/Get/List + EvaluateRule writes last_evaluation" — the Create drop + the
EvaluateRule change land in 6a-5 with the Postgres removal, not here.

**Checks:** build/vet/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit tests green
(api/service/templates/models + all ledger* packages); conventional commit; not on `main`; OpenAPI
updated (re-validated: parses, no dangling `$ref`s); mock regenerated. `storage` untouched (Docker-
gated). No CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| F29 | LOW | `internal/storage/evaluation.go`'s read methods (`GetEvaluation`/`ListEvaluations` + `evaluationQueryContext`/`buildEvaluationListQuery` + `GetEvaluationsQuery`/`EvaluationsFilters`) are now unreferenced by the interface — dead but self-consistent (their own `evaluation_test.go` still exercises them, and `rule_test.go` uses `GetEvaluation`). Swept wholesale with the Postgres package in **6a-5**. | ⬜ open (6a-5) |

### Phase 1 step 3c-4 — burn-on-close (2026-07-03)

Closes a real leak in the state model: EPHEMERAL purges a marker only at **zero** balance, but
3c-3's resolve *parked* the marker at `st:resolved` (balance 1) → **resolved markers never
purged** (accumulate O(all-resolved)). Fix = **burn-on-close** (anticipated in RFC §4.1.2): resolve
/ accept / auto-resolve **burn** the marker back to the pool (`alert_move` with destination = the
rule pool) instead of parking it, draining `st:{from}` → EPHEMERAL purge. Consequences:

- **Markers exist only for active alerts** (open / ack). A closed alert has **no marker**; its state
  lives on the item's `status` mirror + the burn tx. Marker regex tightened to `^(open|ack)$`;
  `st:resolved` is no longer a marker location (`StateResolved` survives only as the status↔segment
  mapping value).
- **Reopen re-mints** (`alert_open`) instead of a guarded move from `st:resolved` (there is no marker
  to move). Resurface from **ACK** stays a guarded move (`alert_reopen`, marker at `st:ack`). Guards
  stay where they matter (ack, resolve/burn, ack→open); open and reopen are unguarded mints
  (idempotency + serialized eval protect — as open always was).
- `−balance(pool, ALERT)` now = **active (open+ack) count**, self-correcting on close.
- No new numscript (burn reuses `alert_move` with the pool as destination).

Tests: unit updated (Reopen → `alert_open` re-mint; resolve/auto-resolve → burn to pool); it-test
`TestIntegration_AlertTransitions` on a fresh **`recon-it4`** now drives ack → **burn (st:ack
purges)** → **reopen (re-mint at st:open)** → re-close, asserting the marker is gone after close.
build/vet/lint(0)/gofmt/-race clean.

> **it-test invocation:** run the full it suite with **`-p 1`** — the packages share one live
> ledger, and default package-level parallelism causes index-propagation contention (a spurious
> `Eventually` timeout in the ledger checkpoint it-test). `go test -tags it -p 1 -run TestIntegration
> ./internal/ledgerstore/... ./internal/ledger/...`.

**Chart challenge recap (answered, no further change):** `alert:item` = the durable canonical
record (stable-address metadata + OCC + status mirror + dedup key + id-index target) — persists like
the Postgres alert row; bounding it is a retention concern (RFC §10.5). `per:{p}` = the dedup-scope
segment; continuous rules use the constant `per:continuous` (functionally used by the period-scoped
sweep) — kept uniform to avoid forking the address shape/code path for a cosmetic gain.

## Proto re-sync procedure (F5)

The ledger protos are **copied**, not submoduled (mirrors ledger-connect). To update:

1. `cp <ledger-connect>/proto/ledger/*.proto proto/ledger/` (ledger-connect keeps them synced
   to a ledger release — currently **v3.0.0-alpha.3**).
2. Rewrite the module path: `sed -i '' 's#formancehq/ledger-connect/internal/ledgerpb#formancehq/reconciliation/internal/ledgerpb#g' proto/ledger/*.proto`.
3. `just generate-ledger-proto` (regenerates `internal/ledgerpb/*`).
4. Re-copy hand-written helpers if ledger-connect changed them:
   `internal/ledgerpb/commonpb/{metadata,index,uint256}_helpers.go`.
5. `go mod tidy && go build ./...`. Commit the regenerated output.

Record the ledger version each sync targets in the commit message.
