# API Reference

Reconciliation exposes one API surface:

- **V1 Ledger Clarity** (`/rules` / `/alerts`) — EE-gated at V1 GA.

It uses the auth scopes (`reconciliation:read`, `reconciliation:write`) and the `ErrorResponse` shape described below.

> Status: the V1 endpoints are ✅ shipped; alert-transition events are delivered via the ledger's
> native events sink (see [Events](#events) below). Evaluations are **non-durable** — there is no
> evaluations read surface. OpenAPI lives in [openapi.yaml](../../openapi.yaml).

---

## V1 Ledger Clarity (✅ shipped)

EE-gated. The contracts below match what's wired in [`internal/api/router.go`](../../internal/api/router.go) and exposed via [`openapi.yaml`](../../openapi.yaml). Underlying models live in [models/rule.go](../../internal/models/rule.go), [models/evaluation.go](../../internal/models/evaluation.go), [models/alert.go](../../internal/models/alert.go).

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

#### `DELETE /rules/{id}`

Removes the rule (its `rule:{id}` account metadata). Alerts already raised persist as their own
accounts — there is no relational cascade.

### Evaluations

#### `POST /rules/{id}/evaluate` — force-run

```json
{
  "at": "2026-06-17T15:00:00Z"
}
```

`at` is optional (defaults to now) — the nominal instant used to derive the reconciliation period
and as the Tier-2 (pool) audit timestamp. Ledger sources are read at a **query checkpoint** the
service pins for the run, not at `at` (ADR-002); there is no `safetyMargin` (a checkpoint is an
atomic cut). Returns `200` + the evaluation result (not persisted — see the status note):

```json
{
  "id":           "ev_…",
  "ruleId":       "rul_…",
  "startedAt":    "…",
  "endedAt":      "…",
  "result":       "PASS" | "FAIL" | "ERROR",
  "pitPerSource": { "payments_pool:0": "…" },
  "evidence":     [ { "fingerprint": "asset:USD/2", "passed": false, "evidence": {…} }, … ],
  "costUnits":    0,
  "error":        ""
}
```

`pitPerSource` records the audit PIT for **Tier-2 (pool) sources only** — a ledger↔ledger rule is
anchored by the shared checkpoint, so its map is empty (ADR-002 §10.1). `evidence` records **only
the failing fingerprints** (an all-`PASS` evaluation has `"evidence": []`); the durable copy of a
break's evidence lives on the alert. There is **no evaluations history endpoint** — an evaluation
is a deterministic projection, not a durable entity (RFC §4.4.2).

### Alerts

An **Alert** is the stable, dedup'd entity for one `(rule, fingerprint, period)` triple — at most one alert per triple. Within a period, reopens after RESOLVED flip status back to OPEN **in place** (same id); the same fingerprint failing in a *new* period is a fresh case (new id). For a `continuous`-cadence rule there is a single ongoing period, so it behaves as one immortal case per `(rule, fingerprint)`. The transition history is the control-ledger's append-only log (see [Events](#events)). See [alert-period-model.md](./alert-period-model.md).

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

#### `GET /alerts/{id}/events` — append-only timeline (⏳ deferred)

**Returns an empty page today.** The transition history exists — it is the control-ledger's ordered
log, and each transition carries a self-describing `last_transition` envelope (see [Events](#events))
— but a *paginated per-alert* read needs a downstream queryable sink (ClickHouse/Databricks): the
ledger log has no per-account filter, so per-request replay of the whole `_recon` log is not viable
(RFC §10). The endpoint is wired and returns the cursor shape below with `data: []` until that sink
lands.

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

Note is **required**. Evidence at acceptance time is frozen onto `resolution.evidenceSnapshot`. If `expiresAt` is set and the rule still fails at expiry, the alert reopens in place (same id) — the prior resolution is preserved in the ledger log, the item's current `resolution` is cleared.

#### `POST /alerts/{id}/snooze` — mute notifications until a future instant

```json
{ "by": "ops@buildr.com", "until": "2026-06-25T18:00:00Z", "note": "migration in flight" }
```

Records a mute intent until `until` (which must be in the future): the alert keeps failing, keeps its status, and **keeps counting against period-green**. The `snooze` metadata + a `snoozed` transition are written; a consumer honours the window — recon no longer gates delivery itself (see [notification-suppression.md](./notification-suppression.md) and [workflows.md §5](./workflows.md)). Re-snoozing overwrites the window; resolving the alert clears it. Rejects a RESOLVED alert and a non-future `until`. The current snooze is exposed on the alert as `snooze`.

#### `POST /alerts/{id}/unsnooze` — lift a snooze early

```json
{ "by": "ops@buildr.com" }
```

Clears an active snooze before its window elapses. Idempotent — unsnoozing an alert that isn't snoozed returns it unchanged and emits no event.

---

<a id="events"></a>
## Events

Reconciliation runs **no message bus of its own**. Every alert transition writes a self-describing
`last_transition` envelope into the `alert:item` metadata in the same atomic batch as the state
change, so the control-ledger's log entry for that write carries "what happened":
`COMMITTED_TRANSACTION` for lifecycle moves (open/ack/resolve/accept/auto-resolve),
`SAVED_METADATA`/`DELETED_METADATA` for snooze/unsnooze.

Delivery is the **ledger's native events sink**. When the operator sets `--events-sink-url`,
reconciliation provisions an HTTP webhook sink at boot (name `reconciliation`, event types
`[COMMITTED_TRANSACTION, SAVED_METADATA, DELETED_METADATA]`, optional `--events-sink-secret` for the
`X-Webhook-Signature` HMAC). The ledger delivers each matching committed log entry to the endpoint
(e.g. the Webhooks module). Sink filtering is by event *type*, not ledger, so consumers filter on
`event.ledger == _recon`.

| Transition (`type`) | Fires when |
|---|---|
| `reconciliation.alert.opened`        | first failing eval for a fingerprint |
| `reconciliation.alert.occurred`      | repeat failure while OPEN/ACK (OCC bump) |
| `reconciliation.alert.reopened`      | same fingerprint fails after RESOLVED (status → OPEN) |
| `reconciliation.alert.acknowledged`  | human ack'd |
| `reconciliation.alert.resolved`      | `fixed_by_booking` closure |
| `reconciliation.alert.accepted`      | business acceptance |
| `reconciliation.alert.auto_resolved` | next eval passes (system close) |
| `reconciliation.alert.snoozed` / `.unsnoozed` | operator mute / lift |

**Envelope** (the `last_transition` metadata value, carried in the log entry's `account_metadata`
or `saved_metadata` payload):

```json
{
  "type": "reconciliation.alert.resolved",
  "subject": "alert:{ruleID}:{fingerprint}",
  "alertID": "…",
  "prevStatus": "ACKNOWLEDGED",
  "newStatus": "RESOLVED",
  "occurredAt": "2026-06-22T10:00:00Z",
  "correlationID": "{evaluationID}",
  "payload": { "resolution": { "kind": "fixed_by_booking", "by": "…", "note": "…" } }
}
```

`correlationID` is the evaluation id for eval-driven transitions (opened/occurred/reopened/
auto_resolved), empty for operator actions. **No recon-side notify gate** — the ledger emits an
event per committed write; repeat-suppression and snooze *muting* are a **consumer** concern (the
Webhooks module / a future digest), driven by the `occurred` bumps and the `snooze` metadata (see
[notification-suppression.md](./notification-suppression.md)). This is a deliberate deferral (RFC §4.4);
the write-side `notify` flag and the watermill publisher were removed with Postgres.

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
| 409 | `CONFLICT`           | Concurrent transition lost the ledger marker guard (compare-and-swap); low-concurrency control-plane, rare (see F22 in the migration log) |
| 422 | `BUSINESS_RULE`      | E.g. accept-without-note, resolve-on-already-resolved |
| 500 | `INTERNAL`           | Engine error, resolver timeout — also raises an `engine.error` meta-alert |
