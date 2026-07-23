# Notification suppression — repeated identical fails

> Why a still-broken alert stops re-paging on every scheduler tick, and how
> that's implemented without touching the audit record. ✅ Shipped.

## The problem

Before the durable [scheduler worker](./scheduler.md) landed, evaluation was
on-demand: an alert re-failed only when a human (or a script) re-ran the rule,
so the `reconciliation.alert.updated` event was rare and meaningful.

Once rules evaluate on a cadence, that stops being true. Every failing
evaluation appends a `fail` row and publishes a webhook
([api.md §Events](./api.md#events)), and the row→event mapping is
`fail + prev ∈ {OPEN, ACKNOWLEDGED} → updated`. So a rule on a 5-minute cron
that stays broken emits `reconciliation.alert.updated` **every 5 minutes,
indefinitely** — each one fanning out through Webhooks to the customer's Slack /
PagerDuty / email. A discrepancy that hovers at the tolerance boundary makes it
worse. None of those repeats carry information the consumer doesn't already
have. That is a pager flood, and it trains operators to ignore the channel.

## The decision

Make the notification a **per-event decision**, computed once when the row is
written, and stored on the row as `alert_event.notify`:

- `notify = true` → the transition is published to the message bus.
- `notify = false` → the transition is recorded in the append-only log for
  audit, but **never published**.

Only one case is ever suppressed: a **steady-state repeat** — an already-`OPEN`
alert failing again with **materially-identical evidence**. Everything that
carries new information stays `notify = true`:

| Transition | Published? | Why |
|---|---|---|
| `opened` — first fail in the period | ✅ | new case |
| `reopened` — fail after a resolve (`prev = RESOLVED`) | ✅ | the case came back |
| resurfacing — fail on an `ACKNOWLEDGED` alert (`prev = ACKNOWLEDGED` → `OPEN`) | ✅ | the ack no longer holds |
| `updated` — fail on `OPEN`, **evidence changed** | ✅ | the discrepancy moved |
| `updated` — fail on `OPEN`, **evidence identical** | 🚫 suppressed | nothing new to say |
| `acknowledged` / `resolved` / `accepted` | ✅ | manual transitions always notify |

> **We suppress the message, not the record.** A suppressed fail still writes
> its `alert_event` row and still increments `occurrence_count`. The
> append-only log remains a complete, per-evaluation history; only the *pager*
> goes quiet. This preserves the audit guarantee the
> [period model](./alert-period-model.md) depends on — and a suppressed alert is
> still `OPEN`, so it still counts against "is this period green?".

## What counts as "materially identical"

Evidence equality is **canonical**, not byte-for-byte. The incoming evidence is
freshly marshalled by a template; the stored evidence has been round-tripped
through Postgres `jsonb`. Semantically-equal payloads routinely differ in key
order and whitespace across that boundary, so a raw `bytes.Equal` would treat
every repeat as a change and suppress nothing.

`storage.sameEvidenceJSON` compares the canonical form of each payload: object
keys sorted (Go's `encoding/json` marshals map keys in sorted order) and numeric
literals preserved verbatim via `json.Number` (so large balances don't lose
precision through a `float64`). Two empty payloads are equal; **a payload that
fails to parse is treated as different** — the safe default is to notify when in
doubt.

This is deliberately strict: `drift: "99" → "98"` (both failing) *is* a change
and publishes. Smoothing near-identical-but-not-equal values is a separate
concern (hysteresis / flap detection), explicitly **not** in this change.

## Implementation

The change is small and lives entirely in the storage + model layers — no engine
changes, no new API surface.

1. **Schema** ([migrations.go](../../internal/storage/migrations/migrations.go))
   — `alert_event` carries `notify boolean NOT NULL DEFAULT true`. The whole V1
   alert model is unreleased, so this is folded straight into the table's
   `CREATE TABLE` rather than tacked on as a separate `ALTER` — no useless
   migration step. The `DEFAULT true` keeps "every transition notifies" as the
   baseline, so only the deliberately-suppressed cases ever write `false`.

2. **Decision** ([storage/alert.go](../../internal/storage/alert.go),
   `openOrUpdateAlertOnce`) — on the update/reopen path we capture the alert's
   current evidence *before* the `UPDATE` overwrites it, then compute:

   ```go
   notify := reopened ||
       prev != models.AlertOpen ||
       !sameEvidenceJSON(prevEvidence, in.Evidence)
   ```

   The single insertion point `appendAlertEvent` takes `notify` and persists it;
   every other caller (first open, ack, resolve, accept, auto-resolve `pass`)
   passes `true`.

3. **Gate** ([storage/store.go](../../internal/storage/store.go),
   `recordAlertEvent`) — the one hook that turns an appended row into an outbound
   message returns early when `event.Notify == false`. This covers both dispatch
   paths (immediate for manual API actions, buffered-until-commit for the
   evaluation path) in one place, and emits a `Debug` log so suppression is
   observable. The invariant on this hook refines from *one row ⇒ one message* to
   **one row with `notify = true` ⇒ one message**.

## Workflow

```text
scheduler tick → evaluate rule → FAIL outcome → OpenOrUpdateAlert
                                                      │
                          ┌───────────────────────────┼───────────────────────────┐
                          ▼                            ▼                            ▼
                   no active alert            OPEN, evidence changed        OPEN, evidence identical
                   → opened (notify)          → updated (notify)            → updated (notify=FALSE)
                          │                            │                            │
                          ▼                            ▼                            ▼
                    append row + publish        append row + publish         append row, NO publish
                                                                          (occurrence_count still ++)
```

The append-only log is identical in all three branches — a row per evaluation.
The only difference is whether `recordAlertEvent` hands that row to the
publisher.

## Configuration

On by default, globally — there is no per-rule toggle. Because the full failing
history stays in the log regardless, suppression loses no audit data, so there's
nothing to gate behind a flag. If a kill-switch is ever needed it can be added at
the `recordAlertEvent` gate without touching the decision or the schema.

## Relationship to snooze

Suppression is automatic and machine-driven: it silences duplicate *noise* the
system generates. **Snooze** (see [workflows.md §Snooze](./workflows.md)) is the
human counterpart — a time-boxed, operator-initiated mute of an alert that *is*
changing and would otherwise legitimately notify. Both reuse the same principle
(suppress the notification, never the record) and, in the implementation, the
same `notify` decision point.

## Open question — log volume

Suppression keeps the *pager* quiet but, by design, still writes a `fail` row per
evaluation. A continuously-broken rule on a fast cadence therefore still grows
`alert_event` linearly. Whether to additionally collapse identical repeats in the
log itself (e.g. bump `occurrence_count` + `last_seen_at` without a new row, or
roll up to a counter) is a separate, deliberately-deferred decision — it trades
audit granularity for storage, and is tracked alongside the
[audit-log evolution](./workflows.md) notes.
