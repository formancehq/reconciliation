# API Reference

Reconciliation exposes two isolated API surfaces:

- **V1 Ledger Clarity** (`/rules` / `/alerts`) — EE-gated at V1 GA.
- **V2 multi-source controls** (`/v2/rules` / `/v2/alerts`) — additive; V1 bodies are unchanged.

It uses the auth scopes (`reconciliation:read`, `reconciliation:write`) and the `ErrorResponse` shape described below.

> Status: the V1 endpoints are ✅ shipped; alert-transition events are delivered via the ledger's
> native events sink (see [Events](#events) below). Evaluations have no standalone evaluation
> resource, but their immutable captures are available from each rule's `/captures` endpoint.
> OpenAPI lives in [openapi.yaml](../../openapi.yaml).

---

<a id="v1v2-coexistence"></a>
## V1/V2 coexistence

The route selects the contract. Clients do not send `contractVersion` on create or patch:

| Contract | Rule routes | Alert routes |
|---|---|---|
| V1 | `/rules`, `/rules/{id}`, `/rules/{id}/evaluate`, `/rules/{id}/captures`, `/rules/{id}/timeline` | `/alerts`, `/alerts/{id}`, `/events`, `/ack`, `/resolve`, `/accept`, `/snooze`, `/unsnooze` |
| V2 | `/v2/rules`, `/v2/rules/{id}`, `/v2/rules/{id}/evaluate`, `/v2/rules/{id}/captures`, `/v2/rules/{id}/timeline` | `/v2/alerts`, `/v2/alerts/{id}`, `/ack`, `/resolve`, `/accept`, `/snooze`, `/unsnooze` |

`contractVersion` is persisted immutably on rules, alerts, and captures. Missing markers on legacy
records decode as version 1; new V1 writes store 1 and V2 writes store 2. V1 response bodies remain
byte-shape compatible and do not gain the field. V2 rule, alert, and capture responses expose the
required read-only value `"contractVersion": 2`.

Version isolation is enforced before pagination and lookup results are returned. V1 lists never
contain V2 resources and V2 lists never contain V1 resources. Fetching, patching, deleting,
evaluating, listing captures for, or acting on an ID through the wrong version returns `404`; it does
not disclose that the resource exists in another contract. A patch cannot change contract version.

The legacy V1 per-alert `/events` endpoint remains a deferred compatibility surface. V2 does not
duplicate that empty endpoint; V2 alert lifecycle items are available through the backed,
rule-scoped `/v2/rules/{id}/timeline` journal.

V1 `source_parity` remains binary and keeps `templateSpec.left` / `.right` plus its four legacy
evidence keys. V2 uses named `sources[]` and does not accept V1 template kinds. There is no automatic
rewrite of rules, captures, alerts, or accepted evidence snapshots. See
[ADR-004](../prd/adr-004-multi-source-comparisons.md).

V1 validation messages present those inputs as **Source A** and **Source B**, while retaining the
stable machine path separately—for example, `Source A is missing an asset (field: left.asset)`.
This is a presentation-only change: request fields, stored specifications, and evidence keys remain
unchanged.

---

## V2 multi-source controls

The lifecycle, periodType, severity, scheduling, pagination, alert actions, and auth scopes match V1;
only the versioned route, typed template catalog, and evidence contracts differ.

### Create a balance equation

`POST /v2/rules`

```json
{
  "name": "cash-plus-receivable-equals-obligation",
  "templateKind": "balance_equation",
  "templateSpec": {
    "sources": [
      { "id": "cash", "ledger": "main", "query": { "$match": { "address": "cash:*" } }, "asset": "USD/2" },
      { "id": "receivable", "ledger": "main", "query": { "$match": { "address": "receivable:*" } }, "asset": "USD/2" },
      { "id": "obligation", "ledger": "control", "query": { "$match": { "address": "liability:*" } }, "asset": "USD/2" }
    ],
    "terms": [
      { "source": "cash", "coefficient": 1 },
      { "source": "receivable", "coefficient": 1 },
      { "source": "obligation", "coefficient": -1 }
    ],
    "tolerance": "0"
  },
  "severity": "high",
  "periodType": "daily"
}
```

The `201` rule contains `contractVersion: 2` and its exact `compiledCEL`. Evaluation evidence uses
`schemaVersion: 2`, `operation: "balance_equation"`, named source values/contributions, residual,
absolute residual, tolerance, and `compiledCEL`.

### Create exchange-rate bounds

`POST /v2/rules`

```json
{
  "name": "eur-usd-valuation-band",
  "templateKind": "exchange_rate_bounds",
  "templateSpec": {
    "sources": [
      { "id": "eur", "ledger": "treasury", "query": { "$match": { "address": "position:eur" } }, "asset": "EUR/2" },
      { "id": "usd", "ledger": "treasury", "query": { "$match": { "address": "valuation:usd" } }, "asset": "USD/2" }
    ],
    "baseSource": "eur",
    "quoteSource": "usd",
    "rate": { "target": "1.10", "toleranceBps": 25 }
  }
}
```

The observed rate means quote major units per base major unit. Bounds are inclusive and evaluated by
exact CEL financial built-ins backed by `big.Int` / `big.Rat`; JSON decimal strings are never
converted to binary floating point. A zero base produces a normal failed outcome with
`undefinedReason: "base_balance_zero"`.

### Create source consensus

`POST /v2/rules`

```json
{
  "name": "usd-record-consensus",
  "templateKind": "source_consensus",
  "templateSpec": {
    "sources": [
      { "id": "subledger", "ledger": "main", "query": { "$match": { "address": "customers:total" } }, "asset": "USD/2" },
      { "id": "processor", "kind": "account_metadata", "ledger": "processor", "query": { "$match": { "address": "reported:balance" } }, "metadataKey": "reported_balance", "asset": "USD/2" },
      { "id": "bank", "kind": "account_metadata", "ledger": "bank", "query": { "$match": { "address": "statement:closing" } }, "metadataKey": "closing_balance", "asset": "USD/2" }
    ],
    "tolerance": "100"
  }
}
```

Consensus passes only when every declared source is present and the widest balance spread is within
tolerance. Evidence names the minimum and maximum sources and lists missing source IDs.

### Create portfolio coverage bounds

`POST /v2/rules`

```json
{
  "name": "liquid-reserve-coverage",
  "templateKind": "coverage_ratio_bounds",
  "templateSpec": {
    "sources": [
      { "id": "cash", "ledger": "treasury", "query": { "$match": { "address": "assets:cash:*" } }, "asset": "USD/2" },
      { "id": "securities", "ledger": "treasury", "query": { "$match": { "address": "assets:securities:*" } }, "asset": "USD/2" },
      { "id": "liabilities", "ledger": "main", "query": { "$match": { "address": "liabilities:customers:*" } }, "asset": "USD/2" }
    ],
    "numeratorTerms": [
      { "source": "cash", "coefficient": 1 },
      { "source": "securities", "coefficient": 1 }
    ],
    "denominatorTerms": [
      { "source": "liabilities", "coefficient": 1 }
    ],
    "ratio": { "min": "1.00", "max": "1.20" }
  }
}
```

Every source is assigned exactly once to a signed portfolio. Evidence preserves both totals, every
contribution, and the exact reduced ratio. A zero denominator is a typed failed outcome.

### V2 validation errors

Invalid specs return the existing `400 VALIDATION` envelope. Human wording uses the source label or
ID and retains a machine path in the detail:

```json
{
  "errorCode": "VALIDATION",
  "errorMessage": "templates: invalid spec",
  "details": "Source \"obligation\" is missing an asset (field: sources[2].asset)"
}
```

All source queries are validated before persistence. One invalid/unindexed query rejects the entire
rule; transient ledger failures remain server errors rather than being misclassified as validation.

See [templates.md](./templates.md#v2-catalog) for the complete specs, arithmetic,
fingerprints, and evidence.

`GET /v2/rules/{id}/captures` returns the same cursor envelope as V1, with
`contractVersion: 2` on each capture and the V2 evidence object preserved unchanged. V2 alert
evidence and `resolution.evidenceSnapshot` preserve that same object; they are never translated to
V1 left/right keys.

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
  "periodType": "monthly",
  "notifications": ["wh_xyz", "email:ops@buildr.com"],
  "labels": { "team": "treasury", "env": "prod" }
}
```

Returns `201` + the rule with `id` and the derived `compiledCEL` for explainability. Validation failures return `400 VALIDATION` (e.g. unknown `templateKind`, invalid spec).

`periodType` (`continuous` *(default)* · `daily` · `weekly` · `monthly`) sets **how long a reconciliation period is**: it scopes each failing fingerprint into a period, so a March break and an April break are distinct, independently-closable cases and resolving April never rewrites March. `continuous` keeps a single ongoing case per fingerprint (live monitoring). It is not how often the rule runs — that is `schedule`, and the two are independent: an hourly `schedule` with a `monthly` `periodType` is normal. The period type determines the `periodID` an alert is filed under: `monthly` yields `2026-07`. See [alert-period-model.md](./alert-period-model.md).

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

`at` is optional (defaults to now) — the nominal instant used to derive the reconciliation period.
Reconciliation is strictly **ledger↔ledger**: each source is read **live** at evaluation time (a
single aggregate is an internally consistent snapshot, ADR-003), and cross-ledger skew is absorbed by
the template's `tolerance`. Returns `200` + the evaluation result (not persisted — see the status note):

```json
{
  "id":        "ev_…",
  "ruleId":    "rul_…",
  "startedAt": "…",
  "endedAt":   "…",
  "result":    "PASS" | "FAIL" | "ERROR",
  "evidence":  [ { "fingerprint": "asset:USD/2", "passed": false, "evidence": {…} }, … ],
  "costUnits": 0,
  "error":     ""
}
```

`evidence` records every passing and failing fingerprint and the exact values used for its verdict.
This complete roster documents both breaks and successful reconciliations, including passes that do
not mutate an alert. The evaluation object itself is **not** a durable entity (a deterministic
projection, RFC §4.4.2) — but each run's
**capture** is (ADR-003), queryable via `GET /rules/{id}/captures` below.

#### `GET /rules/{id}/captures` — evaluation history (captures)

The immutable capture recorded per evaluation (ADR-003): positive assurance on a pass, evidence for
every break, and successful evidence when a pass resolves an active alert. Evidence retention is
planned per fingerprint, so an overall `FAIL` capture can contain both failing and successful
resolution evidence. Unlike the alert-transition timeline
(`/alerts/{id}/events`, sink-gated), **captures are first-class ledger transactions**, so this is
queryable **live** today — no event sink required. Cursor-paginated, most-recent-first. Optional
`?period=` scopes to one reconciliation period.

```json
{
  "cursor": {
    "pageSize": 15, "hasMore": false, "previous": "", "next": "",
    "data": [
      {
        "transactionID": 4213,
        "ruleID":        "rul_…",
        "periodID":      "2026-03",
        "evaluationID":  "ev_…",
        "templateKind":  "source_parity",
        "verdict":       "fail",
        "trigger":       "scheduled",
        "capturedAt":    "2026-03-01T00:00:00Z",
        "evidence": {
          "asset": "USD/2",
          "leftSource": "ledger:main",
          "leftBalance": "100",
          "rightSource": "ledger:control",
          "rightBalance": "95",
          "difference": "5",
          "signedDiff": "5",
          "tolerance": 0
        }
      }
    ]
  }
}
```

`transactionID` is the ledger-local id of the underlying capture transaction. Read path: the store
lists transactions on the rule's capture bucket address (`capture:rule:{ruleId}:per:*`) — which is why
provisioning declares the transaction address index. Bounded to one rule's captures; a native
`ListTransactions` cursor is the follow-up for very high-volume continuous rules (F23-class).

### Alerts

An **Alert** is the stable, dedup'd entity for one `(rule, fingerprint, period)` triple — at most one alert per triple. Within a period, reopens after RESOLVED flip status back to OPEN **in place** (same id); the same fingerprint failing in a *new* period is a fresh case (new id). For a rule with `periodType: continuous` there is a single ongoing period, so it behaves as one immortal case per `(rule, fingerprint)`. The transition history is the control-ledger's append-only log (see [Events](#events)). See [alert-period-model.md](./alert-period-model.md).

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

`occurrenceCount` is the count of FAIL events on this alert across its reopen cycles **within its period** (for a rule with `periodType: continuous`, that's the lifetime count, since there is one unbounded period). Finer per-episode counts can be derived from `/events`.

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

### Rule timeline

`GET /rules/{ruleID}/timeline` and `GET /v2/rules/{ruleID}/timeline` return one
newest-first, cursor-paginated history containing rule revisions, evaluation
observations, and alert lifecycle activity. Every item uses a common envelope:

```json
{
  "id": "8941:0",
  "sequence": "8941",
  "kind": "evaluation.completed",
  "category": "evaluation",
  "ruleID": "…",
  "contractVersion": 2,
  "ruleRevision": "sha256:…",
  "correlationID": "evaluation UUID",
  "occurredAt": "2026-07-18T10:30:00Z",
  "recordedAt": "2026-07-18T10:30:00.042Z",
  "payload": {}
}
```

Read the current rule with `GET /rules/{id}` (or `/v2/rules/{id}`) and page the
timeline independently below it. `revision` on the current rule and
`ruleRevision` on an evaluation identify the exact effective configuration.
The journal is forward-only: pre-rollout state is not presented as invented
lifecycle activity.

Reconciliation runs **no message bus of its own**. Every alert transition writes a self-describing
`last_transition` envelope into the `alert:item` metadata in the same atomic batch as the state
change, so the control-ledger's log entry for that write carries "what happened":
`COMMITTED_TRANSACTION` for lifecycle moves and status-neutral snooze/unsnooze interactions.

Delivery is the **ledger's native events sink**. When the operator sets `--events-sink-url`,
reconciliation provisions an HTTP webhook sink at boot (name `reconciliation`, event types
`[COMMITTED_TRANSACTION, SAVED_METADATA, DELETED_METADATA]`, optional `--events-sink-secret` for the
`X-Webhook-Signature` HMAC). The ledger delivers each matching committed log entry to the endpoint
(e.g. the Webhooks module). The metadata types remain subscribed for compatibility with older or
direct metadata writers. Sink filtering is by event *type*, not ledger, so consumers filter on the
configured control-ledger name (default `reconciliation`).

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

The events sink remains delivery infrastructure. Product history reads use the
rule-scoped activity account and do not scan the global Ledger log.

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

An evaluation that would open more new alerts than the service permits succeeds normally (200) but
withholds its alert transitions and raises an `alert.cap` meta-alert instead — see
[workflows.md §6b](./workflows.md).
