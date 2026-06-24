# API Reference

Reconciliation exposes two API surfaces:

- **Legacy `/policies`** — preserved verbatim for backwards compatibility. Existing customers keep working with no migration.
- **V1 Ledger Clarity** (`/rules` / `/evaluations` / `/alerts`) — the new surface. EE-gated at V1 GA.

Both share the same auth scopes (`reconciliation:read`, `reconciliation:write`) and the same `ErrorResponse` shape.

> Status: both legacy and V1 endpoints are ✅ shipped. Event publication remains ⏳ (task #8).
> OpenAPI lives in [openapi.yaml](../../openapi.yaml) — 16 paths, ~30 schemas.

---

## Legacy `/policies` (✅ shipped)

Backed by the `policy` and `reconciliation` tables. At task #6, these become a thin facade over the `Rule` table — wire shape unchanged.

### `POST /policies`

Create a policy.

```json
{
  "name": "buildr-pool",
  "ledgerName": "buildr",
  "ledgerQuery": { "$match": { "metadata[trust]": "true" } },
  "paymentsPoolID": "0eb4a31f-751e-42d4-8d5b-2129e6d4cf4c"
}
```

Returns `201` + the policy with a generated `id`.

### `GET /policies` · `GET /policies/{id}` · `DELETE /policies/{id}`

Cursor-paginated list, get-by-id, delete. Delete cascades to associated reconciliation rows.

### `POST /policies/{id}/reconciliation`

Run a reconciliation **now** against the supplied PITs.

```json
{
  "reconciledAtLedger":   "2026-06-17T15:00:00Z",
  "reconciledAtPayments": "2026-06-17T15:00:00Z"
}
```

Returns `200` + the `Reconciliation` row (`status: "OK" | "NOT_OK"`, balances, drift). Caller picks both PITs; both must be in the past.

### `GET /reconciliations` · `GET /reconciliations/{id}`

History of previous runs.

### Known legacy quirks

- `status` is `OK` even when drift is positive (legacy convention — only negative drift flags). Tracked in [v1-vs-legacy.md §4](./v1-vs-legacy.md#4-drift-status-logic-the-legacy-bug).
- Payments-side PIT silently returns empty under payments v3. Tracked in [v1-vs-legacy.md §5](./v1-vs-legacy.md#5-payments-side-read).
- Ledger-side PIT + metadata filter silently returns empty when `ACCOUNT_METADATA_HISTORY: DISABLED` ([ledger#1416](https://github.com/formancehq/ledger/issues/1416)).

---

## V1 Ledger Clarity (✅ shipped)

EE-gated. Same auth surface as the legacy API. The contracts below match what's wired in [`internal/api/router.go`](../../internal/api/router.go) and exposed via [`openapi.yaml`](../../openapi.yaml). Underlying models live in [models/rule.go](../../internal/models/rule.go), [models/evaluation.go](../../internal/models/evaluation.go), [models/alert.go](../../internal/models/alert.go).

> Handler-level tests live in [v1_handlers_test.go](../../internal/api/v1_handlers_test.go); the end-to-end orchestration test ([v1_orchestration_test.go](../../internal/api/service/v1_orchestration_test.go)) is the canonical reference for the open/update/auto-resolve/re-open flow these endpoints drive.

### Rules

#### `POST /rules` — create

```json
{
  "name": "buildr-trust-integrity",
  "templateKind": "ledger_invariant",
  "templateSpec": {
    "terms": [
      { "ledger": "buildr", "query": { "$match": { "metadata[trust]": "held" } },       "sign":  1 },
      { "ledger": "buildr", "query": { "$match": { "metadata[trust]": "obligation" } }, "sign": -1 }
    ],
    "tolerance": { "USD/2": 0 }
  },
  "schedule": { "kind": "on_demand" },
  "severity": "high",
  "cadence": "monthly",
  "notifications": ["wh_xyz", "email:ops@buildr.com"],
  "labels": { "team": "treasury", "env": "prod" }
}
```

Returns `201` + the rule with `id` and the derived `compiledCEL` for explainability. Validation failures return `400 VALIDATION` (e.g. unknown `templateKind`, invalid spec).

`cadence` (`continuous` *(default)* · `daily` · `weekly` · `monthly`) sets the reconciliation rhythm: it scopes each failing fingerprint into a period, so a March break and an April break are distinct, independently-closable cases and resolving April never rewrites March. `continuous` keeps a single ongoing case per fingerprint (live monitoring). See [alert-period-model.md](./alert-period-model.md).

See [templates.md](./templates.md) for per-template spec schemas.

#### `GET /rules` — cursor-paginated list

Filterable via query builder: `?type=ledger_invariant`, `?ledger=buildr`, `?enabled=true`, `?label.team=treasury`.

#### `GET /rules/{id}` — fetch one

#### `PATCH /rules/{id}` — partial update

Toggle `enabled`, change `severity`, edit `schedule`, replace `notifications` / `labels`. `templateSpec` edits require re-validation; the API rejects changes that would invalidate active alerts.

#### `DELETE /rules/{id}` — cascade

Drops the rule and (via FK) all its evaluations, alerts, and alert events.

### Evaluations

#### `POST /rules/{id}/evaluate` — force-run

```json
{
  "reconciledAt":  "2026-06-17T15:00:00Z",
  "safetyMargin": "30s"
}
```

Returns `200` + the evaluation record:

```json
{
  "id":           "ev_…",
  "ruleId":       "rul_…",
  "startedAt":    "…",
  "endedAt":      "…",
  "result":       "PASS" | "FAIL" | "ERROR",
  "pitPerSource": { "ledger_set:0": "…", "payments_pool:0": "…" },
  "evidence":     [ { "fingerprint": "asset:USD/2", "passed": true, "evidence": {…} }, … ],
  "costUnits":    0,
  "error":        ""
}
```

#### `GET /rules/{id}/evaluations` — history

Cursor-paginated, ordered by `created_at DESC`.

### Alerts

An **Alert** is the stable, dedup'd entity for one `(rule, fingerprint, period)` triple — at most one row per triple. Within a period, reopens after RESOLVED flip status back to OPEN **in place** (same id); the same fingerprint failing in a *new* period is a fresh case (new id). For a `continuous`-cadence rule there is a single ongoing period, so it behaves as one immortal case per `(rule, fingerprint)`. The full transition history lives in `alert_event` and is exposed at `GET /alerts/{id}/events`. See [alert-period-model.md](./alert-period-model.md).

#### `GET /alerts` — list

Filterable: `?status=OPEN`, `?ruleId=…`, `?severity=high`, `?periodID=2026-03`, `?since=2026-06-01T00:00:00Z`. Filtering by `periodID` answers "is this period reconciled?" — a period with no OPEN/ACKNOWLEDGED alerts is green.

#### `GET /alerts/{id}` — fetch

```json
{
  "id":               "alr_…",
  "ruleId":           "rul_…",
  "fingerprint":      "asset:USD/2",
  "periodID":         "2026-03",
  "status":           "OPEN" | "ACKNOWLEDGED" | "RESOLVED",
  "severity":         "high",
  "firstSeenAt":      "…",
  "lastSeenAt":       "…",
  "occurrenceCount":  4,
  "lastEvaluationID": "ev_…",
  "evidence":         { … },
  "ack":              { "by": "…", "at": "…", "note": "…" },
  "resolution":       null,
  "labels":           { "team": "treasury" }
}
```

`occurrenceCount` is the count of FAIL events on this alert across its reopen cycles **within its period** (for a `continuous`-cadence rule, that's the lifetime count, since there is one unbounded period). Finer per-episode counts can be derived from `/events`.

#### `GET /alerts/{id}/events` — append-only timeline

Returns every event recorded for this alert: every evaluation that touched it plus every manual transition. Most-recent-first.

```json
[
  {
    "id":         "evt_…",
    "alertID":    "alr_…",
    "evaluationID": "ev_…",
    "type":       "fail",
    "prevStatus": "RESOLVED",
    "newStatus":  "OPEN",
    "payload":    { "asset": "USD/2", "drift": "75", … },
    "at":         "2026-06-21T08:42:00Z",
    "isReopen":   true
  }
]
```

`type` is one of `fail` / `pass` / `ack` / `resolve` / `accept`. `prevStatus` is `null` only for the alert's inaugural event. `isReopen` is a derived boolean — true when a `fail` lands on a previously-RESOLVED alert.

#### `POST /alerts/{id}/ack`

```json
{ "by": "ops@buildr.com", "note": "investigating" }
```

Idempotent. Status transitions `OPEN → ACKNOWLEDGED`.

#### `POST /alerts/{id}/resolve`

Two body shapes — distinguished by presence of `transactionRefs`:

```json
// auto / fixed-by-booking
{ "by": "ops@buildr.com", "note": "GBP corridor caught up", "transactionRefs": ["tx_…"] }
```

Status transitions to `RESOLVED` with `resolution.kind = "fixed_by_booking"` (or `"auto"` if the system path closed it).

#### `POST /alerts/{id}/accept` — business acceptance

```json
{
  "by":        "treasurer@buildr.com",
  "note":      "Settlement lag on GBP corridor — confirmed by treasury.",
  "expiresAt": "2026-07-17T00:00:00Z"
}
```

Note is **required**. Evidence at acceptance time is frozen onto `resolution.evidenceSnapshot`. If `expiresAt` is set and the rule still fails at expiry, the alert reopens in place (same id) — the prior resolution is preserved as an `alert_event` row, the alert row's current `resolution` is cleared.

---

## Events (✅ implemented)

Every alert state transition publishes one message to the Formance message bus
(go-libs/v5 `messagingfx`), consumed by the **Webhooks** module exactly like
`ledger.*` / `payments.*` events. One `alert_event` row ⇒ one outbound message:
emission is hooked at the single write point (`appendAlertEvent`) and dispatched
**after the transaction commits**, so a rolled-back evaluation emits nothing.

| Event | Fires when | `alert_event` row |
|---|---|---|
| `reconciliation.alert.opened`       | First failing eval for a fingerprint | `fail`, `prevStatus = null` |
| `reconciliation.alert.updated`      | Subsequent failure while OPEN/ACKNOWLEDGED | `fail`, `prevStatus ∈ {OPEN, ACKNOWLEDGED}` |
| `reconciliation.alert.acknowledged` | Human ack'd | `ack` |
| `reconciliation.alert.resolved`     | Auto-resolve (`pass`) or `fixed_by_booking` (`resolve`) | `pass` / `resolve` |
| `reconciliation.alert.accepted`     | Business acceptance | `accept` |
| `reconciliation.alert.reopened`     | Same fingerprint fails after a closed alert (status → OPEN, same alert id) | `fail`, `prevStatus = RESOLVED` |

The event name is a pure function of the row (`events.EventTypeFor`) — there is
no separate event-kind column to keep in sync. An idempotent no-op (e.g. re-ack
of an already-acknowledged alert) writes no row and therefore emits no event.

**Envelope.** Standard `publish.EventMessage`: `app = "reconciliation"`,
`version = "v1"`, `type ∈ {alert.opened, alert.updated, alert.acknowledged,
alert.resolved, alert.accepted, alert.reopened}`, `idempotencyKey =` the
`alert_event` id. The Webhooks worker lowercases and joins `app` + `type`, so
subscribers match against the full names in the table above. All six events
publish to a single logical topic (`reconciliation`); the operator's
`--publisher-topic-mapping` routes it to the bus subject Webhooks subscribes to.

**Payload.** The full current `Alert` row (which carries the latest evaluation's
`evidence`, plus `resolution` / `ack` / `labels`) paired with the triggering
`alert_event` row (transition `type`, `prevStatus` → `newStatus`, originating
`evaluationID`, and the resolution/ack detail in its `payload`):

```json
{
  "alert": { "id": "…", "ruleID": "…", "fingerprint": "asset:USD/2", "periodID": "2026-03", "status": "RESOLVED", "evidence": { … }, "resolution": { … }, … },
  "event": { "id": "…", "alertID": "…", "type": "resolve", "prevStatus": "ACKNOWLEDGED", "newStatus": "RESOLVED", "evaluationID": "…", "payload": { … }, "at": "2026-06-22T10:00:00Z" }
}
```

Delivery routing (which event goes to which endpoint) is configured per
subscription in the Webhooks module — every event above is published
unconditionally; the customer subscribes to what they care about and fans out to
Jira / PagerDuty / Slack via their own Webhook consumer. The per-recipient
**email digest** is owned in-module and ships separately at V1 GA (its
aggregation is awkward to push down to Webhooks).

---

## Error responses

All endpoints share the existing `ErrorResponse` shape:

```json
{ "errorCode": "VALIDATION", "errorMessage": "…", "details": "…" }
```

| HTTP | `errorCode` | When |
|---|---|---|
| 400 | `VALIDATION`         | Bad request body, unknown templateKind, invalid spec |
| 400 | `INVALID_ID`         | Path UUID malformed |
| 401 | `UNAUTHORIZED`       | Missing/invalid token |
| 403 | `FORBIDDEN`          | Token lacks the required scope |
| 404 | `NOT_FOUND`          | Resource doesn't exist |
| 409 | `CONFLICT`           | Concurrent first-open race rejected by unique constraint (the retry path catches this automatically — surfaced only when retries are exhausted) |
| 422 | `BUSINESS_RULE`      | E.g. accept-without-note, resolve-on-already-resolved |
| 500 | `INTERNAL`           | Engine error, resolver timeout — also raises an `engine.error` meta-alert |
