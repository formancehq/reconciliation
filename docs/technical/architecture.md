# Architecture

Where the pieces live and how they fit together. For the *why* behind the kernel choice, see [ADR-001](../prd/adr-001-cel-kernel.md); for PIT and cross-source consistency, [ADR-002](../prd/adr-002-pit-consistency.md).

---

## Package layout

```text
internal/
├── models/                 ✅ Go types — Rule, Evaluation, Alert, AlertEvent, Resolution
├── storage/                ✅ Postgres CRUD via bun
│   └── migrations/         ✅ Append-only Up migrations (v3.7.2 SDK)
├── engine/                 ✅ Internal CEL kernel
│   ├── types.go            Account, Balance, Posting
│   ├── source.go           Source opaque CEL type
│   ├── resolvers.go        LedgerResolver, PaymentsResolver interfaces
│   ├── builtins.go         CEL function declarations + per-eval bindings
│   ├── engine.go           Compile + Evaluate
│   ├── budget.go           Limits + budgetTracker
│   ├── errors.go           ErrCompile / ErrEvaluate translation
│   └── sdk_resolvers.go    SDK-backed resolver impls (V2.GetBalancesAggregated, V2.ListAccounts, V3.GetPoolBalances{,Latest})
├── templates/              ✅ V1 GA template catalog
│   ├── template.go         Evaluator interface, Outcome, Registry
│   ├── helpers.go          CEL string rendering, fingerprint, sorted-keys, zeroIfNil
│   ├── source.go           Shared Source primitive (ledger / payments_pool): resolve + celTerm
│   ├── ledger_vs_pool_drift.go
│   ├── ledger_invariant.go
│   ├── account_threshold.go
│   └── source_parity.go
└── api/
    ├── service/            ✅ Rule / Evaluation / Alert orchestration (rule.go, evaluation.go, alert.go)
    ├── backend/            ✅ Backend interface + generated mock (now covers all V1 methods)
    ├── rule.go             ✅ V1 rule HTTP handlers + Evaluate
    ├── evaluation.go       ✅ V1 evaluation HTTP handlers
    ├── alert.go            ✅ V1 alert HTTP handlers (ack/resolve/accept + events timeline)
    └── router.go           ✅ Wires legacy /policies and V1 /rules /evaluations /alerts
```

---

## Layer responsibilities

```mermaid
flowchart TB
    subgraph "API layer ✅"
        HTTP[HTTP handlers] --> Svc[Rule / Evaluation / Alert services]
    end
    Svc --> Reg[templates.Registry ✅]
    Svc --> Store[storage.Storage ✅]
    Reg --> Eng[engine.Engine ✅]
    Eng --> Res[SDK Resolvers ✅]
    Res --> Ledger[Formance Ledger]
    Res --> Payments[Formance Payments]
    Store --> PG[(Postgres)]
```

| Layer | Responsibility | Key types |
|---|---|---|
| **HTTP** ✅ | OpenAPI-typed surface; auth scopes; cursor pagination | Handlers in [`internal/api/`](../../internal/api/); routes in [`router.go`](../../internal/api/router.go) |
| **Service** ✅ | Validation, state changes, alert dedup, resolution lifecycle, event log append | Methods on `Service` (rule.go / evaluation.go / alert.go) |
| **Templates** ✅ | Typed specs → CEL; per-fingerprint outcomes | `Evaluator`, `Outcome`, `Registry` |
| **Engine** ✅ | CEL evaluation, budget, PIT propagation, resolver dispatch | `Engine`, `Source`, `Resolvers`, `Limits` |
| **Resolvers** ✅ | SDK calls; data shaping | `SDKLedgerResolver`, `SDKPaymentsResolver` |
| **Storage** ✅ | bun CRUD; unique constraint per (rule, fingerprint, period); append-only `alert_event` log; cascade deletes | `Storage`, models |

---

## The kernel — one-paragraph view

