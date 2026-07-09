# Ledger-native migration — development log

Traceability record for the Reconciliation → Ledger v3 storage migration. One row per
phase/step with status + commit; SDLC reviews and tracked follow-ups below. Design lives in
the [RFC](./rfc-ledger-native-storage.md) and [ADR-002](../prd/adr-002-pit-consistency.md).

**Branch:** `feat/reconciliation-ledger-v3` (based on `feat/ledger-clarity-v1`; rebase onto
`main` after PR #83 merges). Dedicated PR to follow.

---

## 👋 Handoff — resume here

**🎉 Checkpoint alternative COMPLETE (2026-07-08, ADR-003).** Query checkpoints **removed** — recon
reads its data ledgers **live** and records each evaluation as an immutable `_recon` **capture**
transaction (audit-grade: receipt-signed, append-only; positive assurance on pass, break evidence on
fail). Owner-steered pivot away from the per-eval checkpoint (cluster-wide, Raft/SST-heavy, discarded
per eval). 4 reviewed steps: 1 remove cross-check (`13b0357`), 2 drop checkpoints + live reads
(`8979795`, **closes F26 + F32**), 3 capture in `_recon` (`932e931`, chart +2 types/+1 asset/+1
numscript, it-ledger `recon-it4`→`recon-it5`), 4 docs (ADR-003 rewrite + ADR-002/RFC/architecture
sync). Multi-ledger atomic-read gap tracked upstream as **EN-1480**. All green: unit -race, live
it-suite, lint 0, gofmt. Details in the "checkpoint alternative" workstream section below.

**Done (earlier):** Phase 1 steps 0–4 + **5a** (checkpoint mechanism, `6a7e110`) + **all of step 6a** — the
server is now **Postgres-free and stateless**, running entirely on the control-ledger `_recon`
(`LedgerStore` is the sole `service.Store`, F2-secured, provisioned at boot; shared contract types
live in `internal/store`; `internal/storage`+`internal/events` deleted, net −3.5k lines; `31c5ca6`).
Legacy `/policies`+cash-pool gone, evaluations non-durable, alert-events deferred to a Phase-3 sink.
`CheckpointReader` reads data ledgers at a query checkpoint (ADR-002 cut, it-proven). Chart
**finalized** — 4 account types (see §4.1.1/§4.1.2 + phase table). Scope rationale in "Step 6 — scope
decisions" below; per-sub-step SDLC reviews follow. **Verified:** DB-less boot smoke + fresh it-tests.

**🎉 Step 6b COMPLETE — Phase 1 is done. Reconciliation is Postgres-free, stateless, and reads data
ledgers at checkpoint-consistent cuts.** Option C (owner, 2026-07-06): flip the single `LedgerResolver`
interface `pit`→`checkpointID`, retire the pit-based `SDKLedgerResolver`. Rationale: ADR-002 §6 literally
prescribes the single-interface flip ("Tier-2 resolvers keep PIT/latest") — Phase 1 has **no live Tier-2
*ledger* source** (cross-cluster ledger is §11 future); the only Tier-2 source is the payments pool, which
already has its own `PaymentsResolver`. So the Tier-1/Tier-2 split *is* the existing `SourceKind` switch
(ledger→checkpoint, pool→latest); A (two ledger interfaces) / B (`ReadAnchor` union) both preserved a
Tier-2 ledger path nothing constructs — dropped as YAGNI (re-introduce A at §11).

**6b sub-steps (all done):**
- **6b-1 ✅ (`32e6bb0`)** — `CheckpointReader.ListAccounts` + streaming `Client.QueryAccountsFunc`
  (budget-enforced mid-stream, returns engine-free `ledger.Account`).
- **6b-2 ✅ (`15639d8`)** — the flip: `engine.LedgerResolver` `pit`→`checkpointID`;
  `EvalInput{CheckpointID, PIT}` (SafetyMargin subtraction dropped); templates read ledger @ checkpoint,
  pool @ latest; `pitPerSource` **Tier-2-only**; `SDKLedgerResolver` retired + `engine.SDKClient` trimmed;
  new adapter `internal/ledgerresolver`; `EvaluateRule` pins ONE checkpoint per eval
  (Acquire→Evaluate→Release, cancellation-surviving ctx). **F32** found+fixed: `AcquireCheckpoint` waits
  for the async read-index to materialize before returning. End-to-end it-test
  `TestIntegration_EvaluateAtCheckpoint` proves the flip on a live ledger.
- **6b-2b ✅ (`4bc73be`)** — removed the now-inert `SafetyMargin` end-to-end (model/API/scheduler/service
  + OpenAPI); `Schedule` dropped its custom marshalers (default encoder). −139 net.
- **6b-3 ✅ (`4fb1ad0`)** — checkpoint reaper (**F26 resolved**): control-ledger registry
  (`internal:checkpoints`, `cp:<id>` keys), recorded on Acquire / forgotten on Release;
  `ReapOrphanedCheckpoints` (age-thresholded 15m ≫ 30s MaxWallClock, so never reaps a live checkpoint even
  another instance's) runs at startup in the provisioner OnStart. it-test `TestIntegration_ReapOrphanedCheckpoints`.

**Post-Phase-1: Event delivery ✅ COMPLETE (ED-1 + ED-2).** The original Phase 2/3 (Postgres) framing was
absorbed by the 6a pivot. This workstream delivered alert events without a recon-owned message bus (RFC §4.4):
owner chose **"delivery now, history deferred" (2026-07-06)**. Key mechanic: most transitions are
`CreateTransaction` batches (marker move + `account_metadata`) → `COMMITTED_TRANSACTION` events; only
snooze/unsnooze are `SAVED_METADATA`/`DELETED_METADATA` — the sink must cover all three.
- **ED-1 ✅ (`07a0bd5`)** — self-describing `last_transition` envelope stamped on every transition so each
  log event says what happened. `Client.ApplyMetadata` (atomic set+delete) added for unsnooze.
- **ED-2 ✅ (`c769e17`)** — `Client.{Add,Remove,Get}EventsSink` + boot-provision an idempotent HTTP webhook
  sink when `--events-sink-url` is set (`event_types=[COMMITTED_TRANSACTION,SAVED_METADATA,DELETED_METADATA]`;
  `--events-sink-secret` optional), replacing the publisher dropped at 6a-5a. Server is add-only (swallow
  AlreadyExists for boot; config change = manual remove+re-add, which resets the cursor). Verified via
  it-test + a boot smoke (`ledgerctl events list` confirms the sink + live delivery attempts).
- **Deferred (Phase 4):** `ListAlertEvents` paginated history needs a **queryable sink** (ClickHouse/
  Databricks — the ledger log has no account filter, so per-request replay is O(all _recon writes));
  semantic event types + replay API; checkpoint **anchor persistence** (needs retained/scheduled
  checkpoints, §7 — not needed while break evidence is the durable audit, §8).

A dedicated PR off `main` should follow (rebase after PR #83).

**Watch:** open findings F1/F17/F22/F23/F25/F27/F31 (✅ resolved: F2 @ 6a-5a, F16/F29/F30 @ 6a-5b, F26 @ 6b-3, **F33** @ metadata-type-audit — `MetadataToMap` lossless, **F8** @ idempotent-schema-provisioning — `Provision` reconciles account types + metadata fields additively on an existing ledger (delta-based to avoid a per-boot index rewrite — new upstream note **F34**), so additive chart changes no longer need an it-ledger rename; **F26 + F32 now without object** — checkpoints removed @ checkpoint-alternative step 2). Metadata schema **audited & correctly typed** (dead `reopened_at`/`parent_resolution` removed). **Don't touch:**
`feat/ledger-clarity-v1`; untracked V1 files (`docs/drafts/v1-epic-*`, `v1-stories/`); the
uncommitted `Justfile` change (orphaned `generate-ledger-proto`, leave unstaged); `ledger-local/`.
**Build/test:** `export PATH=$PATH:$(go env GOPATH)/bin` then `GOROOT= go build ./...`,
`GOROOT= go test -race ./internal/ledger{,store,schema}/...`, it-tests `GOROOT= go test -tags it
-p 1 -run TestIntegration ./internal/ledgerstore/... ./internal/ledger/...` (**`-p 1`**: packages
share one live ledger; F8 **resolved** — `Provision` now reconciles additively, so an it-ledger rename is only needed for a **destructive** chart change (removing/retyping a field, or changing an account type with accounts); current control ledger `recon-it6`). Conventions: `feat(ledger-v3):` commits, update this log
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
| 1 | **6a** | **Ledger-only `Store`** (transport + simplification + wiring; Postgres removed) | ✅ **done** | `31c5ca6` |
| 1 | 6a-1 | ↳ secure transport (`internal/ledgerauth`: Ed25519 signing + TLS + F2 insecure guard) | ✅ done · reviewed | `5f4ab4b` |
| 1 | 6a-2 | ↳ drop legacy `/policies`+`/reconciliations` + `ledger_vs_pool_drift` template (+ openapi) | ✅ done · reviewed | `299b7a7` |
| 1 | 6a-2b | ↳ sync product docs to the ledger-only surface (delete v1-vs-legacy, purge legacy refs) | ✅ done | `7acda74` |
| 1 | 6a-3 | ↳ evaluations non-durable — drop the read surface (`Get/ListEvaluation` + `/evaluations`); `CreateEvaluation` kept (no-op on ledger @ 6a-5) | ✅ done · reviewed | `e282f78` |
| 1 | 6a-4 | ↳ `ListAlertEvents` → empty + TODO (SAVED_METADATA sink deferred) | ✅ done (folded into 6a-5a) | `03d3a84` |
| 1 | 6a-5a | ↳ bind `LedgerStore` as sole `Store` + `ledger.Client` fx/flags + provision at boot + remove Postgres wiring (boot DB-less) | ✅ done · reviewed | `03d3a84` |
| 1 | 6a-5b | ↳ delete dead Postgres code (storage impl, migrations, `RunInTx`, DB flags, `internal/events`) + extract shared types to `internal/store` (F16/F29/F30) | ✅ done · reviewed | `31c5ca6` |
| 1 | **6b** | **engine flip** (`LedgerResolver` pit→checkpointID, Option C) + checkpoint acquisition (no migration) | ✅ **done** | `4fb1ad0` |
| 1 | 6b-1 | ↳ `CheckpointReader.ListAccounts` + streaming `Client.QueryAccountsFunc` (budget-enforced, engine-free `ledger.Account`) | ✅ done · reviewed | `32e6bb0` |
| 1 | 6b-2 | ↳ interface flip `pit`→`checkpointID` + `EvalInput.CheckpointID` + per-source read + `internal/ledgerresolver` adapter + rewire + service checkpoint acquisition + **F32 readiness wait** | ✅ done · reviewed | `15639d8` |
| 1 | 6b-2b | ↳ remove inert `SafetyMargin` from request/schedule/API + OpenAPI | ✅ done · reviewed | `4bc73be` |
| 1 | 6b-3 | ↳ checkpoint reaper for crash orphans — control-ledger registry + age-thresholded startup reap (F26 resolved) | ✅ done · reviewed | `4fb1ad0` |
| 2/3 | — | ~~Flip reads / Postgres shadow / drop Postgres~~ — **absorbed by the 6a Postgres-free pivot** (already done) | ✅ absorbed | — |
| **ED** | — | **Event delivery** (RFC §4.4) — self-describing transitions + ledger event sink; "delivery now, history deferred" (owner, 2026-07-06) | ✅ **done** | `c769e17` |
| ED | ED-1 | ↳ self-describing `last_transition` envelope stamped on every alert transition (+ `Client.ApplyMetadata` atomic set+delete) | ✅ done · reviewed | `07a0bd5` |
| ED | ED-2 | ↳ provision the ledger `AddEventsSink` (HTTP webhook, `event_types=[COMMITTED_TRANSACTION,SAVED_METADATA,DELETED_METADATA]`) at boot; `--events-sink-url`/`--events-sink-secret` | ✅ done · reviewed | `c769e17` |
| 4 | — | `ListAlertEvents` (queryable history via a ClickHouse/Databricks sink) + semantic events / replay (generic event-log) | ⬜ deferred | — |

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
| F8 | MED | Schema **evolution** on an already-created ledger is not handled: `CreateLedger` applies the full schema only on first boot; adding a metadata field / account type later needs idempotent `SetMetadataFieldType` / `AddAccountType` passes in `Provision`. | ✅ resolved @ idempotent-schema-provisioning (additive reconcile in `Provision`; destructive evolution still out of scope) |
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

### Phase 1 step 6a-5a — wire LedgerStore as sole Store, DB-less boot (SDLC review, 2026-07-05)

The flip: the ledger-native code (additive-but-unwired since step 0) becomes the live storage; the
server boots with **no database**.

- **`LedgerStore` completes `service.Store`** (`adapters.go`): `Ping` (bounded read of the
  control-ledger `world` account), `CreateEvaluation` (no-op — non-durable, 6a-3), `ListAlertEvents`
  (empty + TODO for the Phase-3 SAVED_METADATA sink — **this is 6a-4**, folded in here). The
  `var _ service.Store = (*ledgerstore.LedgerStore)(nil)` assertion lives in `cmd/ledger.go` (wiring
  seam) so the storage layer doesn't depend on the service layer.
- **`cmd/ledger.go`** — `ledgerClientModule`: builds the `ledger.Client` via `ledgerauth.Config.Build`
  (**closes the wiring half of F2** — TLS + Ed25519 signing, refuses insecure unless
  `--ledger-insecure`), binds `LedgerStore` as `service.Store`, provisions the control-ledger at
  startup (idempotent OnStart hook). New `--ledger-*` flags; control-ledger name defaults to
  `ledgerschema.DefaultControlLedger` ("reconciliation").
- **`cmd/serve.go`** drops `bunconnect`/`storage.Module` → DB-less. **`api/module.go`** takes the
  Store from the ledger module and drops the alert-event publisher (delivery → ledger sink in Phase 3,
  per the owner's decision — interim webhook-event gap accepted).

**Verified end-to-end** against a live Ledger v3 (`127.0.0.1:8888`): (1) boot **without**
`--ledger-insecure` is refused with `ErrInsecureTransport` through the full fx graph (F2); (2) **with**
it, the server boots DB-less, the OnStart hook provisions the control-ledger, and `/_healthcheck`
returns `{"default":"OK"}`; (3) `ledgerctl account-types list` on the freshly-provisioned ledger shows
the finalized **4-type chart** (alert-item/alert-pool NORMAL, alert-state EPHEMERAL, rule NORMAL).

**Checks:** build/vet/`golangci-lint --build-tags it` (0, incl. `cmd/`)/gofmt clean; `-race` unit
tests green across all wired packages; conventional commit; not on `main`; no OpenAPI change (wiring
only). No CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| F2 | — | **Resolved.** Enforcement (6a-1) + wiring (6a-5a): the server refuses an insecure ledger transport unless `--ledger-insecure` is set; TLS + Ed25519 signing wired from `--ledger-tls-*` / `--ledger-auth-*`. Proven end-to-end (boot-refusal + secure-path smoke). | ✅ resolved |
| F30 | LOW | `internal/events` (watermill alert-event publisher) is now unimported dead code (its only consumer, `provideAlertEventPublisher`, was removed). Swept with the Postgres deletion in 6a-5b, or when the Phase-3 ledger sink replaces it. | ⬜ open (6a-5b) |
| F31 | LOW | `Ping` reads `world` with `context.Background()`+5s; `Store.Ping()` is currently uncalled (the `/_healthcheck` uses a static check), so it's a latent contract impl. If wired to health later, confirm `world` reads cleanly on a fresh ledger. | ⬜ noted |

### Phase 1 step 6a-5b — delete Postgres, extract internal/store (SDLC review, 2026-07-05)

Removes the now-dead Postgres layer (server already boots DB-less since 6a-5a). **Reconciliation is
Postgres-free and stateless.**

- **New leaf package `internal/store`** (4 files, no ORM deps — bunpaginate + go-libs/query only):
  the storage-agnostic contract types the `Store` interface + `LedgerStore` share —
  `PaginatedQueryOptions`+`NewPaginatedQueryOptions`, `{Rules,Alerts,AlertEvents}Filters` +
  `Get*Query` + `New*Query`, `OpenAlertInput`/`OpenAlertResult`, `RulePatch`, and the
  `ErrNotFound`/`ErrInvalidQuery` sentinels. **Resolves F16.**
- **Deleted:** all of `internal/storage` (bun impl + migrations + `Storage`/`RunInTx`/
  `AlertEventPublisher` + tests — **resolves F29**), `internal/events` (dead after 6a-5a —
  **resolves F30**), and the DB/migrate plumbing in `cmd` (`migrate.go`, `--auto-migrate`,
  `newMigrate`). `Service.inTx` simplified to `fn(ctx, s.store)` (idempotent store, no cross-store tx).
- Swept `storage.` → `store.` across **29 files** (incl. the `scheduler` package, which also consumed
  the moved types). One name-collision handled: files with a local `store` var (the `inTx`/`driveAlerts`
  param → renamed `st`; ledgerstore tests → aliased `recstore`).

**Checks (re-verified independently):** build/vet/`golangci-lint --build-tags it` (0, incl. `cmd/`)/
gofmt clean (only the 4 pre-existing dirty engine/template files remain, untouched); `-race` unit tests
green across api/service/ledgerstore/ledger/ledgerschema/ledgerauth/store/scheduler; **fresh** ledger
it-tests (`-p 1 -count=1`) pass against the live ledger; **DB-less boot smoke** re-run — server
provisions the control-ledger and serves `/_healthcheck` `{"default":"OK"}`. Net **−3522 lines**.
Conventional commit; not on `main`; no OpenAPI change. No CRITICAL/HIGH.

**Step 6a is complete.** The server runs entirely on the control-ledger (`_recon`): rules + alert
lifecycle on the ledger, evaluations non-durable, alert-events deferred to a Phase-3 sink, secure
transport (F2). `CheckpointReader` still stands ready for the 6b engine flip.

### Step 6b — resolver design fork DECIDED (2026-07-06): Option C

The fork deferred at 6a (A: two resolver interfaces + per-source dispatch vs B: `ReadAnchor` union) was
put to the owner with a third option surfaced on a close re-read of ADR-002 §6. **Owner chose C.**

- **What §6 literally says:** *"Interface change: `LedgerResolver.AggregateBalance(…, pit)` →
  `(…, checkpointID)`; same for `ListAccounts`. Tier-2 resolvers keep their PIT/latest semantics."* —
  i.e. flip **the single** ledger interface; the concrete Tier-2 resolver is the payments **pool**
  (`PoolBalanceLatest`, already its own interface).
- **Why A/B were dropped:** both exist to preserve a Tier-2 **ledger** path (`SDKLedgerResolver` + pit).
  Phase 1 constructs **no such source** — a `ledger` source is just `{ledger, query}` with no
  cross-cluster marker, and cross-cluster ledger recon is ADR-002 §11 "revisit" (future). A's second
  interface + B's union both encode a distinction nothing uses. The prior handoff cited A as "matches
  §6"; the precise reading is that **C** matches §6, and A generalises beyond it. Re-introduce A at §11.
- **What C means concretely:** the Tier-1/Tier-2 split is the **existing `SourceKind` switch**
  (`resolveBalances` / `SourceSpec.resolve`): `ledger` → checkpoint (`CheckpointReader`), `pool` →
  latest (`SDKPaymentsResolver`, unchanged). No tier flag on `Source`. `SDKLedgerResolver` retired.
  Live read path today is still SDK+pit (`provideResolvers` wires both resolvers off the SDK) — 6b-2
  swaps the ledger side to the gRPC `CheckpointReader` via the `internal/ledgerresolver` adapter.

### Phase 1 step 6b-1 — CheckpointReader.ListAccounts + streaming QueryAccountsFunc (SDLC review, 2026-07-06)

The per-account read side of the checkpoint reader — the last mechanism piece before the engine flip
(mirrors the 5a / 6a-1 "mechanism-first, not yet wired" split).

- **`Client.QueryAccountsFunc`** streams matched accounts and invokes a callback per account; a non-nil
  callback error aborts the stream verbatim. `QueryAccounts` is now a thin collect-all wrapper over it
  (signature + behaviour unchanged → `ledgerstore` untouched). This is also the streaming seam that
  could later close **F23** for the public lists.
- **`CheckpointReader.ListAccounts(ledger, query, checkpointID, limit)`** reads each matched account's
  per-asset balance frozen at a checkpoint, enforcing the accounts budget **mid-stream** (errors at
  `limit+1`, never truncates — no fetch-all; strictly better than the retired SDK path). Returns the
  engine-free **`ledger.Account`** `{Address, Ledger, Metadata, Balances}` so `internal/ledger` stays a
  leaf; the engine adapter (6b-2) maps it to `engine.Account`. `volumeBalance` prefers the ledger-
  provided `Balance` else `input − output` (mirrors the SDK resolver); metadata via `commonpb.MetadataToMap`.

**Checks:** build/vet/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit + fresh it-suite
(`-p 1`) green; conventional commit; not on `main`; no OpenAPI change; `internal/ledger` confirmed
engine-free. Coverage `ledger` 34.1% (↑ from 32.7%) — the package is the it-tested gRPC wrapper (F3);
the pure logic added (`volumeBalance`/`accountFromProto`/`decimalBig`) is unit-tested, the streaming/
budget path is it-tested (`TestIntegration_CheckpointListAccounts`: frozen per-account snapshot, budget
abort, write-after-checkpoint invisibility). No CRITICAL/HIGH/MEDIUM.

| # | Sev | Finding | Status |
|---|---|---|---|
| — | LOW | `MetadataToMap` keeps only `StringValue`-typed metadata (bool/datetime dropped). Matches the SDK path + existing `ledgerstore` usage; data-ledger metadata filtering is server-side so the returned `Metadata` map is informational. | ✅ acceptable, noted |
| F23 | — | **Partially mitigated** for the resolver read path (`ListAccounts` bounds memory to `limit+1` via `QueryAccountsFunc`). Public `ListRules`/`ListAlerts` still collect-all via `QueryAccounts` → F23 stays open there, but the streaming seam to fix it now exists. | ⬜ open (lists) |

### Phase 1 step 6b-2 — engine flip to checkpoint-anchored reads (SDLC review, 2026-07-06)

The invasive kernel flip (Option C). Data-ledger reads move from SDK+pit to the gRPC control-plane at a
query checkpoint (ADR-002 §6): one checkpoint pinned per evaluation is a globally consistent cross-ledger
cut, so a ledger↔ledger rule reads both sides skew-free.

- **Engine:** `LedgerResolver.AggregateBalance`/`ListAccounts` `pit time.Time` → `checkpointID uint64`.
  `EvalInput` is now `{CheckpointID, PIT}`; the SafetyMargin *subtraction* is gone (a checkpoint is an
  atomic cut and pools read latest, so the PIT-race guard has no consumer left). `pitPerSource` is
  **Tier-2-only** — ledger sources are anchored by the shared checkpoint, so a ledger↔ledger evaluation
  records an empty map; only pool sources record a PIT (§10.1). `Source.PIT` removed (dead after flip).
  `SDKLedgerResolver` retired; `engine.SDKClient` trimmed to the pool read (`SDKPaymentsResolver` stays).
- **Adapter + wiring:** new leaf-bridge `internal/ledgerresolver` wraps `ledger.CheckpointReader` and
  maps `ledger.Account`→`engine.Account`, keeping `internal/ledger` engine-free AND `internal/api/service`
  ledger-free. `provideResolvers` backs `Ledger` with the checkpoint reader over `*ledger.Client`.
  `EvaluateRule` pins ONE checkpoint per eval (Acquire→Evaluate→Release; Release on a
  cancellation-surviving ctx so a cancelled/expired eval still frees the SST-pinning checkpoint — **F26**).
  `service.Checkpointer` (acquire→release-func) abstracts it; the impl is wired in `cmd` (probe ledger =
  control-ledger flag) so the service layer takes no `internal/ledger` dependency.
- **Checks:** build/vet(+`-tags it`)/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit green
  (engine/templates/api/service/ledgerresolver/ledger/ledgerstore/scheduler); full it-suite +
  **end-to-end** `TestIntegration_EvaluateAtCheckpoint` (CreateRule → write two live data ledgers →
  EvaluateRule at a real pinned checkpoint → PASS/no-alert, then imbalance → FAIL/alert; stable ×3);
  DB-less boot smoke. Conventional commit; not on `main`; no OpenAPI change. Net −48 LOC. No CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| F32 | MED | **Checkpoint read-index materializes asynchronously.** `CreateQueryCheckpoint` commits the id via Raft immediately, but the ledger's index-builder builds the checkpoint's (global) read index afterwards; a read in that ~100–130ms window fails with a **non-retryable gRPC `Unknown`** — the server logs "opening checkpoint read index … does not exist" but returns only "unknown server error", so the client cannot match+retry at the read site. Older it-tests passed on timing luck; the control-ledger's concurrent index backfill widens the window. **Fixed in recon:** `AcquireCheckpoint(ctx, probeLedger)` now probes the control ledger at the new checkpoint (bounded ~5s) until readable, then returns a usable snapshot (deletes it on timeout — no leak). Also observed server-side: "creating read index checkpoint N: link …sst: no such file" (SST compacted between snapshot+link) → index-builder retries and succeeds. **Proper fix is upstream** (ledger should return a retryable code, or block create until materialized) — recommend filing a ledger issue. | 🟡 mitigated in-recon; upstream follow-up |
| — | LOW | `EvaluateRuleRequest.SafetyMargin` now inert (checkpoint atomic; pool latest) but still populated by API/scheduler. Accepted-but-ignored interim; wire/API/OpenAPI removal → **6b-2b**. | ⬜ 6b-2b |
| — | LOW | Checkpoint **anchor not persisted**: fresh-per-eval + immediate Release ⇒ the `checkpointID` points at a deleted checkpoint, so recording it has little value; the durable break evidence (the numbers) is the audit (§8). Replay-reproducibility needs retained/scheduled checkpoints (§7) — Phase-2+/6b-3. | ⬜ noted |

### Phase 1 step 6b-2b — remove inert SafetyMargin (SDLC review, 2026-07-06)

`SafetyMargin` was the ledger-PIT in-flight-commit guard; checkpoint reads (6b-2) are an atomic cut and
pools read latest, so nothing consumes it. Removed end-to-end: `models.Schedule` field (+ the custom
`Marshal/UnmarshalJSON` + `scheduleWire` that existed only to translate it — `Schedule` now uses the
default encoder), `EvaluateRuleRequest` field, the API request field + `defaultEvaluateSafetyMargin` +
its parse/validate, the scheduler passthrough, and the `openapi.yaml` field on `Schedule` +
`EvaluateRuleRequest`. Persisted rules with a stale `safetyMargin` in their serialized schedule decode
cleanly (unknown JSON field ignored) — no migration (Postgres is gone). **Checks:** build/vet/lint(0)/
gofmt clean; `-race` unit green (models/api/service/scheduler/ledgerstore); OpenAPI leaf-removed (siblings
intact); conventional commit; not on `main`. Net −139 LOC. Obsolete tests removed (schedule wire test
rewritten to the plain shape; the two `safetyMargin` 400-validation handler tests deleted). No findings.

### Phase 1 step 6b-3 — checkpoint reaper for crash orphans (SDLC review, 2026-07-06)

Closes the remaining **F26** gap. The deferred cancellation-surviving Release (6b-2) covers ctx
cancel/timeout; a hard crash between Acquire and Release still leaks a checkpoint (not auto-cleaned, pins
SSTs). Recon can't enumerate cluster checkpoints (`ListQueryCheckpoints` is `ClusterService`; the client
wraps `BucketService`) and the cluster is shared, so it reaps only checkpoints it created:

- **Registry** on the control ledger (`internal:checkpoints`, one `cp:<id>` = unix-seconds metadata key;
  undeclared dynamic keys, stored as-is, permitted under AUDIT). `AcquireCheckpoint` records; `Release`
  forgets (only after the checkpoint delete succeeds — a failed delete keeps the entry for retry). Both
  best-effort at the call site (a missed record risks only one un-reaped orphan; never fails an eval).
- **`ReapOrphanedCheckpoints(ledger, olderThan, now)`** deletes registered checkpoints older than
  `olderThan` and clears their entries. **Age-thresholded (15m ≫ 30s MaxWallClock)** so a live
  evaluation's checkpoint — even another instance's, on the shared control ledger — is never reaped. Run
  at startup, folded into the provisioner OnStart (after the control ledger exists); never fails boot.

**Checks:** build/vet/lint(0, `--build-tags it`)/gofmt clean; `-race` unit green; it-test
`TestIntegration_ReapOrphanedCheckpoints` (aged orphan reaped+forgotten, fresh entry retained, re-reap
no-op) on a fresh isolated control ledger; full it-suite (ledger/ledgerstore/service) + DB-less boot smoke.
Conventional commit; not on `main`; no OpenAPI change.

| # | Sev | Finding | Status |
|---|---|---|---|
| F26 | — | **Resolved.** Cancellation-surviving Release (6b-2) + startup age-thresholded reaper (6b-3). | ✅ resolved |
| — | LOW | Reaper is **startup-only** — orphans clean on the next boot (aligns with crash→restart under an orchestrator). A long-lived instance that never restarts won't reap its own orphans until it does; acceptable since deferred Release covers the common path. A periodic reaper can be added if needed. | ⬜ noted |

### Event delivery step ED-1 — self-describing alert transition events (SDLC review, 2026-07-06)

First post-Phase-1 step (RFC §4.4, "delivery now"). Stamps a `last_transition` envelope
(`{type: reconciliation.alert.<t>, subject, alertID, prevStatus, newStatus, occurredAt, correlationID,
payload}`) into `alert:item` metadata on every transition, so each ledger log entry for that write is
self-describing for an event-sink consumer — `COMMITTED_TRANSACTION` for lifecycle moves (open/ack/
resolve/accept/auto-resolve), `SAVED_METADATA`/`DELETED_METADATA` for snooze/unsnooze. Stamped at all 7
write paths by adding the key to the existing atomic metadata write (zero extra round-trips). The key is
**undeclared** (like `label.*`) → no chart change, no it control-ledger bump. `correlationID` = the
evaluation id for eval-driven transitions (open/occurred/reopened/auto-resolved), empty for operator
actions (ack/resolve/accept/snooze/unsnooze). Added **`Client.ApplyMetadata`** (atomic set+delete) so
unsnooze records the transition (now attributing the actor `by`) and clears the snooze key in one batch;
`SaveAccountMetadataValues` refactored to share the add-request builder.

**Checks:** build/vet/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit (envelope shape +
operator-vs-eval correlation; snooze/unsnooze mock expectations updated) + full it-suite incl.
`TestIntegration_TransitionEventStamped` (opened→acknowledged envelopes on the live ledger); conventional
commit; not on `main`; no chart/OpenAPI change; mock regenerated. No CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| — | LOW | `last_transition` is LWW — the account holds only the latest transition; full history is the log's ordered sequence of these writes (read via the sink), consistent with the deferred `ListAlertEvents`. | ✅ by design |
| — | LOW | `occurred` covers both OPEN→OPEN (repeat) and ACK→OPEN (resurface); `prevStatus` disambiguates for consumers. | ✅ acceptable |
| F17 | — | Envelope derives from occurrence/evidence state already in the batch, so it doesn't materially widen the content-sensitive-idempotency window. | ⬜ unchanged |

### Event delivery step ED-2 — provision the ledger events sink (SDLC review, 2026-07-06)

Restores alert-event delivery (dropped at 6a-5a) via the ledger's native HTTP webhook sink — no
recon-owned message bus. `Client.{Add,Remove,Get}EventsSink` over `BucketService` (Apply
`Request_AddEventsSink` + the `GetEventsSinks` RPC). Boot-provisions the `reconciliation` webhook sink in
the provisioner OnStart **when `--events-sink-url` is set** (`--events-sink-secret` optional), with
`event_types = [COMMITTED_TRANSACTION, SAVED_METADATA, DELETED_METADATA]` — the three types carrying alert
transitions (lifecycle moves are transactions; snooze/unsnooze are metadata). Unset URL → no sink
(dev/test quiet). `AddEventsSink` swallows `AlreadyExists` (idempotent boot re-provision, matching
CreateLedger et al.).

**Checks:** build/vet/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit + full it-suite incl.
`TestIntegration_EventsSink` (add → get → idempotent re-add no-op → remove → double-remove no-op);
**boot smoke** with `--events-sink-url` — the boot log + `ledgerctl events list` confirm the sink
registered with the right event types + endpoint and the ledger attempting delivery (smoke sink removed
after); no-flag boot clean. Conventional commit; not on `main`; no OpenAPI change. No CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| — | LOW | Sink filtering is by event **type, not ledger** — the webhook receives events for all ledgers (incl. ledger-connect data-ledger writes); consumers filter on `event.ledger == control`. Per-ledger filtering is upstream-future (RFC §4.4). | ✅ noted |
| — | LOW | Server is **add-only** (not add-or-update despite the proto comment): changing an existing sink's endpoint/filter needs a manual `RemoveEventsSink` first, which resets the per-sink cursor (re-delivery from the log head). Documented on the method; operator action. | ✅ documented |
| — | LOW | A failed `AddEventsSink` at boot **fails startup** (consistent with the provisioner). Right for a genuine config error; a transient ledger blip would crash-loop → k8s restart self-heals. Soften to log-and-continue if flaky. | ⬜ noted |

## Workstream: checkpoint alternative (live reads + `_recon` capture)

Owner-steered pivot (2026-07-08, ADR-003): **drop query checkpoints entirely** in favour of live
reads + an immutable audit-grade **capture transaction** in `_recon`. Rationale + option analysis in
[ADR-003](../prd/adr-003-checkpoint-anchor-and-crosscheck.md); the multi-ledger atomic-read gap is
tracked upstream as **EN-1480** (batched read on one snapshot). Staged: (1) remove the runtime
cross-check, (2) remove checkpoints + live reads, (3) capture-as-transaction, (4) docs.

### Checkpoint alternative — Étape 1: remove the kernel/template cross-check (`13b0357`, SDLC review, 2026-07-08)

The three *aggregate* templates (`source_parity`, `ledger_invariant`, `account_threshold`) re-ran their
check through the CEL kernel (`eng.Compile`+`eng.Evaluate` per asset) and asserted the kernel verdict
matched the direct `big.Int` math (`kernel/template disagreement`). Under a checkpoint (or a single
live snapshot) both paths read the **same** state, so the cross-check only re-verified arithmetic
equivalence on identical inputs — a code property, at the cost of 2·N / T·N / N extra reads per eval.
It is also the structural blocker to live reads (two live reads diverge under concurrent writes).

**Change:** direct math is authoritative; the rendered CEL stays in `evidence.compiledCEL` (explain-
ability) and `eng.Compile` stays at rule create (`service/rule.go`, validation, no reads). `eng.Evaluate`
now has no runtime consumer for built-in templates (reserved for post-GA power-mode) — a documented
narrowing of ADR-001 §11. The equivalence moved to a golden test `TestCrossCheck_DirectMathMatchesRenderedCEL`
(direct verdict == rendered-CEL verdict over representative pass/fail specs incl. ledger↔pool). New
helper `templates.poolPitPerSource` reproduces the Tier-2 `pit_per_source` the kernel used to populate
(only `source_parity` can carry a pool source; invariant/threshold are ledger-only → empty).

**Checks:** `go build ./...` ✅ · `go vet ./internal/templates/… ./internal/engine/…` ✅ · `go test -race
./internal/...` green ✅ · gofmt clean (also normalised the pre-existing comment reflow in
`ledger_invariant.go` since it's now edited) ✅ · `golangci-lint ./internal/templates/...` 0 issues ✅ ·
not on `main` ✅ · no OpenAPI change ✅ · `engine`/checkpoint code untouched (Étape 2). No CRITICAL/HIGH.

| # | Sev | Finding | Status |
|---|---|---|---|
| — | LOW | `eng` is now an unused param of `LedgerInvariant.Evaluate` (interface-required; lint skips interface methods). Kept for the `Evaluator` contract; still used by the other two templates' per_account paths. | ✅ acceptable |

### Checkpoint alternative — Étape 2: drop checkpoints, live reads (`8979795`, SDLC review, 2026-07-08)

Reconciliation no longer creates query checkpoints. Ledger reads are **live** — a single
`AggregateVolumes` is computed against one server-side Pebble snapshot (internally consistent);
cross-ledger reads are per-source, their skew absorbed by tolerance (ADR-003). The atomic
multi-ledger cut is deferred to a ledger primitive (**EN-1480**).

Removed: `internal/ledger/checkpoint.go` + `checkpoint_registry.go` (Acquire/Release/wait/record/
forget/reap); client `CreateQueryCheckpoint`/`DeleteQueryCheckpoint`; the `checkpointID` param from
the client read helpers (`AggregateVolumes`/`QueryAccountsFunc`/`QueryAccounts`/`GetAccount` — always
live) and from the engine (`EvalInput.CheckpointID`, `LedgerResolver`), templates, and the
`ledgerresolver` adapter; `service.Checkpointer` (+ the per-eval Acquire/Release in `EvaluateRule`);
the `cmd` checkpointer provider + startup reaper. `CheckpointReader` → `Reader`/`NewReader` (it reads
live). ledgerstore control-ledger reads pass no anchor; mock regenerated. it-tests rewritten to the
live model (`TestIntegration_LiveReads` / `LiveListAccounts` / `EvaluateLive`);
`checkpoint_registry_it_test.go` deleted.

**Findings closed:** **F26** (checkpoint lifecycle/reaper) and **F32** (checkpoint read-index race)
are now **without object** — there are no checkpoints.

**Checks:** build/vet(+`-tags it`)/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit
green; full it-suite (`-p 1 -count=1`, `recon-it4`) green against the live ledger — live
`source_parity` PASS→FAIL end-to-end, live per-account reads + budget abort. Conventional commit; not
on `main`; no OpenAPI change. Large net deletion.

| # | Sev | Finding | Status |
|---|---|---|---|
| — | LOW | Live reads are eventually-consistent on the read index (F25/F27): a read just after a write may briefly lag. Acceptable for reconciliation (settled balances; the next tick re-observes); a `min_log_sequence` freshness floor is available if ever needed (deferred). | ✅ noted |
| — | LOW | `in engine.EvalInput` is now unused in `LedgerInvariant`/`AccountThreshold` `Evaluate` (interface-required); still consumed by `SourceParity` (pool PIT) + carried for the period/`RecordCapture` in the service. | ✅ acceptable |

### Checkpoint alternative — Étape 3: audit-grade capture in `_recon` (`932e931`, SDLC review, 2026-07-08)

Every evaluation now records an immutable **capture** transaction on the control ledger (ADR-003):
the durable "what reconciled and when" — positive assurance on a pass, break evidence on a fail —
recorded independently of the alert lifecycle. This revises the "evaluations non-durable" decision
(RFC §4.4.2): the durable record is the capture transaction, not a queryable evaluation table.

- **Chart** (+2 account types, +1 asset, +1 numscript): `capture:rule:{ruleId}:per:{period}` (NORMAL)
  bucket + `capture:pool:rule:{ruleId}` (NORMAL) mint source; asset `CAPTURE` (precision 0); numscript
  `capture` mints 1 CAPTURE from the pool into the bucket. `-balance(bucket, CAPTURE)` = captures per
  (rule, period); each capture is a distinct transaction, so the account's tx log is the period's
  ordered series.
- **Snapshot** on the transaction metadata (`COMMITTED_TRANSACTION`, undeclared/self-describing):
  `type, rule_id, template_kind, period, evaluation_id, captured_at, verdict, trigger, evidence`. The
  immutable, receipt-signed transaction is the audit record.
- **Store**: `ledgerstore.RecordCapture` (`internal/ledgerstore/capture.go`) via `CreateTransaction`,
  idempotent per (rule, period, evaluation) (`alertActionKey("capture", …)`); `store.CaptureInput`.
- **Wiring**: `EvaluateRule` records the capture inside the eval's `inTx` (before driving alerts);
  `EvaluateRuleRequest.Trigger` (`scheduled`|`manual`, default manual) — set by the scheduler,
  defaulted for the API.

**Chart change → it control-ledger bumped `recon-it4` → `recon-it5`** (F8).

**Checks:** build/vet(+`-tags it`)/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit green
(incl. `TestEvaluateRule_RecordsCapture` — verdict + trigger wiring — and `TestNumscriptCapture`);
full it-suite (`-p 1 -count=1`, fresh `recon-it5`) green — `TestIntegration_RecordCapture` proves the
CAPTURE counter increments per eval and a same-eval replay is idempotent, on the live ledger.
Conventional commit; not on `main`; no OpenAPI change.

| # | Sev | Finding | Status |
|---|---|---|---|
| — | LOW | Continuous rules write one capture transaction per evaluation (the assumed cost of the transaction form). Throttling (on-change / heartbeat) is a documented follow-up if continuous volume bites. | ⬜ noted |
| — | LOW | The capture stores the evaluation's (failing-outcome) evidence + verdict; full pass-side observed balances (all outcomes) and a ledger-signed read proof (EN-1480) are documented follow-ups. | ⬜ noted |
| F8 | — | Chart evolution on an existing ledger is still unhandled: a fresh deploy gets the capture chart via CreateLedger; an existing one needs an idempotent add-types pass. it-ledger bumped to `recon-it5`. | ⬜ open |

## Workstream: metadata type audit (typed schema round-trip)

Audit of every metadata key recon declares/writes/reads against the ledger's 11 typed
`MetadataType`s (STRING/INT64/UINT64/INT8/16/32/UINT8/16/32/BOOL/DATETIME), verifying the
**declared type (`schema.MetadataSchema`) ⇔ value written (`ledgerstore` `strVal`/`boolVal`/`dtVal`)
⇔ value read (`getStr`/`getBool`/`getTime`)** are coherent end-to-end for every field.

**Conclusion: the control schema is already correctly typed** — no string↔int mismatch. UUIDs
(`id`, `rule_id`, `last_evaluation_id`), enums (`status`, `severity`, `template_kind`, `cadence`),
free text (`name`, `fingerprint`, `compiled_cel`), the `period` id (alnum+`-`, not an int), and all
JSON blobs (`evidence`, `resolution`, `ack`, `snooze`, `schedule`, `spec`, `notifications`) are
correctly **STRING** (the ledger has no UUID/enum/JSON metadata type). Timestamps are **DATETIME**
(micros), `enabled` is **BOOL**. The one true counter — `occurrence-count` — is correctly modelled as
the **OCC asset balance**, not metadata. Dynamic `label.*` / `last_transition` stay undeclared STRING
by design. Capture-tx metadata is undeclared, transaction-level, audit-only and never read back, so
its `captured_at` as an RFC3339Nano string (vs the alert-side DATETIME micros) is an intentional
human-readable-envelope choice, not a mismatch — left as-is.

**Two fixes landed:**
- **Dead schema declarations removed** — `reopened_at` (DATETIME) and `parent_resolution` (STRING)
  were declared in `MetadataSchema()` but **never written or read** (no `models.Alert` field, no
  producer/consumer, no docs ref). `parent_resolution` also *contradicted* the reopen design
  ("a reopen drops the prior closure — its audit lives in the event stream", `alert.go`). Removed
  both the constants and the schema entries. A declared-but-unpopulated field is a coherence defect
  (and reserves a forward-index encoding slot for nothing). If `reopened_at` is ever wanted, its
  timestamp is already in `last_transition.occurredAt`; re-add via the F8 schema-bump discipline.
- **`MetadataToMap` made lossless (F33)** — the flatten kept **only `StringValue`**, silently
  dropping any INT64/UINT64/DATETIME/BOOL/NullValue key. It feeds the **data-ledger** read path
  (`ledger.Reader` → `resolver.accountFromProto` → `engine.Account.Metadata`), where recon does not
  control the (customer-declared) metadata types. Latent today (that field is not yet read by any CEL
  builtin/template — the CEL env exposes only `ledgerSet`/`pool`/`balance`/`balances`/`sum`/`abs`),
  but a landmine the moment account metadata is surfaced to CEL. Now stringifies every scalar
  (int/uint → base-10, bool → `true`/`false`, datetime → RFC3339Nano UTC, null → its preserved raw
  original); no key is dropped except a nil/unset value. New round-trip tests in
  `internal/ledgerpb/commonpb/metadata_helpers_test.go`.

**Schema change → it control-ledger bumped `recon-it5` → `recon-it6`** (F8): removing declared fields
changes the `CreateLedger` schema, so a fresh control ledger is provisioned for the it-suite.

**Checks:** build/vet(+`-tags it`)/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit green
(incl. the new `MetadataToMap` lossless-flatten + round-trip tests); full it-suite (`-p 1 -count=1`,
fresh `recon-it6`) green — schema provisions cleanly with the two fields gone. Not on `main`; no
OpenAPI change.

| # | Sev | Finding | Status |
|---|---|---|---|
| F33 | LOW | `commonpb.MetadataToMap` silently dropped non-`StringValue` keys when flattening ledger metadata to `map[string]string` (data-ledger read path → `engine.Account.Metadata`), so a typed data-ledger field would vanish from a rule's view. Latent (the field is not yet read by CEL). **Fixed:** lossless stringify of every scalar type. Full typed metadata in the CEL object model (a typed `map[string]any`) stays a Phase 4 concern. | ✅ fixed |
| F8 | — | Same schema-evolution gap as before: removing the two dead fields is only picked up by a fresh `CreateLedger`; an existing ledger keeps the orphan declarations until an idempotent reconcile pass (`SetMetadataFieldType`/`RemovedMetadataFieldType`) exists. it-ledger bumped to `recon-it6`. | ✅ superseded by idempotent-schema-provisioning (additive) |

## Workstream: idempotent schema provisioning (F8 resolved)

`Provision` runs on every boot but only ever called `CreateLedger`, which applies the full chart
**atomically on first boot** and returns `AlreadyExists` (swallowed) forever after — so an
already-created control ledger never received later **additive** chart changes (new account type /
metadata key). The standing workaround was renaming the it-ledger (`recon-it4`→`it5`→`it6`) to force
a fresh create, which is impossible in production. This closes the recurring F8.

**Fix:** after `CreateLedger`, `Provision` reads the ledger's current chart (`GetLedgerInfo`) and runs
two **delta-based** reconcile passes — applying only what is missing or mistyped:
- **Account types** — `AddAccountType` for types the ledger does not have (sorted for a deterministic
  apply order); the client still swallows `AlreadyExists` as a race/read-lag backstop.
- **Metadata fields** — `SetMetadataFieldType` for a key only when it is absent OR its declared type
  differs from the chart.

**Why delta, not unconditional (F34):** the ledger FSM (`processSetMetadataFieldType`) has **no
unchanged-type guard** — every `SetMetadataFieldType` on an *indexed* field bumps its
`forward_encoding_version` and flips the index to BUILDING, i.e. schedules a forward-index **rewrite**.
recon indexes 13 metadata fields, so a naive "re-declare every field on every boot" would rewrite all
13 indexes on **every boot**. Reading the current schema and skipping already-correct fields keeps a
steady-state boot free of schema mutations (proven: the full it-suite's per-test re-provisions on the
shared `recon-it6` issue zero `AddAccountType`/`SetMetadataFieldType`).

New client methods `Client.{GetLedgerInfo,AddAccountType,SetMetadataFieldType}` (mutations routed
through the standard `Apply` batch; `GetLedgerInfo` maps `NotFound`→`(nil,nil)`) + `Client.DeleteLedger`
(test-hygiene, swallows `NotFound`). `provisionAPI` + its mock extended.

**Scope / residual:** the reconcile is **additive**. Destructive evolution stays out of scope and is
the *only* case still needing an it-ledger rename: removing a declared field (needs
`RemoveMetadataFieldType` — an orphan declaration is otherwise harmless, no writes), retyping a field
on populated data, or changing an account type that already holds accounts
(`ACCOUNT_TYPE_HAS_ACCOUNTS`). A genuine account-type redefinition surfaces its error rather than
being silently applied.

**Convention change:** an it-ledger rename is no longer required for an **additive** chart change —
only for a destructive one (handoff + step-3c-4 note updated).

**Checks:** build/vet(+`-tags it`)/`golangci-lint --build-tags it` (0)/gofmt clean; `-race` unit green —
`TestProvisioner_Provision` (bare ledger → full chart reconciled) + `TestProvisioner_UpToDateLedgerSkipsReconcile`
(the churn guard: a fully-provisioned ledger issues **zero** `AddAccountType`/`SetMetadataFieldType`).
Full it-suite (`-p 1 -count=1`, `recon-it6`) green with the reconcile active — the shared ledger's
per-test re-provisions stay clean no-ops. New it-test `TestIntegration_ProvisionReconcilesExistingLedger`
seeds a **stale** ledger (only the `rule` type, no schema), provisions, and asserts via `GetLedger` that
every chart account type + metadata field is now present, then re-provisions idempotently (unique
ledger, deleted on teardown). Not on `main`; no OpenAPI change.

| # | Sev | Finding | Status |
|---|---|---|---|
| F8 | MED | Additive schema/chart evolution on an existing ledger — **resolved** via delta reconcile in `Provision`. | ✅ resolved |
| F34 | LOW | Ledger `SetMetadataFieldType` has **no unchanged-type guard**: it bumps `forward_encoding_version` (an index rewrite) even when the declared type is identical. recon works around it by diffing against `GetLedgerInfo` and only declaring the delta. Upstream fix: the FSM could no-op an identical redeclaration. | 🟡 mitigated in-recon; upstream follow-up |
| — | LOW | Destructive evolution (remove/retype a field, change a populated account type) is still unhandled and needs an it-ledger rename or a manual `RemoveMetadataFieldType`/type migration. Low frequency; documented. | ⬜ noted |

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
