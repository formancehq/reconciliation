# API Reference

Reconciliation exposes two API surfaces:

- **Legacy `/policies`** — preserved verbatim for backwards compatibility. Existing customers keep working with no migration.
- **V1 Ledger Clarity** (`/rules` / `/evaluations` / `/alerts`) — the new surface. EE-gated at V1 GA.

Both share the same auth scopes (`reconciliation:read`, `reconciliation:write`) and the same `ErrorResponse` shape.

> Status: both legacy and V1 endpoints are ✅ shipped, and alert-event publication is ✅ shipped (see the **Events** section below).
> OpenAPI lives in [openapi.yaml](../../openapi.yaml).

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

Returns `200` + the `Reconciliation` row (`status: "OK" | "NOT_OK"`, balances, drift). Caller picks both PITs; both must be in the past. Each side is read point-in-time at its own timestamp — ledger via `/aggregate/balances?pit=`, payments pool via `/v3/pools/{id}/balances?at=` — so the two sides can be read a settlement cycle apart.

### `GET /reconciliations` · `GET /reconciliations/{id}`

History of previous runs.

### Known legacy quirks

- `status` is `OK` even when drift is positive (legacy convention — only negative drift flags). Tracked in [v1-vs-legacy.md §4](./v1-vs-legacy.md#4-drift-status-logic-the-legacy-bug).
- `reconciledAtPayments` reads the pool point-in-time (`/v3/pools/{id}/balances?at=`). The one caveat: a read strictly *after* the pool's last balance movement returns empty (the balance-window tail) — supply a timestamp within the settled history, not a bleeding-edge instant. Details in [v1-vs-legacy.md §5](./v1-vs-legacy.md#5-payments-side-read).
- Ledger-side PIT + metadata filter silently returned empty when `ACCOUNT_METADATA_HISTORY: DISABLED` ([ledger#1416](https://github.com/formancehq/ledger/issues/1416)) — **fixed in ledger v2.4.11** (the version V1 targets); only older ledgers are affected.

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
  "periodType": "monthly",
  "notifications": ["wh_xyz", "email:ops@buildr.com"],
  "labels": { "team": "treasury", "env": "prod" }
}
```

Returns `201` + the rule with `id` and the derived `explanationCEL`. This is a representative expression for explainability, not the runtime program. Validation failures return `400 VALIDATION` (e.g. unknown `templateKind`, invalid spec).

`periodType` (`continuous` *(default)* · `daily` · `weekly` · `monthly`) sets **how long a reconciliation period is**: it scopes each failing fingerprint into a period, so a March break and an April break are distinct, independently-closable cases and resolving April never rewrites March. `continuous` keeps a single ongoing case per fingerprint (live monitoring). It is not how often the rule runs — that is `schedule`, and the two are independent: an hourly `schedule` with a `monthly` `periodType` is normal. The period type determines the `periodID` an alert is filed under: `monthly` yields `2026-07`. See [alert-period-model.md](./alert-period-model.md).

`periodType` was called `cadence` up to 2.4.1. Both keys are accepted on create and both are returned — on single-rule *and* list responses — so clients on either contract keep working. If both keys carry a value and the values differ, the request is rejected with 400 rather than one being chosen silently. An empty or `null` value counts as unset, not as a conflict; an explicit `periodType: ""` is rejected as an invalid enum value. `cadence` is deprecated and goes away at the next API major — prefer `periodType`.

Rollout ordering is server-first, and inherently so: a client learns `periodType` exists from an SDK regenerated against the 2.4.2 spec, which does not exist until 2.4.2 ships. A pre-2.4.2 server ignores `periodType` and falls back to `continuous`, so a client that somehow sent it at a mid-upgrade fleet would get an unbucketed rule. Anything needing determinism during that window can send both keys with equal values — accepted on every version. That is deliberately not mandated: making `cadence` load-bearing for correctness would mean it could never be removed.

See [templates.md](./templates.md) for per-template spec schemas.

#### `GET /rules` — cursor-paginated list

Filtered via the **query builder** — a JSON expression sent as the request body *or* the URL-encoded `?query=` parameter (not flat `?field=value` params). Supported keys:

- `id`, `name`, `templateKind`, `enabled` — `$match` (equality) only
- `createdAt`, `updatedAt` — comparison operators (`$gt` / `$gte` / `$lt` / `$lte`)

Example: `{"$match":{"templateKind":"ledger_invariant"}}`, or composed with `$and`/`$or`, e.g. `{"$and":[{"$match":{"enabled":true}},{"$gte":{"createdAt":"2026-06-01T00:00:00Z"}}]}`. Any other key returns `400 VALIDATION`. Note there is **no** `ledger` or `label.*` filter — `ledger` lives inside `templateSpec` and labels aren't indexed for query; filter by `templateKind` / `name` (or client-side) instead.

#### `GET /rules/{id}` — fetch one

#### `PATCH /rules/{id}` — partial update

Toggle `enabled`, change `severity`, edit `schedule`, replace `notifications` / `labels`. `templateSpec` edits are re-validated and re-derive `explanationCEL`; a concurrent revision conflict is rejected with `409 RULE_CHANGED`.

#### `DELETE /rules/{id}` — cascade

Drops the rule and (via FK) all its evaluations, alerts, and alert events.

### Evaluations

#### `POST /rules/{id}/evaluate` — force-run

```json
{
  "at":           "2026-06-17T15:00:00Z",
  "safetyMargin": "30s",
  "sourcePITs": {
    "ledger:buildr#0": "2026-06-17T15:00:00Z",
    "pool:0eb4a31f-…#0": "2026-06-17T14:30:00Z"
  }
}
```

`at` defaults to now unless `sourcePITs` is supplied; a per-source replay requires an explicit `at`, which is the canonical PIT used to scope daily/monthly alerts. `safetyMargin` defaults to `30s` (send `"0s"` to read exactly at `at` — e.g. deterministic tests / demos). The margin applies to `at`, not to `sourcePITs`, whose values are already-effective replay instants. A negative `safetyMargin` is rejected with `400`; every timestamp (`at` and each `sourcePITs` value) must be in the past.

`sourcePITs` overrides individual sources at exactly the supplied effective instant, keyed by the stable source key echoed back in `pitPerSource` (`"<label>#<idx>"`). This is the two-independent-timestamps contract — read the ledger at one instant and the payments pool at another to absorb inter-system settlement lag — generalised to any multi-source template (including two terms on the same ledger, addressed by their distinct `#idx`). The required `at` remains the canonical evaluation PIT for alert-period scoping, while each override controls only its source read. A supplied `at` (or a per-source override) reads the payments pool point-in-time; the as-of-now default reads its latest snapshot (a PIT read at ~now hits the empty balance-window tail). A `sourcePITs` key that names no source of the rule's template is rejected with `400` (not silently ignored).

For historical reads, `pitPerSource` can be replayed exactly through `sourcePITs`. Payments' `latest` response does not expose its snapshot timestamp, so that path records the successful observation time instead. The evidence is frozen, but replaying that observation time through the historical endpoint is not guaranteed to reconstruct the same latest snapshot.

Returns `200` + the evaluation record:

```json
{
  "id":           "ev_…",
  "ruleID":       "rul_…",
  "startedAt":    "…",
  "endedAt":      "…",
  "result":       "PASS" | "FAIL" | "ERROR",
  "pitPerSource": { "ledger:buildr#0": "…", "pool:0eb4a31f-…#0": "…" },
  "evidence":     [ { "fingerprint": "asset:USD/2", "passed": true, "proof": { "ledger": "523500", "pool": "-523500" } }, { "fingerprint": "asset:EUR/2", "passed": false, "evidence": {…} } ],
  "costUnits":    12,
  "error":        ""
}
```

`evidence` is the per-fingerprint roster — passing *and* failing — but the two verdicts store different weight:

- **PASS** → a compact, self-contained `proof`: the observed balances plus the predicate inputs needed to verify the historical verdict after the rule changes:
  - `account_threshold`: `{ "balance": "523500", "min": "500000", "max": "600000" }` (unset bounds are omitted).
  - `ledger_vs_pool_drift`: `{ "ledger": "523500", "ledgerSign": "1", "pool": "-523500", "tolerance": "0" }`.
  - `source_parity`: `{ "left": "100", "right": "100", "tolerance": "0" }`.
  - `ledger_invariant`: `{ "positive": "5000", "negative": "-5000", "tolerance": "0" }`.

  These reuse the balance keys the FAIL `evidence` uses and retain only the inputs to the predicate — no compiled CEL or derived/redundant fields.
- **FAIL** → the full `evidence` breakdown (balances, drift, tolerance, compiled CEL), the same shape carried on the alert.

An evaluation that produced no outcomes (no assets matched either side) has `"evidence": []`. Per-fingerprint *failing* detail also lives, with full lifecycle, on the alerts.

> Reproducibility note: the PASS `proof` freezes the observed balances, and `pitPerSource` records the instant each source was read — so a green run is provable both by its frozen proof and by replaying the PITs. Replay reconstructs ledger-source balances faithfully; the one gap is Payments `latest` (no snapshot timestamp — see the caveat above), which the stored proof covers.

#### `GET /evaluations` — cursor-paginated list

Ordered by `created_at DESC`. Filtered via the **query builder** (JSON body or URL-encoded `?query=`, as for `GET /rules`). Supported keys:

- `id`, `result`, `ruleID` — `$match` (equality) only — e.g. a rule's history with `{"$match":{"ruleID":"…"}}`, or just failures with `{"$match":{"result":"FAIL"}}`
- `createdAt`, `startedAt`, `endedAt` — comparison operators (`$gt` / `$gte` / `$lt` / `$lte`)

#### `GET /evaluations/{evaluationID}` — fetch one

### Alerts

An **Alert** is the stable, dedup'd entity for one `(rule, fingerprint, period)` triple — at most one row per triple. Within a period, reopens after RESOLVED flip status back to OPEN **in place** (same id); the same fingerprint failing in a *new* period is a fresh case (new id). For a rule with `periodType: continuous` there is a single ongoing period, so it behaves as one immortal case per `(rule, fingerprint)`. The full transition history lives in `alert_event` and is exposed at `GET /alerts/{id}/events`. See [alert-period-model.md](./alert-period-model.md).

#### `GET /alerts` — list

Filtered via the **query builder** (JSON body or URL-encoded `?query=`, as for `GET /rules`). Supported keys:

- `id`, `status`, `severity`, `fingerprint`, `ruleID`, `periodID` — `$match` (equality) only
- `firstSeenAt`, `lastSeenAt` — comparison operators (`$gt` / `$gte` / `$lt` / `$lte`)

Example: `{"$match":{"status":"OPEN"}}`, or `{"$and":[{"$match":{"periodID":"2026-03"}},{"$match":{"status":"OPEN"}}]}`. Any other key returns `400 VALIDATION`. Filtering by `periodID` answers "is this period reconciled?" — a period with no OPEN/ACKNOWLEDGED alerts is green.

#### `GET /alerts/{id}` — fetch

```json
{
  "id":               "alr_…",
  "ruleID":           "rul_…",
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

`occurrenceCount` is the count of FAIL events on this alert across its reopen cycles **within its period** (for a rule with `periodType: continuous`, that's the lifetime count, since there is one unbounded period). Finer per-episode counts can be derived from `/events`.

#### `GET /alerts/{id}/events` — append-only timeline

Returns a page of the events recorded for this alert: every evaluation that touched it plus every manual transition. Most-recent-first, **cursor-paginated** (`?pageSize=`, `?cursor=`) like the other list endpoints. Pagination is required, not optional: a long-lived alert (a rule with `periodType: continuous`, or an `engine.error` meta-alert) accumulates one event row per failing evaluation indefinitely — notification suppression keeps those rows off the bus but **not** out of the table — so the timeline is unbounded.

```json
{
  "cursor": {
    "pageSize": 15,
    "hasMore": true,
    "next": "…",
    "data": [
      {
        "id":         "evt_…",
        "alertID":    "alr_…",
        "evaluationID": "ev_…",
        "type":       "fail",
        "prevStatus": "RESOLVED",
        "newStatus":  "OPEN",
        "payload":    { "asset": "USD/2", "drift": "75", … },
        "at":         "2026-06-21T08:42:00Z",
        "isReopen":   true,
        "notify":     true
      }
    ]
  }
}
```

`type` is one of `fail` / `pass` / `ack` / `resolve` / `accept` / `snooze` / `unsnooze`. `prevStatus` is `null` only for the alert's inaugural event. `isReopen` is a derived boolean — true when a `fail` lands on a previously-RESOLVED alert.

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
  "by":   "treasurer@buildr.com",
  "note": "Settlement lag on GBP corridor — confirmed by treasury."
}
```

Note is **required**. Evidence at acceptance time is frozen onto `resolution.evidenceSnapshot`. A subsequent failing evaluation reopens the alert in place (same id) — the prior resolution is preserved as an `alert_event` row, the alert row's current `resolution` is cleared.

#### `POST /alerts/{id}/snooze` — mute notifications until a future instant

```json
{ "by": "ops@buildr.com", "until": "2026-06-25T18:00:00Z", "note": "migration in flight" }
```

Mutes the alert's notifications until `until` (which must be in the future). The alert keeps failing, keeps its status, and **keeps counting against period-green** — only its webhooks go quiet, even if the discrepancy moves. The first failing evaluation at or after `until` clears the snooze and notifies once. Re-snoozing overwrites the window; resolving the alert clears it. Rejects a RESOLVED alert and a non-future `until`. The current snooze is exposed on the alert as `snooze`. See [notification-suppression.md](./notification-suppression.md) and [workflows.md §5](./workflows.md).

#### `POST /alerts/{id}/unsnooze` — lift a snooze early

```json
{ "by": "ops@buildr.com" }
```

Clears an active snooze before its window elapses. Idempotent — unsnoozing an alert that isn't snoozed returns it unchanged and emits no event.

---

## Events (✅ implemented)

Every *notifying* alert state transition publishes one message to the Formance
message bus (go-libs/v5 `messagingfx`), consumed by the **Webhooks** module
exactly like `ledger.*` / `payments.*` events. One `alert_event` row with
`notify = true` ⇒ one outbound message: emission is hooked at the single dispatch
point (`recordAlertEvent`) and sent **after the transaction commits**, so a
rolled-back evaluation emits nothing.

A repeated failing evaluation of an already-`OPEN` alert that carries
**materially-identical evidence** is recorded (`notify = false`) but **not
published** — see [notification-suppression.md](./notification-suppression.md).
This keeps a scheduled, still-broken rule from re-paging on every tick.

| Event | Fires when | `alert_event` row |
|---|---|---|
| `reconciliation.opened_alert`       | First failing eval for a fingerprint | `fail`, `prevStatus = null` |
| `reconciliation.updated_alert`      | Failure while OPEN/ACKNOWLEDGED **with new evidence** (identical repeats are suppressed) | `fail`, `prevStatus ∈ {OPEN, ACKNOWLEDGED}`, `notify = true` |
| `reconciliation.acknowledged_alert` | Human ack'd | `ack` |
| `reconciliation.resolved_alert`     | Auto-resolve (`pass`) or `fixed_by_booking` (`resolve`) | `pass` / `resolve` |
| `reconciliation.accepted_alert`     | Business acceptance | `accept` |
| `reconciliation.reopened_alert`     | Same fingerprint fails after a closed alert (status → OPEN, same alert id) | `fail`, `prevStatus = RESOLVED` |
| `reconciliation.snoozed_alert`      | Operator muted the alert until a future instant | `snooze` |
| `reconciliation.unsnoozed_alert`    | Snooze lifted early | `unsnooze` |

The event name is a pure function of the row (`events.EventTypeFor`) — there is
no separate event-kind column to keep in sync. An idempotent no-op (e.g. re-ack
of an already-acknowledged alert, or unsnoozing an alert that isn't snoozed)
writes no row and therefore emits no event. A failing evaluation that repeats
materially-identical evidence on an already-OPEN alert, or fails while an active
snooze is in force, writes a row with `notify = false` and is **not** published
— see [notification-suppression.md](./notification-suppression.md).

**Envelope.** Standard `publish.EventMessage`: `app = "reconciliation"`,
`version = "v1"`, `type ∈ {OPENED_ALERT, UPDATED_ALERT, ACKNOWLEDGED_ALERT,
RESOLVED_ALERT, ACCEPTED_ALERT, REOPENED_ALERT, SNOOZED_ALERT,
UNSNOOZED_ALERT}`, `idempotencyKey =` the `alert_event` id. The Webhooks worker
lowercases `app` and `type`, then joins them with `.`, so subscribers match
against the full names in the table above. All events publish to a single
logical topic (`reconciliation`); the operator's `--publisher-topic-mapping`
routes it to the bus subject Webhooks subscribes to.

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
| 400 | `VALIDATION`              | Unknown templateKind, invalid spec, unsupported query key/operator, or accept without a note |
| 400 | `MISSING_OR_INVALID_BODY`| Request body missing or not decodable JSON |
| 400 | `INVALID_ID`             | Path UUID malformed |
| 401 | `UNAUTHORIZED`           | Missing/invalid token |
| 403 | `FORBIDDEN`              | Token lacks the required scope (enforced at the gateway) |
| 404 | `NOT_FOUND`              | Resource doesn't exist (incl. resolve/accept on an already-resolved alert) |
| 409 | `RULE_BUSY`              | An evaluation of this rule is already in progress (lock contention) |
| 409 | `RULE_CHANGED`           | Rule revised or disabled during evaluation, or a PATCH lost a revision race |
| 500 | `INTERNAL`               | Engine error, resolver timeout — also raises an `engine.error` meta-alert |