A `Source` is an opaque CEL value that names a backend dataset (`ledgerSet(ledger, query)`, `pool(id)`, `LedgerPostings(...)` — last one V1.1). Builtins like `balance(Source)` and `balances(Source)` consume sources and call the matching resolver. Each evaluation builds a fresh CEL env whose bindings close over the current context (ctx, resolvers, budget, PIT). The validation env at construction time has *declarations only* — used for type-checking at rule-create time without exercising resolvers.

```mermaid
flowchart LR
    Tmpl[Template renders per-asset CEL] --> Compile[engine.Compile against validation env]
    Compile --> Evaluate[engine.Evaluate]
    Evaluate --> Bindings[Build per-eval env with bindings]
    Bindings --> Run[program.ContextEval]
    Run --> Resolvers[Resolver calls via builtins]
    Resolvers -.-> SDKLedger[V2.GetBalancesAggregated]
    Resolvers -.-> SDKPayments[V3.GetPoolBalances / …Latest]
```

See [engine/engine.go](../../internal/engine/engine.go) for the Compile/Evaluate flow, [engine/builtins.go](../../internal/engine/builtins.go) for the CEL function set, and [engine/sdk_resolvers.go](../../internal/engine/sdk_resolvers.go) for the SDK wiring.

---

## Templates layer — one-paragraph view

A template owns its own end-to-end evaluation. It scouts the asset universe by calling resolvers directly (e.g. union of ledger + pool balances for `ledger_vs_pool_drift`), then evaluates CEL over those immutable snapshot values and renders the source-shaped per-asset CEL into `evidence.compiledCEL` for explainability. The service layer collects the resulting stable-fingerprint outcomes and opens or updates one alert per failure (appending one `alert_event` row per outcome).

```mermaid
flowchart LR
    Spec[templateSpec] --> Scout[resolver.AggregateBalance / PoolBalance]
    Scout --> Universe[Union of assets]
    Universe --> ForEach[For each asset]
    ForEach --> SnapshotCEL[CEL over scouted snapshot values]
    ForEach --> CEL[Render asset CEL → evidence.compiledCEL]
    SnapshotCEL --> Outcome[Outcome { fingerprint, passed, evidence }]
    CEL --> Outcome
    ForEach --> Outcomes[List of Outcome]
```

The template executes an equivalent CEL expression over the values it just scouted, so CEL is authoritative without re-reading Ledger or Payments. All fingerprint expressions share one wall-clock deadline and runtime-cost budget. The source-shaped CEL remains in evidence for explainability, and a kernel-parity test verifies both renderings stay equivalent.

---

## Storage shape

```mermaid
erDiagram
    RULE       ||--o{ EVALUATION  : "evaluated by"
    RULE       ||--o{ ALERT       : "raises"
    EVALUATION ||--o{ ALERT       : "last_evaluation_id"
    ALERT      ||--o{ ALERT_EVENT : "transitions / history"
    EVALUATION ||--o{ ALERT_EVENT : "evaluation_id (per-eval row)"
    POLICY     ||--o{ RECONCILIATION : "legacy"

    RULE {
        uuid id PK
        text name
        text template_kind
        jsonb template_spec
        text explanation_cel
        bool enabled
        text severity
        text cadence "continuous|daily|weekly|monthly — Go/API: periodType"
        jsonb schedule
        jsonb notifications
        jsonb labels
        ts created_at
        ts updated_at
    }
    EVALUATION {
        uuid id PK
        uuid rule_id FK
        ts started_at
        ts ended_at
        jsonb pit_per_source
        text result
        jsonb evidence
        text error
        bigint cost_units
    }
    ALERT {
        uuid id PK
        uuid rule_id FK
        text fingerprint
        text period_id "continuous|2026-03|2026-W12|…"
        text status
        text severity
        ts first_seen_at
        ts last_seen_at
        bigint occurrence_count "lifetime within period"
        uuid last_evaluation_id FK
        jsonb evidence "current"
        jsonb ack "current"
        jsonb resolution "current"
        jsonb snooze "current"
        jsonb labels
    }
    ALERT_EVENT {
        uuid id PK
        uuid alert_id FK
        uuid evaluation_id FK "nullable"
        text type "fail|pass|ack|resolve|accept|snooze|unsnooze"
        text prev_status "nullable"
        text new_status
        jsonb payload
        bool notify "false = suppressed repeat"
        ts at
        ts created_at
    }
    POLICY {
        uuid id PK
        text name
        text ledger_name
        jsonb ledger_query
        uuid payments_pool_id
    }
    RECONCILIATION {
        uuid id PK
        uuid policy_id FK
        ts reconciled_at_ledger
        ts reconciled_at_payments
        text status
        jsonb ledger_balances
        jsonb payments_balances
        jsonb drift_balances
        text error
    }
```

