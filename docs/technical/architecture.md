# Architecture

Where the pieces live and how they fit together, **after the ledger-native migration**.
Reconciliation is now **Postgres-free and stateless**: all its state lives on a Ledger v3
control-ledger (`_recon`), it reads the ledgers it reconciles **live**, and it records each
evaluation as an immutable `_recon` **capture** transaction. For the *why* behind the kernel, see
[ADR-001](../prd/adr-001-cel-kernel.md); for the read/consistency model,
[ADR-003](../prd/adr-003-checkpoint-anchor-and-crosscheck.md) (which supersedes the checkpoint model
of [ADR-002](../prd/adr-002-pit-consistency.md)); for the storage/event design, the
[ledger-native storage RFC](../drafts/rfc-ledger-native-storage.md).

---

## Package layout

```text
internal/
├── models/          Go types — Rule, Evaluation, Alert, AlertEvent, Resolution
├── store/           Storage-agnostic contract types + sentinels (leaf; no ORM) — the
│                    Store interface shape shared by the service and ledgerstore
├── ledgerpb/        Generated Ledger v3 gRPC protos (synced from ledger-connect)
├── ledger/          Ledger v3 gRPC client: transactions, metadata, account/log queries,
│                    events sinks; live Reader; provisioner
├── ledgerauth/      Ed25519 request signing + TLS; refuses insecure transport by default (F2)
├── ledgerschema/    Chart of accounts, metadata schema/indexes, address builders, numscript
│                    library, and the query translator (recon query → ledger filter)
├── ledgerstore/     LedgerStore — the sole service.Store, backed by the control-ledger `_recon`
├── ledgerresolver/  Adapter: ledger.Reader (live) → engine.LedgerResolver
├── engine/          Internal CEL kernel
│   └── types.go / source.go / resolvers.go / builtins.go / engine.go / budget.go / errors.go
├── templates/       V1 GA template catalog (source.go, ledger_invariant, account_threshold, source_parity)
└── api/
    ├── service/     Rule / Evaluation / Alert orchestration; live reads + a capture per evaluation
    ├── backend/     Backend interface + generated mock
    ├── rule.go / alert.go   HTTP handlers
    └── router.go
```

There is **no `storage/` package and no `internal/events`** — both were deleted with Postgres.
Shared contract types moved to the leaf `internal/store`.

---

## Layer responsibilities

```mermaid
flowchart TB
    subgraph API
        HTTP[HTTP handlers] --> Svc[Rule / Evaluation / Alert service]
    end
    Svc --> Reg[templates.Registry]
    Svc --> Store[LedgerStore]
    Reg --> Eng[engine.Engine]
    Eng --> LR["Ledger resolver<br/>live Reader adapter"]
    Store --> Recon[("control-ledger _recon (gRPC)<br/>rules, alerts, captures")]
    LR --> Data[("data-ledgers A/B<br/>(live)")]
```

| Layer | Responsibility | Key types |
|---|---|---|
| **HTTP** | OpenAPI-typed surface; auth scopes; cursor pagination | handlers in [`internal/api/`](../../internal/api/), routes in [`router.go`](../../internal/api/router.go) |
| **Service** | Validation, evaluation orchestration, **capture recording**, alert dedup + lifecycle | `Service` (rule.go / evaluation.go / alert.go) |
| **Templates** | Typed specs → direct `big.Int` math; per-fingerprint outcomes; rendered CEL for explainability | `Evaluator`, `Outcome`, `Registry` |
| **Engine** | CEL type-check at rule-create + budget/resolver dispatch (live reads) | `Engine`, `Source`, `Resolvers`, `Limits` |
| **Resolvers** | Ledger reads **live** (strictly ledger↔ledger) | `ledgerresolver.Resolver` (over `ledger.Reader`) |
| **Storage** | Rules + alert lifecycle + immutable evaluation **captures** as Numscript batches + typed metadata on `_recon` | `LedgerStore`, `ledger.Client`, `ledgerschema` |

---

## The kernel — CEL for validation + explainability

A `Source` is an opaque CEL value naming a backend dataset (`ledgerSet(ledger, query)`)
over builtins (`balance`, `balances`, `sum`, `abs`). At **rule-create** time the engine
type-checks the rule's CEL against a *declarations-only* env — no resolver is exercised. Built-in
**templates evaluate in typed Go** over a single live read per source (ADR-003); they render the
equivalent CEL into `evidence.compiledCEL` for explainability but do **not** run it (the golden test
`TestCrossCheck_*` guards that the two agree). `engine.Evaluate` (the CEL runtime) is reserved for
the post-GA raw-CEL power mode.

