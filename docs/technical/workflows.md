# Lifecycle Workflows

The V1 product surface is a **business lifecycle**, not a rules engine. This document is the visual reference for that lifecycle:

> **Observe → Detect → Alert → Evidence → Resolve or Accept**

Each diagram below shows one flow. The status legend in [README.md](./README.md) marks what's implemented today vs planned.

> **Canonical reference test.** Every flow on this page is exercised end-to-end
> in [`v1_orchestration_test.go`](../../internal/api/service/v1_orchestration_test.go).
> If the diagrams and the test ever diverge, the test is the source of truth.

---

## 1. Rule creation

```mermaid
sequenceDiagram
    autonumber
    participant U  as Operator (API / fctl)
    participant Svc as Service.CreateRule ✅
    participant Reg as templates.Registry ✅
    participant Eng as engine.Engine ✅
    participant DB  as Postgres

    U->>Svc: POST /rules { templateKind, templateSpec, … }
    Svc->>Reg: Get(templateKind)
    Reg-->>Svc: Evaluator
    Svc->>Reg: evaluator.Validate(spec)
    alt invalid spec
        Reg-->>Svc: ErrInvalidSpec
        Svc-->>U: 400 VALIDATION
    end
    Svc->>Reg: evaluator.Explain(spec) → representative CEL
    Reg-->>Svc: compiledCEL
    Svc->>Eng: Compile(compiledCEL)  (sanity-check it parses)
    Eng-->>Svc: ok
    Svc->>DB: INSERT INTO reconciliations.rule
    DB-->>Svc: row
    Svc-->>U: 201 Created { rule }
```

**Notes**

