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

1. List enabled rules with a cron schedule.
2. For each, check whether its cron expression came due in the last tick window
   (`dueInWindow`: the schedule's next firing after the previous tick is `<= now`).
3. Fire the due ones via `EvaluateRule` (each in its own goroutine; failures are
   logged and never stop the loop — engine-side errors still raise the
   `engine.error` meta-alert).

**Timezone:** the rule's `schedule.tz` when set, else **UTC** (deterministic —
never the host's local zone).

**Enabling it:** off by default. `--scheduler-enabled` turns it on;
`--scheduler-interval` (default `1m`) sets the tick granularity.

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