Resolvers (ADR-003): reconciliation is strictly **ledger↔ledger**.
- **Ledger sources** read **live** — a single `AggregateVolumes` is an internally consistent
  snapshot; cross-ledger skew is absorbed by the template's `tolerance`. Backed by
  `ledgerresolver.Resolver` over `ledger.Reader`.

```mermaid
flowchart LR
    Tmpl["Template: direct big.Int math"] --> Reads["resolve sources (live)"]
    Reads --> LR["ledger source → AggregateVolumes (live)"]
    Tmpl --> Evidence["render compiledCEL into evidence"]
```

See [engine/engine.go](../../internal/engine/engine.go), [engine/builtins.go](../../internal/engine/builtins.go),
and the adapter [ledgerresolver/resolver.go](../../internal/ledgerresolver/resolver.go).

---

## The control-ledger data model

Reconciliation stores everything on `_recon` — there is no relational schema. The chart has
**6 account types** plus three precision-0 assets — `ALERT` (the lifecycle marker), `OCC`
(occurrence counter), and `CAPTURE` (per-evaluation counter). The data-ledgers being reconciled
(A, B) are *external* and read-only to recon; they are not part of this chart.

| Account | Type | Role |
|---|---|---|
| `rule:{id}` | NORMAL | the rule — typed metadata (spec, cadence, compiled CEL, notifications, labels) |
| `alert:item:rule:{id}:per:{p}:fp:{h}` | NORMAL | **canonical alert record** — metadata (`status` mirror, severity, evidence, resolution, ack, snooze, **`last_transition`**, labels) + `OCC` balance = occurrence count |
| `alert:st:{state}:rule:{id}:per:{p}:fp:{h}` | **EPHEMERAL** | the `ALERT` **marker** — source-of-truth for status; `state ∈ {open, ack}` |
| `alert:pool:rule:{id}` | NORMAL | mint source for `ALERT`+`OCC` (overdraft) and free gauges |
| `capture:rule:{id}:per:{p}` | NORMAL | **evaluation capture bucket** (ADR-003) — one immutable capture tx per evaluation (snapshot in tx metadata); `−balance(·, CAPTURE)` = count |
| `capture:pool:rule:{id}` | NORMAL | mint source for `CAPTURE` (overdraft) |

The **marker is the status source-of-truth**; the `status` metadata key is an LWW mirror for O(1)
point-reads. Both are written in **one atomic Numscript batch**, so they never diverge.

### Transition workflow

Five numscripts (library, pinned `v1.0.0`): `alert_open` (mint marker + OCC), `alert_bump`
(OCC only), `alert_move` (guarded marker move, no OCC), `alert_reopen` (guarded move + OCC),
and `capture` (mint 1 `CAPTURE` → capture bucket).

```mermaid
flowchart TB
    Pool["alert:pool:rule:{id}<br/>(mint ALERT + OCC, overdraft)"]
    Pool -- "open: mint 1 ALERT + 1 OCC (alert_open)" --> StOpen["alert:st:open<br/>(marker)"]
    StOpen -- "repeat: +1 OCC (alert_bump)" --> StOpen
    StOpen -- "ack: guarded move (alert_move)" --> StAck["alert:st:ack<br/>(marker)"]
    StAck -- "resolve / accept / auto-resolve: BURN marker → pool (alert_move)" --> Closed["(no marker)<br/>EPHEMERAL purges st:{state}"]
    StOpen -- "resolve / accept / auto-resolve: BURN → pool" --> Closed
    Closed -- "reopen (was RESOLVED): re-mint (alert_open) +OCC" --> StOpen
    StAck -- "resurface (new fail): guarded move (alert_reopen) +OCC" --> StOpen
```

- **Every transition is one guarded, idempotent transaction.** The numscript's *bare source*
  (the marker must hold the funds) acts as a **compare-and-swap**: an illegal transition fails
  the whole batch. The item's status mirror + descriptive metadata + `last_transition` envelope
  are set in the same batch.
- **Burn-on-close.** Resolving drains the marker back to the pool → `alert:st:{state}` hits zero →
  **EPHEMERAL purges it** → a closed alert has *no* marker (no accumulation). Reopen re-mints.
- **Free gauges.** `−balance(pool, ALERT)` = active (open+ack) alerts; `−balance(pool, OCC)` =
  total occurrences.
- **Snooze / unsnooze** are metadata-only (no marker move), status-neutral.
- **Idempotency.** Each batch carries a deterministic key over `(action, rule, fingerprint,
  period, evaluationID)` — a gRPC retransmit is deduplicated by the ledger (see **F17** in the
  migration log for the content-sensitive caveat).

