# Architecture

Where the pieces live and how they fit together. For the *why* behind the kernel choice, see [ADR-001](../prd/adr-001-cel-kernel.md); for PIT and cross-source consistency, [ADR-002](../prd/adr-002-pit-consistency.md).

---

## Package layout

```text
internal/
├── models/                 ✅ Go types — Rule, Evaluation, Incident, Resolution
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
│   └── sdk_resolvers.go    SDK-backed resolver impls (V2.GetLedger, V2.GetBalancesAggregated, V3.GetPoolBalancesLatest)
├── templates/              ✅ V1 GA template catalog
│   ├── template.go         Evaluator interface, Outcome, Registry
│   ├── helpers.go          CEL string rendering, fingerprint, sorted-keys
│   ├── ledger_vs_pool_drift.go
│   ├── ledger_invariant.go
│   └── account_threshold.go
└── api/
    ├── service/            ✅ Rule / Evaluation / Incident orchestration (rule.go, evaluation.go, incident.go)
    ├── backend/            ✅ Backend interface + generated mock (now covers all V1 methods)
    ├── rule.go             ✅ V1 rule HTTP handlers + Evaluate
    ├── evaluation.go       ✅ V1 evaluation HTTP handlers
    ├── incident.go         ✅ V1 incident HTTP handlers (ack/resolve/accept)
    └── router.go           ✅ Wires legacy /policies and V1 /rules /evaluations /incidents
```

---

## Layer responsibilities

```mermaid
flowchart TB
    subgraph "API layer ⏳"
        HTTP[HTTP handlers] --> Svc[Rule / Evaluation / Incident services]
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
| **Service** ✅ | Validation, state changes, incident dedup, resolution lifecycle | Methods on `Service` (rule.go / evaluation.go / incident.go) |
| **Templates** ✅ | Typed specs → CEL; per-fingerprint outcomes | `Evaluator`, `Outcome`, `Registry` |
| **Engine** ✅ | CEL evaluation, budget, PIT propagation, resolver dispatch | `Engine`, `Source`, `Resolvers`, `Limits` |
| **Resolvers** ✅ | SDK calls; feature-flag cache; data shaping | `SDKLedgerResolver`, `SDKPaymentsResolver` |
| **Storage** ✅ | bun CRUD; partial unique index for incident dedup; cascade deletes | `Storage`, models |

---

## The kernel — one-paragraph view

A `Source` is an opaque CEL value that names a backend dataset (`LedgerSet(ledger, query)`, `PaymentsPool(id)`, `LedgerPostings(...)` — last one V1.1). Builtins like `balance(Source)` and `balances(Source)` consume sources and call the matching resolver. Each evaluation builds a fresh CEL env whose bindings close over the current context (ctx, resolvers, budget, PIT). The validation env at construction time has *declarations only* — used for type-checking at rule-create time without exercising resolvers.

```mermaid
flowchart LR
    Tmpl[Template renders per-asset CEL] --> Compile[engine.Compile against validation env]
    Compile --> Evaluate[engine.Evaluate]
    Evaluate --> Bindings[Build per-eval env with bindings]
    Bindings --> Run[program.ContextEval]
    Run --> Resolvers[Resolver calls via builtins]
    Resolvers -.-> SDKLedger[V2.GetBalancesAggregated]
    Resolvers -.-> SDKPayments[V3.GetPoolBalancesLatest]
