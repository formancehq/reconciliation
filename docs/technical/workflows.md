# Lifecycle Workflows

The V1 product surface is a **business lifecycle**, not a rules engine. This document is the visual reference for that lifecycle:

> **Observe → Detect → Alert → Evidence → Resolve or Accept**

Each diagram shows one flow. Since the ledger-native migration, all state lives on the
control-ledger `_recon` and the ledgers being reconciled are read at **query checkpoints**
(see [architecture.md](./architecture.md) and [ADR-002](../prd/adr-002-pit-consistency.md)).

> **Canonical reference test.** The evaluate→alert flows are exercised end-to-end in
> [`v1_orchestration_test.go`](../../internal/api/service/v1_orchestration_test.go) (unit, fakes)
> and [`evaluation_it_test.go`](../../internal/api/service/evaluation_it_test.go) (against a live
> ledger, at a real checkpoint). If the diagrams and the tests diverge, the tests win.

---

## 1. Rule creation

```mermaid
sequenceDiagram
    autonumber
    participant U   as Operator (API / fctl)
    participant Svc as Service.CreateRule
    participant Reg as templates.Registry
    participant Eng as engine.Engine
    participant L   as Ledger (_recon)

    U->>Svc: POST /rules { templateKind, templateSpec, … }
    Svc->>Reg: Get(templateKind).Validate(spec)
    alt invalid spec
        Reg-->>Svc: ErrInvalidSpec
        Svc-->>U: 400 VALIDATION
    end
    Svc->>Reg: evaluator.Explain(spec) → representative CEL
    Svc->>Eng: Compile(compiledCEL)  (sanity-check it parses)
    Svc->>L: SaveAccountMetadata(rule:{id}, typed metadata)
    L-->>Svc: applied
    Svc-->>U: 201 Created { rule }
```

**Notes**

- The rule is a typed-metadata account `rule:{id}` on `_recon` — no relational row.
- `Explain()` returns one representative CEL string for `compiled_cel` (explainability); real
  evaluation re-renders the per-asset CEL at run time.

---

## 2. Rule evaluation

```mermaid
sequenceDiagram
    autonumber
    participant Trig as Trigger (cron / POST evaluate)
    participant Svc  as Service.EvaluateRule
    participant CP   as Checkpointer (ledger)
    participant Reg  as templates.Registry
    participant Eng  as engine.Engine
    participant Data as data-ledgers A/B
    participant L    as Ledger (_recon)

    Trig->>Svc: evaluate(rule)
    Svc->>CP: AcquireCheckpoint()  (pins one cross-ledger cut)
    CP-->>Svc: checkpointID
    Svc->>Reg: Evaluate(spec, eng, resolvers, {checkpointID, PIT})
    Reg->>Data: AggregateVolumes(query, checkpointID)  (Tier-1, per source)
    Data-->>Reg: balances (per asset)
    Reg->>Eng: Compile + Evaluate(per-asset CEL, checkpointID)
    Eng->>Data: balance(source) via builtin @ checkpointID
    Eng-->>Reg: pass/fail
    Reg-->>Svc: []Outcome  (one per fingerprint axis)
    loop For each failing outcome
        Svc->>L: OpenOrUpdateAlert — Numscript batch (marker + metadata + last_transition)
    end
    loop For each passing / disappeared fingerprint
        Svc->>L: AutoResolveAlert — guarded burn (marker → pool)
    end
    Svc->>CP: Release(checkpointID)  (cancellation-surviving ctx)
    Svc-->>Trig: evaluation { result, outcomes }
```

**Notes**

- The service pins **one checkpoint per evaluation**; every Tier-1 ledger source (and the kernel
  cross-check) reads at it, so ledgers A and B are compared at one consistent cut. Tier-2 pool
  sources read "latest". Released on a cancellation-surviving context (**F26**).
