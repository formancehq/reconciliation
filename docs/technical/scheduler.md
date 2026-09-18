# Cron scheduler

> Runs rule evaluations automatically on a schedule — the counterpart to the
> on-demand `POST /rules/{id}/evaluate`.

## What it does

A rule can declare a **cron schedule** (`schedule.kind = "cron"`, with a cron
`expr` and optional `tz`). When the scheduler is enabled, it fires `EvaluateRule`
for each such rule at its scheduled times — the same code path a manual
evaluation takes (each reads its data ledgers live and records its own `_recon`
capture — ADR-003), so detection, the period model, the alert lifecycle, and
event delivery all behave identically.

The cron expression is **validated at rule-create time** (`POST /rules`) — a bad
expression is rejected up front, not discovered silently at run time.

## How it works (V1 — in-process)

A single goroutine ([internal/scheduler/](../../internal/scheduler/)) ticks once a
minute (cron is minute-granular):

1. List enabled rules with a cron schedule. `enabled` is filtered **server-side**
   (it is a declared, indexed metadata field), and the scan **drains every page** —
   a tick never schedules a subset of the rules it was supposed to see. Cron-ness
   is still checked in-process, because `schedule` is stored as opaque JSON with no
   indexed discriminator. An implausibly large rule set (100k enabled rules) fails
   the tick loudly rather than firing an arbitrary slice of it.
2. For each, check whether its cron expression came due in the last tick window
   (`dueInWindow`: the schedule's next firing after the previous tick is `<= now`).
3. Fire the due ones via `EvaluateRule` (each in its own goroutine; failures are
   logged and never stop the loop — engine-side errors still raise the
   `engine.error` meta-alert).

**Timezone:** the rule's `schedule.tz` when set, else **UTC** (deterministic —
never the host's local zone).

**Enabling it:** off by default. `--scheduler-enabled` turns it on;
`--scheduler-interval` (default `1m`) sets the tick granularity.

## Shutdown — evaluations drain, they are not cancelled

An evaluation is **not one atomic write**. The control ledger has no
cross-operation transaction (`Service.inTx` is a passthrough), so a single
`EvaluateRule` records its capture and then opens or resolves each alert as
*separate* ledger transactions. Cutting an evaluation off partway is therefore
not a clean abort:

| Cancelled… | Result |
|---|---|
| before the capture | the control read the ledgers and recorded **nothing** — from the outside the rule never ran that tick |
| between capture and alerts | the capture claims N breaks whose alerts were never opened — audit record and alert state disagree |
| partway through the alerts | some of that evaluation's alerts opened, the rest did not |

The first is the silent-non-execution failure this product exists to catch, and
none of the three is visible after the fact. Because it only happens on process
stop, it would otherwise land on **every rolling deploy**.

So the scheduler keeps two contexts apart:

- the **loop** context — cancelled on stop, which stops *starting* new work;
- the **work** context — deliberately detached (`context.WithoutCancel`), so an
  evaluation already in flight runs to completion.

`Run` returns only once the in-flight evaluations drain, bounded by a 30s grace
(`defaultShutdownGrace`, matching `engine.DefaultLimits.MaxWallClock` so a
well-behaved evaluation has roughly its own budget to finish in). Past the grace
the work context *is* cancelled, with an error logged naming the risk — an
unbounded wait would hang shutdown, and the process is going down regardless.

The fx `OnStop` hook waits for `Run` to return, bounded by fx's own stop context.
That wait is what makes the drain mean anything: returning straight after
cancelling would let the process exit mid-evaluation. It never fails shutdown —
by then the loop is stopped either way, and an error would only mask the real
cause.

> This is about **shutdown**, not throughput. A tick still fans out one goroutine
> per due rule with no concurrency limit, so N rules sharing a midnight cron start
> N evaluations at once. Bounding that is tracked separately.

## ⚠️ Single-active-instance assumption

This scheduler fires on **every process it runs in**. With multiple replicas it
would fire each schedule N times (N× evaluations, N× events). For V1 it must
run on **exactly one instance** (or stay disabled). Reconciliation evaluations
are largely idempotent within a period — duplicate runs dedup onto the same
alert (the guarded marker CAS + idempotency keys) — but **events would be
emitted multiple times**, so don't scale the scheduler out as-is.

## Planned follow-up (multi-replica safety)

Per [PRD §8](../prd/README.md), the scheduler host is a deliberate open
decision. The in-process MVP is the lean V1 start; with Postgres gone, the path
to running safely on N replicas is one of:
- a **ledger-native lease** — a compare-and-swap on a control-ledger account
  (a bare-source guard, like the alert marker) so only the lease-holder's loop
  fires, or
- moving schedules to **Temporal** (already in the stack) for durable,
  exactly-once, observable scheduling.

Both reuse the same "list cron rules → fire due ones" core; only the
fire-exactly-once mechanism changes.
