# RFC — Ledger-native storage for Reconciliation (drop Postgres)

**Status:** Draft — **partially superseded.** The storage decision (drop Postgres → `_recon`) landed.
Two later decisions overtook this doc's read model: (1) query checkpoints were **removed** in favour of
live reads + an immutable `_recon` capture ([ADR-003](../prd/adr-003-checkpoint-anchor-and-crosscheck.md)),
and (2) reconciliation is now **strictly ledger↔ledger** — the Payments/Tier-2 pool `Source`,
`pool()` builtin, `PaymentsResolver`, and the Formance SDK were removed (ledger-only increment,
2026-07-09; see the migration log). References below to "pointing the pool side at a ledger", the
`PaymentsResolver` interface, and checkpoint reads are historical planning context — the current-state
surface is [architecture.md](../technical/architecture.md).
**Owner:** Arnaud
**Date:** 2026-07-02
**Depends on:** Ledger v3 (`release/v3.0`), ledger-connect, PR #83 (Ledger Clarity V1)
**Related:** [ADR-002 — PIT consistency](../prd/adr-002-pit-consistency.md), [ADR-003 — live reads + capture](../prd/adr-003-checkpoint-anchor-and-crosscheck.md), [architecture.md](../technical/architecture.md), upstream [ledger#1416](https://github.com/formancehq/ledger/issues/1416)

---

## 1. Problem

Reconciliation currently persists all of its control-plane state (`rule`, `evaluation`,
`alert`, `alert_event`, legacy `policy`/`reconciliation`) in **PostgreSQL** via bun
(`internal/storage/`). We want to **remove the Postgres dependency** to simplify
deployment, and to align with the platform direction: Ledger v3 itself dropped Postgres
in favour of an embedded Pebble + Raft store, and Payments data now lives *inside*
ledgers (via ledger-connect writing counterparty events as transactions). Once both
sides of a reconciliation are ledgers, the natural end state is *"reconciliation is a
stateless service that reads ledgers and records its findings back into a ledger"* — no
database of its own.

This RFC specifies **how** to migrate reconciliation's storage onto the ledger, which
parts map cleanly, which do not, and the constraints from the ledger repo we must respect.

## 2. What is already true (do not rebuild)

- **Payments-in-Ledger is live.** ledger-connect is a stateless gRPC client that maps
  counterparty events (PSP, banks, schemes, chains) into double-entry transactions in a
  **dedicated ledger per source** (`apideck:*`, `fpay:*`, `bitcoin`, …). Each source
  declares its schema up-front via `GetSchema` (metadata field types, account types,
  indexes, prepared queries) and persists its ingestion **cursor as account metadata**
  on a `cursor:{source}` account, advanced atomically in the same `Apply` batch as the
  data (`ledger-connect/internal/application/engine/worker.go:449`).
- **Ledger↔Ledger reads are already possible.** PR #83's `Source` primitive +
  `source_parity` template compare two balance sources of *any* kind, including
  `ledger↔ledger` (`internal/templates/source_parity.go`). The read side of the vision
  is largely done; what remains is pointing the "pool" side at a ledger and fixing PIT
  (see §4.6).

## 3. Non-negotiable constraints from Ledger v3

These come straight from the ledger repo's `CLAUDE.md` invariants and docs. They rule
out the naïve "put my tables in the ledger's Pebble" idea and shape everything below.

| Constraint | Implication for Reconciliation |
|---|---|
| **No embedded/library mode** — the ledger is a Raft-clustered service reachable only via gRPC (`BucketService`) / HTTP. | Reconciliation talks to the ledger **over the network**. There is no in-process access to its Pebble. We are a *client*, exactly like ledger-connect. |
| **Pebble is not a general-purpose KV** — it is an audit-log-backed **projection** store. Invariant #8: every persisted dataset other than the audit chain must be re-derivable and **verified by the `checker`**. | We must **not** add a `recon_*` dataset inside the ledger's Pebble. Instead we use the ledger's *public data model* — accounts + typed metadata — which are already first-class, audit-covered citizens. |
| **FSM is deterministic, no Pebble reads on the hot path, coverage gate (#3/#6/#9).** | Only relevant if we modified the ledger core — we do not. Staying a pure API client keeps us entirely outside the FSM. |
| **No arbitrary point-in-time queries** — v3 deliberately dropped v2's `moves`-table PIT; historical reads use **chapter-boundary snapshots / query checkpoints / on-demand replay** (`ledger/docs/drafts/prepared-queries.md §14`). | Our "compare A and B at the same instant" model (ADR-002) must be rebuilt on checkpoints, not on a `pit` timestamp. See §4.5. |
| **Metadata is scalar + typed + last-write-wins** (`string / int64 / uint64 / bool / datetime`); long values are a concern for the read-side index (`ledger/docs/drafts/advanced-read-queries.md:1111`). | JSON specs/evidence become **string** values; we index only scalar fields we actually query on, and keep large blobs unindexed (or out of the ledger). No native append (see §4.5). |
| **Event system exists, but emits *generic log-derived* events, not custom domain events.** The ledger emits one ordered, at-least-once event per committed log — including `SAVED_METADATA` — to configurable sinks; there is no API to publish a custom semantic event today (`ledger/docs/technical/architecture/subsystems/events-mirror/events.md`). | Our append-only history rides `SAVED_METADATA` events off the `_recon` log for free (§4.4). What's missing — semantic event types, per-ledger sink filtering, replay API — is the *future* generic event-log, now an enhancement rather than a blocker. |

**Bottom line:** we store reconciliation state *in the ledger* (as accounts + metadata
via the public API), **never** *in the ledger's Pebble* (as a new internal dataset).

## 4. Proposal

Reconciliation becomes a **stateless service** (like ledger-connect). Its control-plane
state is persisted as **account metadata in a dedicated control-ledger** — call it
`_recon` — using **`AddMetadata` actions only, no transactions** (per direction: we do
not abuse postings for non-financial artifacts).

> **Design horizon — dedicated events, if they fit.** Metadata on zero-balance accounts is
> the *pragmatic* path: no new ledger feature, and the exact pattern ledger-connect already
> runs in production. But it does mildly repurpose the account model. The **definitive
> future** we aim for is **first-class, dedicated events** — a reconciliation state change
> *is* an event in the ledger log, not a metadata write on a synthetic account. We switch
> the moment the generic event-log can also materialise **current state** (an alert's
> status) without replaying history (§4.4); if metadata-on-accounts proves clean enough, it
> simply stays. The line we never cross either way: **no abusing transactions/postings** for
> non-financial state.

### 4.1 Control-ledger: chart of accounts + metadata schema

Accounts are pure **metadata carriers** (zero balance, never touched by a transaction).
Declared up-front via `SetMetadataFieldType` + account types, exactly as ledger-connect
declares `cursor:{source}` (`worker.go:230`).

| Entity | Account address | Key metadata (typed) | Indexed |
|---|---|---|---|
| Rule | `rule:{ruleID}` | `template_kind` (string), `enabled` (bool), `severity` (string), `schedule` (string, ISO8601), `spec` (string, JSON), `compiled_cel` (string), `updated_at` (datetime) | `template_kind`, `enabled`, `severity` |
| Alert (current state) | `alert:{ruleID}:{fingerprint}` | `status` (string), `severity` (string), `first_seen_at` (datetime), `last_seen_at` (datetime), `occurrence_count` (int64), `last_evaluation_id` (string), `evidence` (string, JSON), `resolution` (string, JSON) | `status`, `severity` |
| Global config / leases | `config:main` | scheduler lease, feature flags | — |

### 4.1.1 Alert lifecycle: metadata-driven vs state-in-account

The alert lifecycle (`OPEN → ACKNOWLEDGED → RESOLVED / ACCEPTED`, with reopen) is a state
machine, and the ledger offers two idiomatic representations. The second mirrors how the
Payments plugins already model state — a value moves between hold/state accounts, transient
ones declared with the **EPHEMERAL** persistence class (`ledgerctl at add … --persistence
ephemeral`: volumes purged when the account reaches zero balance; `transient` = never
persisted, must net to zero in-batch).

- **Metadata-driven** — the alert is one account; `status` is a last-write-wins metadata
  field; a transition is a single `AddMetadata`. Simplest; a point-read gives current
  state; but no illegal-transition guard (LWW silently accepts a double-resolve) and history
  must be reconstructed from the `SAVED_METADATA` stream.
- **State-in-account** — a 1-unit marker (a synthetic `ALERT` asset) moves between
  per-state accounts `alert:{rule}:{fp}:{state}` via a transaction; transient states are
  EPHEMERAL. Buys three things metadata can't:
  1. **Atomic illegal-transition guard** — Numscript `source = @alert:{fp}:open` fails the
     whole tx if the marker isn't there. A free CAS on the lifecycle.
  2. **Semantic + immutable history** — each transition is a `COMMITTED_TRANSACTION` whose
     postings encode `from-state → to-state`; audit-chain-native, chapter-archivable.
  3. **Balance-native queries** — "how many OPEN per rule" is an `AGGREGATE_VOLUMES` over
     `*:open`; occurrence counters are monotonic balances; burning the marker on close
     leaves zero hot-state.

  Costs: a per-state chart of accounts + Numscript per transition; reopen/parent-link
  modeling; a single alert's full status needs a bounded scan of its state accounts.

**Lean — split by dimension (hybrid).** Lifecycle status → state-in-account (idiomatic,
guarded, semantic history, EPHEMERAL transient states, burn-on-close). Descriptive
attributes (severity, evidence, notes) → metadata. Counters/timestamps → balances + tx
timestamps. This is **not** the transaction-abuse we ruled out — a transition genuinely *is*
a movement (how the plugins model payments); the line we keep is *no config/JSON blobs in
postings*. Metadata-only remains a valid, simpler V1 if the team prefers minimal modeling
over day-one invariant enforcement. ~~Decision for review.~~ **Decided: hybrid (§4.1.2).**

A third variant — **state-as-asset on a single account** (encode the state as a burnable
`S_{state}` asset on the `item`, guard via burn instead of a move between state-addressed
accounts) — was considered to collapse `item` + `st:{state}` into one account (3 types, no
EPHEMERAL). **Rejected** (2026-07-03): the separation resolves a real addressing conflict (the
CAS guard needs a *moving* state address; metadata needs a *stable* one), and keeping state in
the address is more self-describing (§4.1.3) and matches the Payments-plugin idiom. The wins were
marginal at recon's write volume. `item` and `st:{state}` stay separate.

### 4.1.2 Decided model & account naming (hybrid)

Verified against Ledger v3: overdraft is a Numscript source clause (`allowing unbounded
overdraft`, `docs/ops/performance-tuning.md`); EPHEMERAL purges **volumes only, not
metadata** (`internal/infra/state/write_set_ephemeral_purge.go` `partitionVolumes`);
`AGGREGATE_VOLUMES` sums over an `AddressPrefix` (`read-path/prepared-queries.md`); account
types assign a persistence class per address pattern (`ledgerctl account-types add`).

Assets in `_recon`: `ALERT` (marker, precision 0) · `OCC` (occurrence counter).

Segment order (implemented): `rule → per → fp`, so `(rule, period)` is a queryable address
prefix (step 3c-1).

| Family | Pattern | Persistence | Holds | Role |
|---|---|---|---|---|
| Rule | `rule:{ruleId}` | NORMAL | metadata | config |
| Alert item (canonical) | `alert:item:rule:{ruleId}:per:{period}:fp:{fpHash}` | NORMAL | metadata + `OCC` balance + `status` mirror | O(1) point-read, description, occurrence count |
| State marker | `alert:st:{state}:rule:{ruleId}:per:{period}:fp:{fpHash}` | **EPHEMERAL** | 1 × `ALERT` | lifecycle source-of-truth, transition guard, per-status prefix aggregation |
| Source pool | `alert:pool:rule:{ruleId}` | NORMAL (overdraft at mint) | negative `ALERT` + negative `OCC` | mints markers **and** OCC units; two free per-rule gauges (see below) |

**Per-rule source pool (decided).** One pool per **rule** (not per rule+period) sources *both* the
ALERT markers and the OCC counter units for all of the rule's periods. Assets are independent
within an account, so nothing mixes: `−balance(ALERT)` = live-alert count, `−balance(OCC)` = total
occurrences (both per rule, all periods). Two deliberate cardinality cuts, each verified to lose
nothing the design relies on:
- **OCC merged into the pool** (not a separate `alert:occ`): a separate pool would only add a
  redundant account — the occurrence total is also derivable by aggregating item OCC balances.
- **Keyed by rule, not rule+period**: the pool is *only* a mint source (STRICT needs every posting
  source declared) plus an optional gauge; the period scoping added O(#periods) NORMAL accounts
  (never purged) for a gauge that is (a) currently unread and (b) already derivable by aggregating
  the EPHEMERAL `st:` markers (live count, bounded by the live set — how `PQOpenCount` already
  works) or the item OCC balances (occurrences). `@world` is STRICT-exempt and was an option, but a
  declared per-rule pool keeps the chart self-describing and bounds the source footprint to
  O(#rules). Chart stays 4 account types.

Naming rules honoured: a fixed descriptor precedes every variable segment (`rule:`, `per:`,
`fp:`, `st:`), and **status is leftmost** in the marker family so everything after it
aggregates under `alert:st:{state}:`. `{fpHash}` = a hash of the raw fingerprint (raw string
kept as metadata) because fingerprints contain `:`/`|` that would break address segmentation.

**Queries:**
- Status / full alert (one) → `GetAccount(alert:item:rule:R:per:P:fp:H)` (`status` metadata + OCC). O(1).
- OPEN count global → `AGGREGATE_VOLUMES` of `ALERT`, `AddressPrefix("alert:st:open:")`.
- OPEN count per rule → prefix `alert:st:open:rule:R:`.
- Live alerts per rule → `-balance(alert:pool:rule:R, ALERT)` (free); per rule+period → `AGGREGATE_VOLUMES(ALERT)` over `alert:st:open:rule:R:per:P:` (+ ack).

**Transitions run as library Numscripts** (stored via `SaveNumscript` at provisioning, pinned
`v1.0.0`, referenced by name + account vars — not inlined per call; see the numscript-library
doc). Accounts are passed as `vars`:
- *Open* (`alert_open`) — mint from the pool → `send [ALERT 1] (source = $pool allowing unbounded overdraft, destination = $st_open)` + `send [OCC 1] (source = $pool …, destination = $item)`; set `status`/description metadata on item.
- *Repeat* (`alert_bump`) — `+OCC` from the pool → item; refresh `last_seen`/evidence (marker stays at `st:open`).
- *Reopen / resurface* (`alert_reopen`) — guarded move `$st_from → $st_open` (bare source = CAS, fails if the marker isn't at `{from}`; drained `{from}` purges) + `+OCC`; mirror `status` back to OPEN; a reopen also deletes the prior `resolution`/`ack`.
- *Ack / Resolve / Accept* (step 3c-3) — guarded move `alert:st:{from}:… → alert:st:{to}:…` (no OCC).
- *Close* (future) — burn the marker back to `alert:pool:…` → ALERT balance rises toward 0 (live count drops); full lifecycle preserved in the tx log (archivable).

**Consequences:**
1. Metadata lives on the NORMAL `item` account only; markers stay metadata-free (EPHEMERAL
   purges volumes, not metadata). `status` is denormalised onto `item` for O(1) reads, but the
   marker is the guarded source-of-truth; both are updated in one atomic batch.
2. Never aggregate at the bare `alert:` prefix (markers `+`, pool `−`, OCC would mix) — always
   at the family prefix (and per asset).
3. The mint is unguarded (pool overdraft always succeeds); concurrent double-open of the same
   fingerprint is handled by the batch idempotency key + serialized per-rule evaluation. All
   other transitions are guarded.
4. Notify vs suppress maps onto event type: transitions are `COMMITTED_TRANSACTION`
   (notifiable); repeat-occurrence bumps are `SAVED_METADATA` (suppressible).

### 4.1.3 Strict chart-of-accounts enforcement

Like a ledger-connect plugin declaring its schema, the module owns and **strictly enforces**
its chart of accounts on `_recon`: any write to an address outside the declared patterns is
**rejected by the ledger**, not merely discouraged by convention.

Mechanism (shipped, verified in `docs/ops/cli.md`):

- `ledgerctl account-types add <name> <pattern> --persistence <mode>` declares each family's
  address pattern — fixed segments + **regex-constrained variables**, e.g.
  `alert:st:{state:^(open|ack|resolved|accepted)$}:rule:{ruleId:^[0-9a-f-]{36}$}:fp:{fpHash:^[0-9a-f]{16}$}:per:{period}`.
  Each family's pattern also carries its persistence class (EPHEMERAL for `alert:st:*`,
  NORMAL for the rest).
- `ledgerctl account-types set-default-enforcement --mode STRICT` makes the **FSM reject
  untyped accounts** (deterministic across replicas). `AUDIT` logs violations but allows them
  — used for gradual rollout.

Benefits:

- **Integrity by construction.** A malformed or rogue write to `_recon` (a bug in our own
  Numscript, or a third party writing to the ledger) is rejected at admission/FSM — the
  account format decided by reconciliation is guaranteed, not assumed.
- **Self-describing ledger.** The declared patterns *are* the schema; operators and advanced
  users read the chart to understand the model.

Consequence for sources: to keep STRICT clean, **mint from the declared overdraft pool**
(`alert:pool:*`, one per rule, sourcing both `ALERT` and `OCC`; a future `COST` asset can
ride the same pool) rather than `@world`, so every address in every posting matches a declared
type. (Confirm `@world`'s treatment under STRICT; if it is not implicitly exempt, declare it or
avoid it entirely via the pool.)

Rollout: start in **AUDIT** during the POC to surface any pattern the code emits that we
didn't declare, then flip to **STRICT** once the chart is stable.

### 4.2 Write model

- **One `Apply` batch per evaluation.** An evaluation that opens/updates N alerts emits N
  `AddMetadata` actions in a single `ApplyBatch` — atomic at the batch level. No
  cross-account transaction needed.
- **Idempotency:** every batch carries an idempotency key (`ApplyBatch.idempotency_key`)
  derived from `(ruleID, evaluationID)`, so replays after a crash are safe — the same
  mechanism ledger-connect relies on for at-least-once ingestion.
- **Reuse the existing client shape:** `ledger-connect/internal/infra/ledger/client.go`
  (`SaveAccountMetadata`, `CreateLedger`, `Apply`) is the reference implementation to copy.

### 4.3 Read / query model (replacing SQL)

| Today (SQL) | Ledger-native |
|---|---|
| `SELECT * FROM rule WHERE enabled AND schedule matches` | `ListAccounts(_recon, prefix="rule:", filter: enabled==true)` + client-side schedule bucketing |
| Upsert alert on `(rule_id, fingerprint)` unique index | **Structural dedup:** the address `alert:{rule}:{fingerprint}` *is* the unique key. One account per fingerprint; `AddMetadata` (LWW) is the upsert. The Postgres partial-unique-index + concurrency race disappears. |
| `SELECT active alerts for rule` | `ListAccounts(_recon, prefix="alert:{rule}:", filter: status=='OPEN')` |
| Aggregate balances of a data-ledger account set | `ExecutePreparedQuery(AGGREGATE_VOLUMES)` on the data-ledger (already used by the resolvers) |

This removes bun, migrations, triggers, and the partial unique index entirely.

### 4.3.1 Query abstraction & index management

Ledger v3 gives three read primitives the module must drive (analogous to a ledger-connect
plugin's `GetSchema`, applied to `_recon`):

- **Filter language** (`internal/pkg/filterexpr`): `metadata[k] == v` / `in` / `between`,
  `address == "foo:*"` (prefix), `exists metadata[k]`, with `and/or/not`.
- **Prepared queries**: named, per-ledger, pre-validated at creation, run as `LIST` or
  `AGGREGATE_VOLUMES` with runtime params.
- **Indexes**: `SetMetadataFieldType` declares a typed field **and builds its forward
  index**; `CreateIndex` for the rest. Build is **async** — queries return `ErrIndexBuilding`
  until the per-replica `IndexVersionState.CurrentVersion` is ready.

The module therefore owns two layers:

1. **Schema/index provisioner (bootstrap, idempotent).** Declare the **account-type patterns**
   (with persistence class + regex-constrained variables) and set **STRICT enforcement**
   (§4.1.3). Declare the typed metadata fields we
   query — on `alert:item:*`: `status`, `severity`, `rule_id`, `period`, `first_seen_at`,
   `last_seen_at`, `label.*`; on `rule:*`: `enabled`, `template_kind`, `cadence`, `severity`.
   Register the standard prepared queries: `open-count` (`AGGREGATE_VOLUMES`,
   `address == "alert:st:open:*"`), `open-count-by-rule` (`… "alert:st:open:rule:$rule:*"`),
   `alerts-list` (`LIST`, `address == "alert:item:*" and metadata[status] == $status …`),
   `rules-enabled` (`LIST`, `address == "rule:*" and metadata[enabled] == true`). Gate
   dependent features on index readiness (handle `ErrIndexBuilding`).
2. **Filter translator (in `LedgerStore`).** Map recon's existing `go-libs/query.Builder`
   surface (status/severity/ruleId/labels + pagination) to a named prepared query + params or
   an ad-hoc `filterexpr` string. Standard users keep recon's normal query API and never see
   the account-naming scheme; advanced users can query `_recon` directly.

Routing: counts/aggregates → `AGGREGATE_VOLUMES` over an address prefix or the
`alert:issued:*` pool balance; filtered lists → `LIST` over `alert:item:*` metadata predicates.

### 4.4 Append-only history — ride the ledger's event system

**Key realisation: we do not need to build or run a message bus.** The Ledger v3 **event
system** (`ledger/docs/technical/architecture/subsystems/events-mirror/events.md`) already
emits, for **every** committed log entry, exactly one ordered, at-least-once event —
derived from the global log, leader-only, crash-safe, Raft-replicated. Crucially it
includes a **`SAVED_METADATA`** event type (alongside `COMMITTED_TRANSACTION`,
`DELETED_METADATA`, `CREATED_LEDGER`, …). Since our writes are metadata saves, **every
rule/alert change already produces an event carrying the full log payload**.

So the `_recon` **global log *is* the append-only history**, and delivery is a
cluster-level config, not code we maintain:

1. **Alert lifecycle history (`alert_event`).** No versioned-metadata-keys hack needed:
   the ordered `SAVED_METADATA` event stream for `_recon` *is* the transition log,
   deduplicable by the monotonic `sequence` field. Consumers filter by `event.ledger == "_recon"`.
2. **Evaluations — high volume, NOT a durable ledger table (deliberate).** **[Revised by
   [ADR-003](../prd/adr-003-checkpoint-anchor-and-crosscheck.md): each evaluation is now recorded as an immutable
   `_recon` **capture transaction** — durable and ledger-native. The reasoning below explains why a
   Postgres evaluation *table* was rejected; the capture is the durable, receipt-signed record that
   replaces it, covering passes (positive assurance) as well as breaks.]** Rationale: an
   evaluation is a **deterministic projection** — `result = f(rule spec, source balances @PIT)`
   — re-derivable because the ledger already holds the balances. Alerts, by contrast, carry
   non-derivable human state (ack, resolution notes) → alerts are durable, evaluations need
   not be. Plus: evaluations are high-churn (mostly PASS), and the ledger has no
   TTL/partition-drop for arbitrary accounts, so persisting every PASS bloats the hot set
   (PR #83 already planned partitioning + retention on the `evaluation` table — it was never
   "keep forever"). What *does* stay durable: the alert-backing `evidence` on `alert:item:*`.
   The remaining run-history is a spectrum to pick per product need:
   - **A. Minimal (default):** `last_evaluation` (result, PIT, evidence, cost) on the alert;
     no full history.
   - **B. Sink (recommended if history matters):** stream every evaluation to
     ClickHouse/Databricks → SQL-queryable history + retention, outside the hot set; recon
     still owns no storage.
   - **C. Bounded in-ledger:** last-N runs per rule as EPHEMERAL/ring accounts.
   - **D. Metering as balance:** `cost_units` → a `COST` balance per rule (free aggregation),
     independent of history retention.

   Recommendation: **A + D** by default, **+ B** when the product surfaces a run-history —
   without re-introducing Postgres.
3. **Delivery / notifications.** Configure a ledger **sink** via `AddEventsSink`
   (gRPC/Raft, runtime-reconfigurable). The **HTTP webhook sink ships in the light
   binary** (no build tag) → zero extra dependency; Kafka / NATS / ClickHouse / Databricks
   need their heavy build tags. **This lets reconciliation drop its own watermill/Kafka
   publisher from PR #83** — the ledger delivers to the Webhooks module and other
   interfaces directly.

**Sink filtering** is by event *type* (`SinkConfig.event_types`), not by ledger — a sink
for `SAVED_METADATA` receives metadata events for *all* ledgers (including data-ledger
writes from ledger-connect), so consumers filter on `event.ledger`. Per-ledger sink
filtering is listed as a *future* consideration upstream.

**What the future generic event-log still buys us** (enhancement, not blocker):
- **Semantic event types** — `reconciliation.alert.resolved` instead of a generic
  "metadata changed" that a consumer must interpret from the diff. Until then, write enough
  into the metadata payload (e.g. a `last_transition` field) that the `SAVED_METADATA`
  event is self-describing.
- **A replay API** to bootstrap a brand-new consumer from history (only persisting sinks —
  ClickHouse/Databricks — give that today).
- **Design the semantic envelope now** so adoption is a writer swap, and coordinate it with
  the ledger team + ledger-connect (shared need):
  ```
  { type: "reconciliation.alert.<transition>" | "reconciliation.evaluation",
    subject: "alert:{rule}:{fingerprint}" | "rule:{rule}",
    occurred_at: <datetime>, correlation_id: <evaluationID>, payload: <json> }
  ```

### 4.5 PIT consistency — query checkpoints (resolved)

> **Superseded by [ADR-003](../prd/adr-003-checkpoint-anchor-and-crosscheck.md).** Query checkpoints
> were removed: reconciliation reads its data ledgers **live** and records an immutable `_recon`
> **capture** per evaluation; cross-ledger skew is absorbed by tolerance, and a certifiable atomic
> multi-ledger read is a future ledger primitive ([EN-1480](https://formance-team.atlassian.net/browse/EN-1480)).
> The checkpoint reasoning below is retained as design history.

v3 has **no arbitrary PIT** (the v2 `moves`-diff approach was dropped). The anchor is a
**query checkpoint**, and for Ledger↔Ledger this is *stronger* than v2's cross-system PIT.

**Key property — a checkpoint is a globally-consistent cross-ledger cut.** All ledgers live
in one Raft group / one store, so a `CreateQueryCheckpoint` freezes A, B, and `_recon` at the
**same log sequence** simultaneously. "Compare A and B at the same instant" = read both with
the same `checkpoint_id`. Every read RPC honours it (`GetAccount`, `ListAccounts`,
`AggregateVolumes`, …). This removes the cross-system skew v2 had (Ledger vs a separate
Payments pool); the tolerance margin is now only needed for non-ledger sources.

**Flow.** An evaluation pins one `checkpoint_id` at the start; resolvers pass it (not a
`pit`) when reading A and B → PIT-consistent evidence; store `checkpoint_id` + log sequence in
the evaluation/evidence for reproducibility. Interface change:
`LedgerResolver.AggregateBalance(ctx, ledger, query, pit time.Time)` →
`(…, checkpointID uint64)` (`0` = live).

**Which checkpoint, by cadence:**
- *Periodic rules* (daily/weekly/monthly) → **scheduled checkpoints**
  (`query-checkpoint set-schedule`), one per period boundary, shared by all rules of that
  cadence → `checkpoint_id` ↔ `period_id`.
- *Continuous rules* → a **rolling `recon-current` checkpoint** refreshed every T seconds
  (create new, delete previous), read by all continuous evals in the window — amortises cost.
- *Skew-tolerant* → live read + tolerance margin (legacy fallback).

**Lifecycle & cost.** Creation is a leader-only Raft write + a Pebble checkpoint (cheap —
hard-linked SSTs), but a long-lived checkpoint **pins old SSTs from compaction** (disk grows),
and checkpoints are **NOT auto-cleaned**. So recon owns their lifecycle: create → use →
delete, or a bounded ring. Retention = how far back a past evaluation's exact snapshot can be
re-opened. The alert's `evidence` (numbers at break time) is durable regardless — the
checkpoint is only for re-drilling the full snapshot.

**Caveat:** watch upstream [ledger#1416](https://github.com/formancehq/ledger/issues/1416)
(`/aggregate/balances` PIT+metadata under `ACCOUNT_METADATA_HISTORY: DISABLED`); confirm the
v3 gRPC checkpoint path behaves before relying on it.

**Action:** rewrite ADR-002 around `checkpoint_id` (global consistent cut), not a timestamp.

## 5. Deliberately kept OUT of the ledger

- **Scheduler leader-election — not needed for correctness (see §5.1).** Duplicate firing
  across stateless replicas is made safe by guarded transitions + idempotent writes; a lease
  is only a later cost optimisation.
- **High-volume evaluation history** (see §4.4.2) — streamed to a ledger event sink
  (ClickHouse/Databricks for a queryable copy), not stored on the ledger accounts.
- **Its own message bus** — dropped entirely; the ledger event system delivers (§4.4.3).
- **Secrets / OAuth client credentials** — config, not ledger data.

### 5.1 Scheduler across replicas — a non-problem, resolved by idempotency

The scheduler is a **client-side action**, not ledger state: the ledger's log replication
makes rules/alerts *consistent* across nodes and across reconciliation replicas' reads, but it
does not decide *which replica fires* an evaluation. Run N stateless replicas and each will
independently see "rule R is due" and fire it.

Duplicate firing is nonetheless **safe**, for two reasons — so leader-election is **not**
needed for correctness:

1. **Guarded transitions** (state-in-account, §4.1.2): concurrent transitions race on a single
   marker; only one succeeds, the rest fail the guard. No double-resolve.
2. **Idempotent writes**: the evaluation batch carries an idempotency key derived
   deterministically from `(ruleId, canonical cron slot)`. The ledger applies the first and
   replays the cached outcome for the rest — exactly-once effect, including the otherwise
   unguarded *open* (no double marker).

**Chosen model: N stateless replicas, no leader-election, idempotent firing.** This is simpler
than a lease *and* strictly more available than a singleton (no SPOF — any live replica covers
the tick, none is missed). The only residual cost is duplicate read+compute (both replicas do
the work before the write dedups); negligible at POC scale. If it becomes material (many rules
/ large account sets), add a **k8s Lease** around the cron loop purely as a cost optimisation.

**Design requirement:** the idempotency key MUST be deterministic on `(ruleId, cron slot)` —
the scheduled slot, not `now()` — so two replicas on the same tick produce the same key.

## 6. Tradeoffs

**Wins**
- No Postgres, no bun, no migrations. Reconciliation becomes stateless → trivial to
  deploy and scale horizontally (matches ledger-connect's operational profile).
- Free audit trail, replication, and integrity — metadata & (future) events are
  audit-chain-covered and checker-verified by the ledger itself.
- Structural dedup (address = fingerprint) removes a whole class of concurrency bugs.
- Single operational surface (the ledger cluster) for both data and control state.

**Costs / risks**
- **Impedance mismatch.** JSON specs/evidence become opaque string metadata; rich SQL
  queries (JSONB predicates, joins) are gone — we get metadata-prefix + scalar filters
  only. Anything more must be computed client-side.
- **Events are generic, not semantic** — `SAVED_METADATA` carries the metadata diff, not
  an `alert.resolved` domain event; consumers interpret it (or we self-describe via a
  `last_transition` field) until the generic event-log lands (§4.4).
- **Sink filtering is by event type, not by ledger** — a metadata sink also carries
  data-ledger writes; consumers filter on `event.ledger`. Potential noise (§4.4).
- **PIT rework** — ADR-002 must move from timestamp to checkpoint semantics.
- **Network dependency** on the ledger cluster for every state read/write (no local
  fallback). Ledger downtime = reconciliation degraded.
- **Scheduler** duplicate firing wastes read+compute across replicas (correctness is safe via
  idempotency + guards, §5.1); optionally optimised later with a k8s Lease.

## 7. Migration plan (phased, reversible)

Introduce a `LedgerStore` behind the existing `Storage` interface (`internal/storage/store.go`)
so the swap is one implementation, not a rewrite of the service layer.

- **Phase 0 — Reads first (prereq, ~Option C).** Point the "pool" `Source` at a
  ledger-connect-fed ledger; move PIT to checkpoints (§4.5); verify #1416. No storage
  change yet.
- **Phase 1 — Control-ledger + dual-write.** Create `_recon` with schema; implement
  `LedgerStore`; dual-write (Postgres + ledger) behind the interface; assert parity in
  tests.
- **Phase 2 — Flip reads.** Serve rules/alerts from the ledger; Postgres becomes a
  shadow/fallback.
- **Phase 3 — Drop Postgres + own message bus.** Remove bun + migrations and the
  watermill/Kafka publisher; wire a ledger event sink (HTTP webhook by default) for
  delivery; history is the `_recon` `SAVED_METADATA` stream (§4.4).
- **Phase 4 — Semantic events / replay.** When the generic event-log primitive lands,
  move from generic `SAVED_METADATA` to semantic `reconciliation.*` events and a replay
  API; coordinate the envelope with ledger-connect.

## 8. Open questions

1. ~~Scheduler HA without CAS~~ **Resolved (§5.1):** N stateless replicas + idempotent
   firing; no leader-election needed. (k8s Lease later only as a cost optimisation.)
2. **Ledger event system — enough as-is?** `SAVED_METADATA` events + sinks cover delivery
   and history today (§4.4). Do we need the generic event-log's **semantic event types**,
   **per-ledger sink filtering**, and **replay API** before GA — and on what timeline?
   Joint design with the ledger team + ledger-connect (shared need).
3. **Metadata value size limits** — confirm the ledger's practical cap for string values
   (JSON specs/evidence) and the read-side index policy for long values
   (`advanced-read-queries.md:1111`). Truncate + offload large evidence?
4. **Multi-tenancy** — one `_recon` ledger per stack, or per tenant? Aligns with how
   ledger-connect names per-source ledgers.
5. **Legacy `/policies` facade** — keep serving it from the `LedgerStore` unchanged.

## 9. Alternatives considered

- **B — Embed Pebble (`cockroachdb/pebble`) as reconciliation's own local engine.**
  Removes Postgres but makes the service **stateful single-node** (PVC, backup,
  single-writer), forces us to hand-roll every secondary index and re-implement today's
  JSONB queries, and worsens scheduler HA. Reuse the ledger repo's *patterns* (zone/sub
  key layout, `WriteSession`/`ReadHandle` split, snapshot reads) but not its cluster.
  Rejected as default: heavier ops than staying stateless on the shared ledger.
- **C — Keep Postgres, only finish Ledger↔Ledger reads.** Least work, but does not meet
  the "no Postgres" goal. Retained as **Phase 0** (it is a prerequisite either way).
- **Naïve — write recon tables into the ledger's Pebble.** Rejected: violates ledger
  invariants #3/#6/#8/#9 and there is no library access anyway (§3).

## 10. Transposability of Ledger Clarity V1 (current code)

A review of the current implementation confirms the port is contained — most of the
codebase is storage-independent.

**Clean / keep as-is:**
- **Engine + templates** (`internal/engine/`, `internal/templates/`) are pure compute over
  the `LedgerResolver` / `PaymentsResolver` interfaces — zero storage coupling. Only the
  resolver *implementations* change (point at gRPC). The `Source` primitive already supports
  ledger↔ledger (`SourceKind == "ledger"` on both sides).
- **Models** — ~85% scalar fields → typed metadata; `spec` / `evidence` / `resolution` /
  `ack` are JSON blobs → string metadata. No impedance mismatch.
- **Service orchestration** — `EvaluateRule` wraps "one evaluation → N alert transitions" in
  `RunInTx`; that unit maps 1:1 to a single ledger `Apply` batch (batch-atomic). No service
  refactor.

**Contained adapter work (behind the existing `Store` interface, `internal/api/service/service.go`):**
- `List*` queries take a `go-libs/query.Builder` → add a translator to ledger metadata
  filters. Small; V3's read-side index supports substring/range/exact predicates natively.
- `RunInTx` (a type-assertion today) → implement as a ledger batch wrapper.

**Do NOT re-introduce Postgres** for the gaps a naive port would hit — V3 already covers them:
- *Filtering* — native via the typed-metadata read-side index + `ListAccounts` keyset
  pagination. "OPEN alerts for rule X" is an indexed metadata query, not a Postgres index.
- *History* — via the event system (§4.4), not an `alert_event` table; a ClickHouse/
  Databricks sink gives SQL-queryable history if pagination is needed.
- *Rules for the scheduler* — list from the control-ledger (`enabled == true`); only the
  scheduler **lease** needs an external CAS (§5, §8).

**Ledger-native modeling wins (prefer these over a 1:1 port — we are POC):**
1. Alert lifecycle → **state-in-account** (§4.1.1): free illegal-transition guard, semantic
   history, balance rollups, EPHEMERAL cleanup.
2. Evaluations → **not a durable table**; keep `last_evaluation` on the alert + stream the
   full evaluation to a sink (ClickHouse for queryable retention).
3. Labels → **flat indexed metadata keys** (`label.env = prod`), not a JSON blob → native
   filtering.
4. Evidence → **by reference** (evaluation id + PIT, recompute on read) or on the evaluation
   account, not duplicated on every alert.
5. Period retention → **EPHEMERAL / burn-on-close** per cadence → bounded hot set.

**Effort:** a working `LedgerStore` POC behind the current interface ≈ 2–3 weeks; the CEL
kernel is untouched. The one true external dependency remains scheduler HA (§5, §8).
