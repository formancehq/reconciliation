# Alert period model

> Why a reconciliation alert is scoped to a **period**, not an immortal
> `(rule, fingerprint)` row — and how that's implemented.

## The problem

An alert's dedup identity used to be `(rule_id, fingerprint)`, a single row that
opened, resolved, and **reopened in place forever**. The fingerprint identifies
*what* is wrong (e.g. `asset:USD/2`); see [api.md §Alerts](api.md). That immortal
row has two failure modes for reconciliation:

1. **It masks past issues.** Reconciliation is *periodic*: "March is reconciled"
   is a permanent claim about a closed accounting period. If a discrepancy
   existed in March, that fact must survive even after the same fingerprint goes
   green later. An immortal alert that flips back to RESOLVED lets a later green
   state hide that an earlier period was red — you can look clean for the
   duration of an audit and fall off again afterwards (cf. Wirecard). The
   discrepancy, and the adjustment that justified it, must stay on the record
   for *their* period.
2. **It conflates distinct cases.** A USD/2 break in March and an unrelated
   USD/2 break in November are different business events with different root
   causes — not "the same problem recurring." One immortal timeline fuses them,
   muddying per-case metrics and ownership.

## The decision

Scope the alert's dedup **identity by period**:

```
identity = (rule_id, fingerprint, period_id)
```

- The **fingerprint stays structured** ("what is wrong") — the period is a
  *separate column*, not fused into the fingerprint hash. This is the key to
  not "restricting the model": you can still `GROUP BY` the fingerprint across
  periods to ask family-level questions ("has USD/2 been problematic for six
  months?") while each `(fingerprint, period)` is its own immutable case.
- A new period opens a **fresh case** (new alert id, `opened` — not a reopen of
  a prior period's). Past periods are immutable historical record.
- Reopen-in-place is **bounded to within a period** — the period *is* the flap
  window. No separate time-based reopen timer is needed.

### What we deliberately did **not** build (yet)

- **A two-table issue + episode model.** `unique(issue_id, period_id)` is the
  same dedup as `unique(rule_id, fingerprint, period_id)` plus a materialized
  parent row. A parent "issue" entity only earns its keep once the *family*
  needs its own mutable state (owner, mute, cross-period SLA). Operators work
  *periods* today ("get April green"), so the family is a reporting lens — a
  `GROUP BY`, not a table. The alert row is effectively the episode; an `issues`
  table can be backfilled later without a painful migration.
- **A `scope_type` taxonomy** (instant / run / rolling-window / …). One periodic
  derivation covers V1; `period_id` is a generic string column that generalises
  when a genuinely non-periodic rule appears.

## The mechanism

### 1. Rule cadence → period id

A rule declares a **cadence** ([models.Cadence](../../internal/models/rule.go)):

| Cadence | `period_id` for a PIT of 2026-03-15T10:30Z | Meaning |
|---|---|---|
| `continuous` *(default)* | `continuous` | Live monitoring — one unbounded scope; reopens in place forever. |
| `daily` | `2026-03-15` | One case per UTC calendar day. |
| `weekly` | `2026-W11` | One case per ISO week (ISO year + week number). |
| `monthly` | `2026-03` | One case per UTC calendar month. |

`Cadence.PeriodID(pit)` is **deterministic**: any instant in the same bucket
yields the same id, so re-evaluating a period *continues* its existing case
rather than spawning a new one. Bucketing is **UTC** — the accounting-period
timezone is a known V1 simplification (a late-March instant in a western zone
buckets to April).

### 2. Evaluation derives and threads the period

The evaluation runner ([`(*Runner).run`](../../internal/reconciliation/runner.go)) computes
`periodID = rule.Cadence.PeriodID(req.PIT − req.SafetyMargin)` once and threads
it through `driveAlerts` into every alert write. The period is bucketed from the
**margin-adjusted** instant — the *same* one the resolvers read at — not the raw
PIT: an evaluation just after a period boundary with a positive margin (the 30s
default) reads the *previous* period's data, so its alert must be scoped to that
previous period, and a later rerun of the real period continues the same case.
The writes it threads through:

- **Open / update** — `OpenOrUpdateAlert` dedups on `(rule_id, fingerprint,
  period_id)`. First fail in a period → `opened`; subsequent → `updated`; after
  a resolve *in the same period* → `reopened`. A fail in a *new* period → a
  fresh `opened`.
- **Auto-resolve & the disappear-sweep** — both are **scoped to the current
  period**. A passing/vanished fingerprint closes its case for *this* period
  only; a prior period's open cases are never swept. This is the anti-"cook the
  books" guarantee: running April green leaves March's open case exactly as it
  was.

### 3. Engine-health alerts stay continuous

The `engine.error` meta-alert (resolver timeout, kernel throw) is operational —
"the check couldn't run", not "March didn't reconcile" — so it is always opened
in the `continuous` scope regardless of the rule's cadence.

### 4. Period reconciliation status

The product's headline question — *"is March green?"* — is **green ⇔ zero
active (OPEN/ACKNOWLEDGED) alerts** in the period, read via the wired path
`GET /alerts?periodID=2026-03&status=OPEN` (and internally
`Storage.ListActiveAlertFingerprints(rule, period)`, whose empty result means
green). Resolved/accepted alerts don't count against it but remain on record,
so a period that went green stays auditable: the resolved cases (and the
adjustments that justified them) are still there. If an adjustment later looks
questionable, the proof that there *was* an issue is preserved. (A dedicated
period-status endpoint can wrap this when a UI needs it — deferred until then
rather than shipping an unwired method.)

## Backwards compatibility

The default cadence is `continuous`, whose `period_id` is the constant
`"continuous"`. Under it, `(rule_id, fingerprint, "continuous")` is exactly the
old `(rule_id, fingerprint)` dedup — so the migration is **behaviour-preserving**
for existing rules and rows. Periodic scoping is **opt-in per rule**.

> **Open product decision.** The default is `continuous` to keep this change
> additive. The product framing ("reconciliation is periodic") arguably wants
> `monthly` as the default for reconciliation rules — that's a one-line flip
> (`service.CreateRule`), deferred pending the team's call.

## Trade-offs & known edges

- **Daily cadence churns persistent breaks.** A break that persists five days
  becomes five daily cases. For reconciliation that's *correct* — each day
  independently certifies — but it's a conscious choice, the opposite of what
  you'd want in ops monitoring (where it would be noise).
- **UTC period boundaries** (see above).
- **No cross-period rollup entity** — answered by query today; promote to an
  `issues` table if/when family-level management is needed.

## Interaction with webhooks

`period_id` rides on the `Alert` in every [`reconciliation.alert.*` event](api.md)
payload, so consumers can route/aggregate by period. `reopened` now means a
*within-period* reopen; the same fingerprint failing in a new period is a fresh
`opened`.

## Code map

| Concern | Location |
|---|---|
| Cadence + `PeriodID` derivation | [internal/models/rule.go](../../internal/models/rule.go) |
| `period_id` on the alert | [internal/models/alert.go](../../internal/models/alert.go) |
| Period-scoped dedup / sweep / status | [internal/storage/alert.go](../../internal/storage/alert.go) |
| Period derivation + threading | [internal/reconciliation/runner.go](../../internal/reconciliation/runner.go) (`(*Runner).run` + `driveAlerts`) |
| Cadence on create | [internal/api/service/rule.go](../../internal/api/service/rule.go) |
| Schema | migration #8 in [internal/storage/migrations/migrations.go](../../internal/storage/migrations/migrations.go) |