- The `Outcome` list covers every asset the template touched — passing included — so the service
  can **auto-resolve** prior alerts whose fingerprint isn't in the failing set.
- **No durable evaluation row.** The result is returned; the break `evidence` is durable on
  `alert:item`; there is no `/evaluations` read surface (RFC §4.4.2). Correctness rests on
  idempotent, guarded per-alert batches — there is no cross-store transaction to roll back.
- Resolver / kernel errors short-circuit and raise an `engine.error` meta-alert (§6).

---

## 3. Alert lifecycle

An **Alert** is the stable, dedup'd entity for one `(rule, fingerprint, period)`. There is exactly
one alert per triple — re-opens flip `status` back to `OPEN` **in place**. The transition history
is the ledger log (§8).

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
    RESOLVED --> OPEN: same fingerprint fails again (reopen — same alert)
    OPEN --> OPEN: POST /snooze · /unsnooze (status-neutral mute)
```

**Invariants** — the mechanics behind this state machine are the account model in
[architecture.md](./architecture.md#the-control-ledger-data-model):

- Exactly **one** `alert:item:*` account per `(rule, fingerprint, period)`. The `ALERT` **marker**
  (`alert:st:{state}:*`, EPHEMERAL) is the status source-of-truth; a guarded Numscript move is the
  compare-and-swap that serialises transitions — no DB unique constraint.
- **Reopen** flips `status` back to `OPEN` on the same item and re-mints the marker (it was burned
  on close); the `OCC` balance (occurrence count) keeps climbing; the prior `resolution`/`ack` keys
  are cleared on the item but preserved in the log.
- Every transition writes a self-describing `last_transition` envelope in the same atomic batch, so
  the ledger log is the faithful timeline.
- **Snooze is status-neutral**: a snoozed alert stays `OPEN`/`ACK`; only the mute intent is recorded (§5).

---

## 4. Resolution paths

```mermaid
flowchart LR
    subgraph Closure
        A[Auto-resolved<br/>next eval passes] --> R(RESOLVED — burn marker → pool<br/>+ last_transition auto_resolved)
        F[Fixed by booking<br/>POST /resolve + tx refs] --> R2(RESOLVED<br/>+ last_transition resolved)
        B[Accepted by business<br/>POST /accept + note + evidence snapshot] --> R3(RESOLVED<br/>+ last_transition accepted)
    end
    R -.fingerprint fails again.-> Reopen(Same alert<br/>status → OPEN, re-mint marker<br/>+ last_transition reopened)
    R2 -.fingerprint fails again.-> Reopen
    R3 -.fingerprint fails again.-> Reopen
```

| Path | Author | Note | Tx refs | Evidence snapshot |
|---|---|---|---|---|
| `auto` | system | — | — | — |
| `fixed_by_booking` | operator | optional | optional | — |
| `accepted_by_business` | operator | **required** | — | frozen at acceptance |

The *current* closure is typed metadata (`resolution`) on `alert:item`; the historical record of
every prior closure across reopen cycles is the sequence of `last_transition` envelopes in the
ledger log.

---

## 5. Snooze & notification suppression

Once rules fire on a [schedule](./scheduler.md), a still-broken alert would re-emit an event on
every tick. Reconciliation **records** the signal an operator needs to suppress noise, but — since
the ledger-native migration — **it no longer gates delivery itself**. Delivery is the ledger
events sink (§8); *suppression is now a consumer concern* (the Webhooks module / a future digest),
driven by the data recon exposes:

| | What recon records | Who suppresses |
|---|---|---|
| **Repeat** (materially-identical fail on an `OPEN` alert) | an `OCC` bump + a fresh `last_transition` (`occurred`) with the current evidence | consumer (dedupe on unchanged evidence) — the write-side `notify` gate was removed with Postgres |
| **Snooze** | `snooze` metadata (until/by/at/note) on the item + `last_transition` (`snoozed`/`unsnoozed`) | consumer (honour `snooze.until`) |

```mermaid
flowchart LR
    S[POST /alerts/id/snooze<br/>until = T, by, note] --> M(snooze metadata set on item<br/>+ last_transition snoozed)
    M -- POST /alerts/id/unsnooze --> Lift[snooze cleared atomically<br/>+ last_transition unsnoozed by actor]
    M -.resolve/accept/auto.-> Clr[snooze cleared on close]
