# Ledger v3 storage model

This document is the consolidated map of how Reconciliation uses Ledger v3. It describes the
current implementation, not an aspirational event-sourcing model.

Reconciliation is stateless and Postgres-free. It uses:

- one **control ledger** for its own rules, alerts, captures, and activity history; the default
  ledger name is `reconciliation` and can be changed with `--ledger-control-name`;
- one or more **data ledgers**, which are read live and never mutated by Reconciliation.

The `_recon` name used in older design documents is a conceptual shorthand for the control ledger,
not its configured default name.

```mermaid
flowchart LR
    API["HTTP API and service"] --> Store["LedgerStore"]
    Store --> Control["control ledger (default: reconciliation)"]
    API --> Templates["versioned templates"]
    Templates --> Resolver["live Ledger resolver"]
    Resolver --> Data["data ledgers A, B, C, ..."]
```

## Executable sources of truth

The persisted model is defined by code. When this document and code disagree, these files are
authoritative:

| Concern | Definition |
|---|---|
| Ledger name, assets, and account address builders | [`internal/ledgerschema/addresses.go`](../../internal/ledgerschema/addresses.go) |
| Account types, typed metadata, indexes, and prepared queries | [`internal/ledgerschema/schema.go`](../../internal/ledgerschema/schema.go) |
| Stored Numscript programs and their pinned version | [`internal/ledgerschema/scripts.go`](../../internal/ledgerschema/scripts.go) |
| Query-filter construction and JSON-query translation | [`internal/ledgerschema/filters.go`](../../internal/ledgerschema/filters.go), [`internal/ledgerschema/query.go`](../../internal/ledgerschema/query.go), and [`internal/ledgerstore/filter.go`](../../internal/ledgerstore/filter.go) |
| Idempotent startup provisioning | [`internal/ledger/provisioner.go`](../../internal/ledger/provisioner.go) |
| Runtime wiring and enforcement mode | [`cmd/ledger.go`](../../cmd/ledger.go) |
| Persisted/read model mappings | [`internal/ledgerstore/`](../../internal/ledgerstore/) |

## Control-ledger chart

The chart contains eight account types. Pool accounts are declared overdraft sources used to mint
precision-zero markers and counters; they do not represent customer money.

| Address pattern | Persistence | Purpose |
|---|---|---|
| `rule:{ruleId}` | NORMAL | Current rule configuration as typed account metadata. |
| `alert:item:rule:{ruleId}:per:{period}:fp:{fpHash}` | NORMAL | Canonical alert record, occurrence count, and current-state metadata. |
| `alert:st:{open|ack}:rule:{ruleId}:per:{period}:fp:{fpHash}` | EPHEMERAL | The single `ALERT` lifecycle marker. It is drained and purged when the alert closes. |
| `alert:pool:rule:{ruleId}` | NORMAL | Per-rule overdraft source for `ALERT` and `OCC`. |
| `capture:rule:{ruleId}:per:{period}` | NORMAL | Address touched by every immutable evaluation-capture transaction. |
| `capture:pool:rule:{ruleId}` | NORMAL | Per-rule overdraft source for `CAPTURE`. |
| `activity:rule:{ruleId}` | NORMAL | Ordered transaction stream for the combined rule timeline. |
| `activity:pool:rule:{ruleId}` | NORMAL | Per-rule overdraft source for `ACTIVITY`. |

The four assets all have precision zero:

| Asset | Meaning |
|---|---|
| `ALERT` | Exactly one marker for each active alert. |
| `OCC` | One unit for each alert occurrence. |
| `CAPTURE` | One unit for each recorded evaluation. |
| `ACTIVITY` | One unit for each rule lifecycle, evaluation, or alert activity item. |

## Current state and immutable history

Rule and alert accounts are current-state projections. Typed account metadata supports direct reads
and filtering: rule fields include the template specification, contract version, revision, periodType,
schedule, and labels; alert fields include status, evidence, acknowledgement, resolution, snooze,
and the latest transition envelope.