- `Explain()` returns one representative CEL string for the rule's `compiled_cel` column. Real evaluation re-renders the per-asset CEL at run time — `compiled_cel` is for explainability and the future `rules explain` endpoint.
- For metadata-filtered ledger templates, the service additionally calls `SDKLedgerResolver.Features(ledger)` and refuses if `ACCOUNT_METADATA_HISTORY: DISABLED` — see [ledger#1416](https://github.com/formancehq/ledger/issues/1416) and [v1-vs-legacy.md §6](./v1-vs-legacy.md#6-ledger-side-feature-flag-gotcha).

---

## 2. Rule evaluation

```mermaid
sequenceDiagram
    autonumber
    participant Trig as Trigger (cron ✅ / POST evaluate ✅)
    participant Svc  as Service.EvaluateRule ✅
    participant Reg  as templates.Registry ✅
    participant Eng  as engine.Engine ✅
    participant Res  as SDK Resolvers ✅
    participant Alr  as Storage.OpenOrUpdate/AutoResolveAlert ✅
    participant Log  as Storage.AlertEvent (append-only) ✅
    participant DB   as Postgres

    Trig->>Svc: evaluate(rule)
    Svc->>Reg: Get(rule.templateKind).Evaluate(spec, eng, resolvers, in)
    Reg->>Res: AggregateBalance / PoolBalanceLatest  (per source)
    Res-->>Reg: balances (per asset)
    Reg->>Eng: Compile(per-asset CEL)
    Reg->>Eng: Evaluate(compiled, in)
    Eng->>Res: balance(source, asset) via builtin
    Res-->>Eng: int64
    Eng-->>Reg: pass/fail + pitPerSource
    Reg-->>Svc: []Outcome  (one per fingerprint axis)
    Svc->>DB: INSERT evaluation (PASS/FAIL/ERROR + pit_per_source + evidence)
    DB-->>Svc: evaluationId
    loop For each failing outcome
        Svc->>Alr: OpenOrUpdateAlert(rule, outcome, evaluationId)
        Alr->>Log: append 'fail' event (prev_status, new_status=OPEN)
    end
    loop For each passing outcome
        Svc->>Alr: AutoResolveAlert(rule, outcome.fingerprint, evaluationId)
        Alr->>Log: append 'pass' event (prev_status, new_status=RESOLVED)
    end
    Svc-->>Trig: evaluation { result, outcomes }
```

**Notes**

- The `Outcome` list always covers every asset the template cares about — passing assets included. The service uses that to **auto-resolve** prior alerts whose fingerprint isn't in the failing set.
- Resolver / kernel errors short-circuit the loop and raise an `engine.error` meta-alert instead of a data alert — see §5.
- `CreateEvaluation` + every alert/event write happen inside one transaction, so a mid-loop failure rolls everything back.

---

## 3. Alert lifecycle

An **Alert** is the stable, dedup'd entity for one `(rule, fingerprint)` pair. There is exactly one Alert per pair for the lifetime of the rule — re-opens flip `status` back to `OPEN` **in place**, they do not create new rows. The full transition history lives in the `alert_event` log.

```mermaid
stateDiagram-v2
    [*] --> OPEN: failing eval for fingerprint
    OPEN --> ACKNOWLEDGED: POST /alerts/{id}/ack
    OPEN --> RESOLVED: next eval passes (auto)
    OPEN --> RESOLVED: POST /alerts/{id}/resolve { fixed_by_booking }
    OPEN --> RESOLVED: POST /alerts/{id}/accept (accepted_by_business)
    ACKNOWLEDGED --> RESOLVED: next eval passes (auto)
    ACKNOWLEDGED --> RESOLVED: POST /alerts/{id}/resolve { fixed_by_booking }
    ACKNOWLEDGED --> RESOLVED: POST /alerts/{id}/accept (accepted_by_business)
    RESOLVED --> OPEN: same fingerprint fails again (reopen — same row)
```

**Invariants**

- Exactly **one** alert row per `(rule_id, fingerprint)` — enforced by the UNIQUE constraint on the `alert` table (see [migration #6](../../internal/storage/migrations/migrations.go)).
- Reopen after `RESOLVED` flips status back to `OPEN` on the **same row**. The lifetime `occurrence_count` keeps incrementing. The prior `resolution` and `ack` are cleared on the alert row but **preserved** as `alert_event` rows.
- Every transition (fail, pass, ack, resolve, accept) appends one row to `alert_event`. That log is append-only by convention — code paths never UPDATE or DELETE.

### Reading the history

The alert row carries the *current* state (status, current evidence, current resolution if RESOLVED). For the **timeline** of the alert — every evaluation that touched it, every manual transition, every prior resolution across reopen cycles — query `alert_event` via `GET /alerts/{id}/events`. That endpoint is the single source of truth for audit reconstruction.

A `fail` event with `prev_status = RESOLVED` IS a reopen. The API surfaces this as a derived `isReopen` flag on each event response.

---

## 4. Resolution paths

```mermaid
flowchart LR
    subgraph "Closure paths"
        A[Auto-resolved<br/>next eval passes] --> R(RESOLVED on the alert row<br/>+ pass event)
        F[Fixed by booking<br/>POST /resolve + tx refs] --> R2(RESOLVED on the alert row<br/>+ resolve event)
        B[Accepted by business<br/>POST /accept + note + evidence snapshot<br/>+ optional expiresAt] --> R3(RESOLVED on the alert row<br/>+ accept event)
    end
    R -.fingerprint fails again.-> Reopen(Same alert row<br/>status → OPEN<br/>+ fail event with prev=RESOLVED)
    R2 -.fingerprint fails again.-> Reopen
    R3 -.fingerprint fails again.-> Reopen
```

**Required artefacts per path**

| Path | Author | Timestamp | Note | Transaction refs | Evidence snapshot | Expires |
|---|---|---|---|---|---|---|
| `auto`               | system | now | — | — | — | — |
| `fixed_by_booking`   | operator | now | optional | optional | — | — |
| `accepted_by_business` | operator | now | **required** | — | frozen at acceptance | optional |

Stored on the alert row as `resolution` (JSONB) for the *current* closure, and in `alert_event.payload` for the historical record of every prior resolution across reopen cycles.

```jsonc
{
  "kind": "accepted_by_business",
  "by":   "treasurer@buildr.com",
  "at":   "2026-06-17T12:34:56Z",
  "note": "Settlement lag on GBP corridor, confirmed by treasury.",
  "evidenceSnapshot": { "asset": "GBP/2", "drift": "50000", "tolerance": 0, … },
  "expiresAt": "2026-07-17T00:00:00Z"
}
```

---

## 5. Engine-error meta-alerts

```mermaid
flowchart TB
    Eval[Engine.Evaluate] -- runtime error --> Translate[ErrEvaluate wrap]
    Translate --> Svc[Service.EvaluateRule ✅]
    Svc --> EngEvt[INSERT evaluation<br/>result = ERROR]
    Svc --> MetaAlr[Open engine.error meta-alert<br/>labels: { kind: 'engine.error' }<br/>fingerprint: 'engine.error']
    MetaAlr -.distinct channel.- Notif[Notifications]
```

Why a separate path: a kernel/resolver failure (timeout, CEL builtin throw, budget exceeded) is not a *financial* alert. Routing it through the same channel as data alerts would contaminate the financial-alert feed and confuse ops. The meta-alert uses the same alert + event infrastructure as data alerts — only the `engine.error` fingerprint and the `kind: engine.error` label distinguish it.

The translation happens in [engine/errors.go](../../internal/engine/errors.go) via `ErrEvaluate`.

---

## 6. Evaluation idempotence & PIT propagation

```mermaid
flowchart LR
    Tick[Schedule tick @ T] --> Sub[Subtract safety margin]
    Sub --> PIT["PIT = T - 30s"]
    PIT --> Sources["Each Source.PIT = T - 30s"]
    Sources --> Eng[Engine.Evaluate]
    Eng --> Persist["INSERT evaluation<br/>pit_per_source = { 'ledger_set:0': T-30s, … }"]
    Persist --> Audit[Replayable at the same PIT]
```

Every `Evaluation` row stores the **resolved** PIT per `Source`. This means an auditor can pose the question *"what was the answer at the time the rule fired?"* and get a reproducible result by replaying each source's PIT call. See [ADR-002](../prd/adr-002-pit-consistency.md).

---

## 7. Audit log evolution — alert_event as the seed

The `alert_event` table is structured as append-only today: every transition is written via a single helper (`appendAlertEvent`), and no code path issues UPDATE or DELETE against it. That's enough for V1 GA — operators get a faithful timeline for every alert.

The longer-term direction is the same kind of cryptographic audit chain the Ledger uses for transactions. The table is shaped so that evolution is additive:

- A per-alert `seq` column can be added later for strict ordering inside one alert's timeline (without breaking existing reads, which order by `(at, id)`).
- A `prev_hash` / `hash` pair can chain events into a Merkle-style log, so a single root hash per alert proves the entire history is intact.

None of that is in V1 GA — flagging it here so the alert_event shape stays compatible with that direction. The instrumentation point for any of it is `appendAlertEvent` in [internal/storage/alert.go](../../internal/storage/alert.go).