```

**Rules**

- **Snooze** requires an active (`OPEN`/`ACK`) alert and a **future** `until`; re-snoozing
  overwrites the window; closing an alert clears any snooze (so a later reopen is never silently muted).
- **Unsnooze** lifts a snooze early, idempotently, and now **attributes the actor** (`by`) — the
  clear + the `last_transition` land in one atomic batch (`Client.ApplyMetadata`).
- `snooze`/`unsnooze` are status-neutral (`prevStatus == newStatus`).

See [notification-suppression.md](./notification-suppression.md) for why suppression matters and
what a consumer needs. Write-side repeat-suppression and delivery muting are **deferred** to the
consumer/semantic-event work (RFC §4.4).

---

## 6. Engine-error meta-alerts

```mermaid
flowchart TB
    Eval[Engine.Evaluate] -- runtime error --> Translate[ErrEvaluate wrap]
    Translate --> Svc[Service.EvaluateRule]
    Svc --> MetaAlr["Open engine.error meta-alert<br/>fingerprint: engine.error<br/>label kind: engine.error"]
    MetaAlr -.distinct channel.- Notif[Notifications]
```

A kernel/resolver failure (resolver timeout, CEL builtin throw, budget exceeded) is not a
*financial* alert, so it opens a synthetic `engine.error` meta-alert (same alert infrastructure, a
distinct `engine.error` fingerprint + `kind: engine.error` label) instead of a data alert. The
evaluation returns `ERROR` (non-durable). Translation lives in
[engine/errors.go](../../internal/engine/errors.go).

---

## 7. Checkpoint consistency

```mermaid
flowchart LR
    Tick[Evaluate @ T] --> Acq[AcquireCheckpoint → checkpointID C]
    Acq --> Read["Tier-1 sources read A, B @ C<br/>(one consistent cross-ledger cut)"]
    Read --> Eval[Engine.Evaluate]
    Eval --> Rel[Release C]
```

There is **no arbitrary PIT** in Ledger v3 — the anchor is a **query checkpoint** (ADR-002). One
checkpoint pinned per evaluation freezes ledgers A, B and `_recon` at the same log sequence, so a
cross-ledger rule reads a skew-free snapshot. `PIT` survives only as the nominal instant used to
derive the reconciliation period and as the audit timestamp for Tier-2 (pool) sources. While a
checkpoint is retained, the read is re-derivable; the durable break `evidence` on `alert:item`
survives regardless (ADR-002 §8).

---

## 8. Audit history & delivery — the ledger log

There is **no `alert_event` table**. The control-ledger's ordered, append-only log *is* the audit
history: every transition is a `COMMITTED_TRANSACTION` (marker move + `account_metadata`) or, for
snooze/unsnooze, a `SAVED_METADATA`/`DELETED_METADATA` entry — each carrying the self-describing
`last_transition` envelope. The ledger already gives the cryptographic transaction-log guarantees
recon used to aspire to.

- **Delivery** rides the ledger's native **events sink** ([architecture.md](./architecture.md#event-delivery)):
  when `--events-sink-url` is set, recon provisions an HTTP webhook sink for
  `[COMMITTED_TRANSACTION, SAVED_METADATA, DELETED_METADATA]`; consumers filter on
  `event.ledger == _recon`.
- **`GET /alerts/{id}/events` (`ListAlertEvents`) returns empty today** — paginated per-alert
  history needs a downstream queryable sink (ClickHouse/Databricks), because the ledger log has no
  per-account filter (RFC §10). Deferred.