Immutable history lives on transactions:

- a capture transaction stores the evaluation envelope, including verdict, trigger, evidence,
  contract version, rule revision, timings, result, and error;
- an activity transaction stores a versioned semantic envelope containing kind, rule, occurrence
  time, contract version, revision, correlation identifier, and payload;
- alert and capture programs post `ACTIVITY` in the same transaction as their state mutation;
- rule create, update, delete, snooze, and unsnooze use the generic activity program and apply
  account metadata atomically in that transaction.

The combined timeline therefore records rule lifecycle, evaluation/capture activity, alert
lifecycle, and manual alert interactions in one per-rule stream. It is complete from the activity
journal rollout onward. The API does not fabricate pre-rollout activity from current state.

Capture evidence retains every outcome, including successful outcomes that do not mutate an alert.
Each timeline evaluation item therefore points to a reproducible observation rather than only
proving that the run occurred.

## Numscript library and atomicity

The stored library is pinned at `2.0.0` and contains six programs:

| Program | Atomic effect |
|---|---|
| `alert_open` | Mint the active marker and first occurrence, then append activity. |
| `alert_bump` | Increment occurrences, then append activity. |
| `alert_reopen` | Guarded marker move to open, increment occurrences, then append activity. |
| `alert_move` | Guarded lifecycle marker move, then append activity. |
| `capture` | Append a capture counter and activity item. |
| `activity` | Append an activity item for rule changes or status-neutral alert interactions. |

Lifecycle moves use a bare source account as a compare-and-swap guard. Account metadata and
transaction metadata are supplied in the same Ledger transaction, so current state and history
commit together. Deterministic idempotency keys make a retransmitted write safe; callers must not
reuse a key for different content.

Snooze and unsnooze remain status-neutral and do not move the `ALERT` marker. They are no longer
metadata-only writes: the activity transaction applies the snooze metadata change and records the
manual interaction atomically.

## Queries and indexes

Typed metadata declarations and query indexes are separate concerns. Every account field used by
the list-filter translator has an explicit account-metadata index. Dynamic `label.*` keys are not
indexed.

The transaction address index is required by both capture history and the rule timeline. Without
it, Ledger rejects address-filtered transaction queries with `FailedPrecondition` and an
`index not found: address` detail. Index creation is asynchronous; immediately after provisioning,
reads can temporarily return `Unavailable` with `index is still building` even though the index is
already declared.

Only two fixed hot paths are prepared queries:

- `alerts-open-count` aggregates open alert markers;
- `rules-enabled` lists rules used by the scheduler.

Rule and alert filters, capture history, and activity history use structured `QueryFilter` values
built at runtime. Multi-source data reads use the data-ledger aggregation/account APIs and are
separate from these control-ledger queries.

## Provisioning and schema evolution

The provisioner runs at startup. It creates the control ledger when absent, then reconciles missing
account types and typed metadata fields, creates indexes and prepared queries, and saves every
Numscript version. Re-running it is idempotent.

Additive changes are applied to an existing ledger. Destructive changes—removing or retyping a
field, or changing an account type that already contains accounts—are not silently migrated and
need an explicit migration plan. The server currently provisions with chart enforcement `AUDIT`;
the code does not yet enable `STRICT` enforcement.

Contract evolution is independent from the chart. A missing persisted `contract_version` is read
as V1. V1 keeps `source_parity.left`/`.right` and the legacy evidence keys; V2 stores named
multi-source specifications and is isolated by route and persisted contract version.

## Verification

Storage changes should exercise:

- schema, query-filter, Numscript, provisioner, and LedgerStore unit tests with `just tests`;
- the serial `it`-tagged suite against a live Ledger v3 on `localhost:8888` with
  `just tests-integration`;
- a live create, evaluate, alert action, timeline read, and delete flow;
- restart provisioning against the same control ledger to verify additive idempotency and index
  readiness behavior.