```

See [engine/engine.go](../../internal/engine/engine.go) for the Compile/Evaluate flow, [engine/builtins.go](../../internal/engine/builtins.go) for the CEL function set, and [engine/sdk_resolvers.go](../../internal/engine/sdk_resolvers.go) for the SDK wiring.

---

## Templates layer — one-paragraph view

A template owns its own end-to-end evaluation. It scouts the asset universe by calling resolvers directly (e.g. union of ledger + pool balances for `ledger_vs_pool_drift`), then for each asset it renders a fresh CEL string, compiles + evaluates via the kernel, and emits an `Outcome` with a stable fingerprint. The service layer collects outcomes and opens/updates one incident per failing fingerprint.

```mermaid
flowchart LR
    Spec[templateSpec] --> Scout[resolver.AggregateBalance / PoolBalanceLatest]
    Scout --> Universe[Union of assets]
    Universe --> ForEach[For each asset]
    ForEach --> CEL[Render asset-specific CEL]
    CEL --> Engine[engine.Compile + Evaluate]
    Engine --> Outcome[Outcome { fingerprint, passed, evidence }]
    ForEach --> Outcomes[List of Outcome]
```

Each template also performs a **kernel/template consistency check** — it runs the same per-asset comparison twice (direct big.Int math + via the kernel) and errors loudly on divergence. Catches kernel drift early.

---

## Storage shape

```mermaid
erDiagram
    RULE ||--o{ EVALUATION : "evaluated by"
    RULE ||--o{ INCIDENT   : "opens"
    EVALUATION ||--o{ INCIDENT : "first_/last_evaluation_id"
    INCIDENT ||--o| INCIDENT : "parent_incident_id (re-open)"
    POLICY ||--o{ RECONCILIATION : "legacy"

    RULE {
        uuid id PK
        text name
        text template_kind
        jsonb template_spec
        text compiled_cel
        bool enabled
        text severity
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
    INCIDENT {
        uuid id PK
        uuid rule_id FK
        text fingerprint
        text status
        text severity
        ts opened_at
        ts last_seen_at
        int occurrence_count
        uuid first_evaluation_id FK
        uuid last_evaluation_id FK
        jsonb evidence
        jsonb ack
        jsonb resolution
        uuid parent_incident_id FK
        jsonb labels
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

- **`incident_active_per_fingerprint`** — partial unique index on `(rule_id, fingerprint) WHERE status IN ('OPEN','ACKNOWLEDGED')`. Guarantees at most one active incident per fingerprint; concurrent failing evaluations always update the same row.
- **`ON DELETE CASCADE`** — deleting a `Rule` cleans up its evaluations and incidents.
- **`touch_updated_at` trigger** — `updated_at` advances on every UPDATE for `rule` and `incident`. Verified in [migrations.go](../../internal/storage/migrations/migrations.go) and exercised in storage tests.

---

## Concurrency model

- **Engine** is concurrency-safe; a single instance is the long-lived dep injected at startup.
- **Per-eval CEL env** is built fresh per `Engine.Evaluate` call — no shared state between concurrent evaluations.
- **Budget tracker** uses `atomic.Int64` for `accountsScanned`.
- **Resolvers** cache feature flags but are otherwise stateless per call.
- **Incident dedup** relies on the partial unique index, not application-level locking. Concurrent failing evaluations attempting to insert the same fingerprint will race, but only one succeeds — the loser does an UPDATE.

---

## Dependencies

| Library | Pinned at | Why |
|---|---|---|
| `github.com/google/cel-go` | v0.28.1 | Kernel evaluator. See [ADR-001](../prd/adr-001-cel-kernel.md) §6. |
| `github.com/formancehq/formance-sdk-go/v3` | v3.7.2 | Has `V3.GetPoolBalancesLatest` — required to avoid the legacy PIT empty path. |
| `github.com/uptrace/bun` | (inherited via go-libs) | ORM; existing project convention. |
| `github.com/formancehq/go-libs/migrations` | inherited | Migration framework. |

---

## What's not in this diagram (yet)

- **Scheduler** (cron loop, advisory-lock leasing) — ⏳ task #8 V1 GA.
- **Webhook event publisher** — ⏳ task #8.
- **Email digest** — ⏳ task #8.
- **fctl wiring** — ⏳ task #8.
- **EE gating + usage metering** — ⏳ task #8.

The kernel + templates + storage are designed so each of these slot in without touching what's already shipped.