Addresses, assets, metadata keys, and the numscript library live in
[`internal/ledgerschema`](../../internal/ledgerschema/); the lifecycle writes in
[`internal/ledgerstore`](../../internal/ledgerstore/).

---

## Reads — live + capture

`EvaluateRule` reads its data ledgers **live** (ADR-003): each `ledgerSet` source is one
`AggregateVolumes` — an internally consistent server-side snapshot — so a single-ledger universe is
skew-free. Cross-ledger rules read each side separately (per-source); the transient skew is absorbed
by the template's `tolerance` (a period close reconciles settled state; continuous monitoring
self-corrects on the next tick). A certifiable atomic multi-ledger read is a future ledger primitive
([EN-1480](https://formance-team.atlassian.net/browse/EN-1480)); `min_log_sequence` (a live-read
freshness floor) is available but unused.

Every evaluation is recorded as an **immutable capture transaction** on `_recon` (ADR-003): a
self-describing snapshot (`verdict`, `trigger`, `evidence`, …) on a `COMMITTED_TRANSACTION`, plus a
`CAPTURE` counter unit in the `capture:rule:{id}:per:{p}` bucket. This is the durable "what reconciled
and when" — positive assurance on a pass, break evidence on a fail — receipt-signed and append-only,
replacing a queryable evaluation table (RFC §4.4.2). The run result is also returned from
`EvaluateRule`.

---

## Event delivery

Reconciliation runs **no message bus**. On every transition the store stamps a self-describing
`last_transition` envelope (`reconciliation.alert.<type>`, subject, prev/new status,
correlationID, payload) into `alert:item` metadata, so each Ledger log entry for that write is
self-describing — `COMMITTED_TRANSACTION` for lifecycle moves, `SAVED_METADATA`/`DELETED_METADATA`
for snooze/unsnooze.

Delivery is the ledger's native **events sink**: when `--events-sink-url` is configured, recon
provisions an HTTP webhook sink at boot for `[COMMITTED_TRANSACTION, SAVED_METADATA,
DELETED_METADATA]`. The ledger delivers matching events to the webhook (e.g. the Webhooks
module); consumers filter on `event.ledger == _recon` (sink filtering is by event type, not
ledger — RFC §4.4). See [ledger/events_sink.go](../../internal/ledger/events_sink.go).

---

## Concurrency & consistency

- **Engine** is concurrency-safe; a single instance is injected at startup. Each `Evaluate`
  builds a fresh per-eval CEL env — no shared state. Budget uses `atomic.Int64`.
- **Alert dedup / transitions** rely on **ledger CAS** (the marker bare-source guard) +
  deterministic idempotency keys — *not* an application lock or a DB unique constraint. A losing
  concurrent transition fails its guard (see **F22**: a raw `FailedPrecondition` where Postgres
  yielded a clean no-op).
- **Marker ↔ status mirror** are written in one atomic batch, so the log never reflects a state
  the item doesn't.
- **Statelessness** — recon holds no local state; multiple replicas share `_recon`. Reads are
  live and each write (alert transition, capture) is idempotent per (rule, period, evaluation), so
  concurrent replicas converge without coordination.

---

## Dependencies

| Library | Why |
|---|---|
| `github.com/google/cel-go` | Kernel evaluator ([ADR-001](../prd/adr-001-cel-kernel.md) §6). |
| `google.golang.org/grpc` + `internal/ledgerpb` | Ledger v3 gRPC transport (control-ledger + data-ledger reads). |
| `github.com/go-jose/go-jose/v4` | Ed25519 request signing for the secure ledger transport (F2). |

`bun` and the migrations framework are **gone** with Postgres. The Formance SDK
(`formance-sdk-go`) is **gone** with the Tier-2 payments-pool resolver — reconciliation is
strictly ledger↔ledger and reads the data ledgers directly over gRPC.

---

## What's not here yet

- **`ListAlertEvents` (paginated history)** — returns empty; a queryable history needs a
  downstream sink (ClickHouse/Databricks), since the ledger log has no per-account filter (RFC §10).
- **Semantic event types + replay API** — the future generic event-log; today's events are
  generic log-derived events carrying the `last_transition` envelope.
- **Certifiable atomic multi-ledger read** for exact replay / provable simultaneity — a future
  ledger primitive ([EN-1480](https://formance-team.atlassian.net/browse/EN-1480)); today's capture
  records the observed numbers (durable evidence), not a re-queryable cut.
- **Scheduler multi-replica leasing** — the in-process cron loop is single-instance for now
  ([scheduler.md](./scheduler.md)).
