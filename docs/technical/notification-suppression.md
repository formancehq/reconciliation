# Notification suppression — repeated identical fails

> Why a still-broken alert shouldn't re-page on every scheduler tick, and where
> that responsibility now lives after the ledger-native migration.

## The problem

Once rules evaluate on a [cadence](./scheduler.md), a rule that stays broken
produces a transition **every tick**: a 5-minute cron that keeps failing emits a
`reconciliation.alert.occurred` event every 5 minutes, indefinitely. None of
those repeats carry information the consumer doesn't already have. Delivered
straight through to Slack / PagerDuty / email that is a pager flood, and it
trains operators to ignore the channel. The same is true of an alert an operator
has deliberately **snoozed**: it keeps failing, but the operator has said "don't
page me about this until T".

## Where suppression lives now

Reconciliation no longer runs a message bus, and **no longer gates delivery at
the write layer**. Delivery is the ledger's native events sink
([api.md §Events](./api.md#events)): the ledger emits one event per committed log
entry, and the consumer (the Webhooks module, or a future per-recipient digest)
decides what to page. Reconciliation's job is to make every transition
**self-describing enough that a consumer can suppress correctly** — it records
the signal, it does not silence the pager itself.

> **Record everything; page selectively — at the consumer.** The control-ledger
> log is the complete, per-evaluation history (every `occurred` bump is a real
> log entry). A suppressed alert is still `OPEN` and still counts against "is
> this period green?". Suppression removes *messages*, never *records* — and now
> that filtering happens downstream of the log, not upstream of it.

## What recon exposes for a consumer to suppress on

Every transition carries a self-describing `last_transition` envelope
(`type`, `prevStatus`→`newStatus`, `correlationID`, `payload`), so a consumer has
what it needs without reverse-engineering the metadata diff:

| Case | Signal recon emits | How a consumer suppresses |
|---|---|---|
| **Steady-state repeat** — an already-`OPEN` alert failing again with materially-identical evidence | `reconciliation.alert.occurred` with the current `evidence` in the alert item | dedupe: don't page if the evidence is unchanged since the last delivered event for this alert |
| **Snooze** — operator muted the alert until `T` | `reconciliation.alert.snoozed` + the `snooze` metadata (`until`/`by`/`note`) on the item | hold notifications for the alert until `snooze.until`; `reconciliation.alert.unsnoozed` lifts it (with the actor) |

Everything that carries new information — `opened`, `reopened`, an `occurred`
whose evidence **moved**, and every manual transition (`acknowledged`,
`resolved`, `accepted`) — is a distinct event the consumer should page.

## What changed from the Postgres design

The V1 Postgres store computed the suppression decision **write-side**: an
`alert_event.notify` boolean, set from a canonical `sameEvidenceJSON` comparison,
gated at a single `recordAlertEvent` dispatch point. That mechanism —
`alert_event`, `notify`, `sameEvidenceJSON`, the watermill publisher — was
**removed with Postgres** (migration step 6a-5b). It did not move into the
ledger store; it was **deliberately deferred to the consumer / the future
semantic event-log** (RFC §4.4). The trade-off: the ledger delivers more events
(one per write), and correct suppression now depends on the consumer honouring
the `occurred`/`snooze` signals above.

## Follow-ups

- **A suppressing consumer** (dedupe-on-unchanged-evidence + honour-snooze) in
  the Webhooks module or a per-recipient digest — the natural home now that
  events are generic and consumer-interpreted.
- **Semantic event types** (RFC §4.4) could re-introduce a first-class
  "materially unchanged" marker so consumers don't re-derive equality.
- **Log-volume roll-up** — collapsing identical repeats in the ledger log itself
  (vs one entry per tick) trades audit granularity for storage; deferred, tracked
  with the [audit-log direction](./workflows.md#8-audit-history--delivery--the-ledger-log).
