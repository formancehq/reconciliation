# Archive

Historical documents of the reconciliation module. They are kept for the record and are not maintained: their content, their links and the code they cite may be out of date. Do not use them as a reference for current behavior.

Archived on 2026-10-08 by the review of the Ledger v3 prototype (section 7 of the review, epic EN-2746).

| Document | Why it is archived | Where its content lives now |
|---|---|---|
| [rfc-ledger-native-storage.md](./rfc-ledger-native-storage.md) | The RFC that moved reconciliation from Postgres to a Ledger v3 control ledger. The move is done. | [ledger-v3-storage.md](../technical/ledger-v3-storage.md), and RFC 0022 "Reconciliation v3 on Ledger v3" (formancehq/internal-rfcs#37) |
| [ledger-v3-migration-log.md](./ledger-v3-migration-log.md) | The step-by-step log of that migration, 1,700 lines. | Its open points are tracked in Jira under EN-2344 and EN-2746. |
| [adr-002-pit-consistency.md](./adr-002-pit-consistency.md) | The point-in-time consistency model. ADR-003 superseded its aligned-checkpoint model. | [ADR-003](../prd/adr-003-checkpoint-anchor-and-crosscheck.md) |
| [notification-suppression.md](./notification-suppression.md) | Suppression of repeat notifications at the consumer. | The v3 events publish `UPDATED_ALERT` only when the evidence changes: [events-v3](../drafts/events-v3/README.md) |