### Key invariants

- **`alert_unique_scope`** — UNIQUE constraint on `(rule_id, fingerprint, period_id)` in the `alert` table. Exactly one alert row per (rule, fingerprint, period); the same fingerprint failing in a new period is a fresh case, not a reopen. `period_id` is `'continuous'` for live-monitoring rules, which reproduces the original per-pair dedup. Concurrent failing evaluations either update or (on a true first-open race) retry as update.
- **`alert.occurrence_count`** is the count of FAIL events on the alert within its period — it survives reopen cycles (for a `continuous` rule, the lifetime count). Per-episode counts are derived by filtering `alert_event` between status transitions.
- **`alert_event` is append-only by convention** — no code path issues UPDATE or DELETE against it. A future migration can layer a per-alert `seq` + `prev_hash`/`hash` chain on top without breaking readers (mirrors the Ledger transaction log shape).
- **`ON DELETE CASCADE`** — deleting a `Rule` cleans up its evaluations, alerts, and alert events.
- **`touch_updated_at` trigger** — `updated_at` advances on every UPDATE for `rule` and `alert`. Verified in [migrations.go](../../internal/storage/migrations/migrations.go) and exercised in storage tests.

---

## Concurrency model

- **Engine** is concurrency-safe; a single instance is the long-lived dep injected at startup.
- **Per-eval CEL env** is built fresh per `Engine.Evaluate` call — no shared state between concurrent evaluations.
- **Budget tracker** uses `atomic.Int64` for `accountsScanned`.
- **Resolvers** are stateless SDK adapters — one call per read, no cached state.
- **Alert dedup** relies on the UNIQUE constraint on `(rule_id, fingerprint, period_id)`. Same-rule evaluations also guard their complete read-and-commit window with `storage.TryWithRuleLock`: a concurrent API request receives HTTP 409 and a worker requeues without consuming an attempt. Different rules remain independent. See [storage/alert.go](../../internal/storage/alert.go), [storage/rule_lock.go](../../internal/storage/rule_lock.go), and the concurrency regression tests.
- **Alert event appends** happen in the same transaction as the alert UPDATE/INSERT — the log can never reflect a state the alert table doesn't.

---

## Dependencies

| Library | Pinned at | Why |
|---|---|---|
| `github.com/google/cel-go` | v0.28.1 | Kernel evaluator. See [ADR-001](../prd/adr-001-cel-kernel.md) §6. |
| `github.com/formancehq/formance-sdk-go/v3` | v3.7.2 | Pool reads: `V3.GetPoolBalances` (point-in-time, `?at=`) + `V3.GetPoolBalancesLatest` (current snapshot). |
| `github.com/uptrace/bun` | (inherited via go-libs) | ORM; existing project convention. |
| `github.com/formancehq/go-libs/migrations` | inherited | Migration framework. |

---

## What's not in this diagram (yet)

- **Scheduler worker** — PostgreSQL-backed planner, durable jobs, leases, fencing tokens, bounded catch-up, and multi-replica execution ([scheduler.md](./scheduler.md)).
- **Webhook event publisher** — alert transitions publish after commit through the `go-libs` PostgreSQL circuit breaker. This is intentionally not a transactional outbox; the crash gap and duplicate replay limits are documented in [scheduler.md](./scheduler.md).
- **Email digest** — ⏳ (owned in-module, ships separately at V1 GA).
- **fctl wiring** — ⏳.
- **EE gating + usage metering** — ⏳.

The kernel + templates + storage are designed so each of these slot in without touching what's already shipped.
