# Reconciliation v3 alert events

Status: **draft**, decided in RFC 0022 "Reconciliation v3 on Ledger v3" (formancehq/internal-rfcs#37). No code publishes these events yet. The scheduler and events slice (S13, EN-2763) implements them, and the Webhooks module consumes them. The payload schema is [alert-event.schema.json](./alert-event.schema.json).

## Envelope

Reconciliation publishes on the Stack broker through the go-libs publisher, on the logical topic `reconciliation`, like v2.5.0. Every message uses the go-libs `publish.EventMessage` envelope:

```json
{
  "idempotency_key": "reconciliation:4182",
  "date": "2026-10-08T14:05:12Z",
  "app": "reconciliation",
  "version": "v3",
  "type": "OPENED_ALERT",
  "payload": { "alert": {}, "rule": {}, "transition": {} }
}
```

- `version` is `v3`, the module major, as Ledger does (`v2` for Ledger v2, `v3` for Ledger v3). v2.5.0 published `v1`.
- `idempotency_key` is `<control ledger name>:<transaction id>`, the control ledger transaction that recorded the transition. It is the deduplication key.
- Webhooks lowercases `app` and `type` and joins them: subscribers match `reconciliation.opened_alert`.

## Event types

The types are the v2.5.0 ones, so existing webhook subscriptions keep matching.

| Type | Webhook name | Published when | `transition.kind` |
|---|---|---|---|
| `OPENED_ALERT` | `reconciliation.opened_alert` | An evaluation fails a check that has no live alert | `opened` |
| `UPDATED_ALERT` | `reconciliation.updated_alert` | A later evaluation fails again and the decision evidence changed: amount or sign of the gap | `updated` |
| `ACKNOWLEDGED_ALERT` | `reconciliation.acknowledged_alert` | An operator acknowledges an `OPEN` alert | `acknowledged` |
| `RESOLVED_ALERT` | `reconciliation.resolved_alert` | A passing evaluation (`reason: auto`) or an operator (`reason: fixed_by_booking`) closes the alert | `resolved` |
| `ACCEPTED_ALERT` | `reconciliation.accepted_alert` | An operator accepts the discrepancy | `accepted` |
| `REOPENED_ALERT` | `reconciliation.reopened_alert` | A closed alert fails again: `new_failure` after a resolution, `evidence_changed` or `acceptance_expired` after an acceptance | `reopened` |

Not published in `v3.0.0`:

- **A repeat failure with unchanged evidence.** It updates `lastSeenAt` and `occurrenceCount` with a minimal write, and does not notify.
- **`SNOOZED_ALERT` and `UNSNOOZED_ALERT`.** They come back with snooze, in a later v3 minor. From then on, a snoozed alert does not publish `UPDATED_ALERT`.

**Meta-alerts** use the same types. Their `alert.fingerprint` is `evaluation.failed` (an evaluation could not read, evaluate or write) or `alert.cap` (an evaluation produced more transitions than one batch allows). A consumer that only wants business discrepancies filters them out by fingerprint.

## Delivery

- **After the commit.** An event is published only once its transition is committed in the control ledger. For an evaluation, that is the same atomic batch as its capture.
- **At least once.** A catch-up loop republishes from a cursor stored in the control ledger, so a crash between commit and publish delays an event and never loses it. A consumer deduplicates on `idempotency_key`.
- **Ordered per alert by `transition.alertVersion`.** Two events of one alert may arrive out of order after a retry. A consumer applies an event only when its `alertVersion` is greater than the last one it applied for that alert.
- **No ledger events sink.** Reconciliation no longer asks the Ledger to deliver its events (Jira EN-2724).

## Payload

The payload carries three objects, so a consumer can act without an API call:

- `alert`: the alert right after the transition, as `GET /alerts/{alertID}` returns it in the draft contract.
- `rule`: the rule's ID, name, template kind, severity and labels.
- `transition`: what happened. Its kind, the statuses before and after, the time, the alert version, the control ledger transaction, and, depending on the kind, the evaluation, the operator or the reason.

Example `RESOLVED_ALERT` payload, after a corrective booking:

```json
{
  "alert": {
    "id": "6f0d6c1e-2a4b-4c8e-9f51-0b7c2d3e4f50",
    "version": 3,
    "ruleID": "0b5e2f7a-1c3d-4e5f-8a9b-1c2d3e4f5a6b",
    "fingerprint": "asset:USD/2",
    "periodID": "2026-10",
    "status": "RESOLVED",
    "severity": "high",
    "firstSeenAt": "2026-10-08T09:00:00Z",
    "lastSeenAt": "2026-10-08T13:00:00Z",
    "occurrenceCount": 5,
    "lastEvaluationID": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d",
    "resolution": {
      "kind": "fixed_by_booking",
      "by": "ops@example.com",
      "at": "2026-10-08T14:05:12Z",
      "transactionRefs": ["fix-2026-10-08-001"]
    },
    "labels": { "team": "treasury" },
    "createdAt": "2026-10-08T09:00:00Z",
    "updatedAt": "2026-10-08T14:05:12Z"
  },
  "rule": {
    "id": "0b5e2f7a-1c3d-4e5f-8a9b-1c2d3e4f5a6b",
    "name": "Client funds equal the safeguarding account",
    "templateKind": "balance_equation",
    "severity": "high",
    "labels": { "team": "treasury" }
  },
  "transition": {
    "kind": "resolved",
    "from": "ACKNOWLEDGED",
    "to": "RESOLVED",
    "at": "2026-10-08T14:05:12Z",
    "alertVersion": 3,
    "transactionID": 4182,
    "by": "ops@example.com",
    "reason": "fixed_by_booking"
  }
}
```

## Changes from v2.5.0

| | v2.5.0 | v3 |
|---|---|---|
| Envelope `version` | `v1` | `v3` |
| Payload | `{alert, event}`, with the v2 alert event row | `{alert, rule, transition}` |
| `idempotency_key` | The alert event row ID | `<control ledger>:<transaction id>` |
| Snooze events | Published | Back with snooze, in a later v3 minor |
| Repeat failure | `UPDATED_ALERT` on every failing run | `UPDATED_ALERT` only when the decision evidence changes |

When S13 implements these events, the schema is generated from the Go types into `docs/events/`, as Ledger does with `docs events --write-dir docs/events`, and this draft moves there.
