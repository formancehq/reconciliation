# API Reference

Reconciliation exposes two API surfaces:

- **Legacy `/policies`** — preserved verbatim for backwards compatibility. Existing customers keep working with no migration.
- **V1 Ledger Clarity** (`/rules` / `/evaluations` / `/incidents`) — the new surface. EE-gated at V1 GA.

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

EE-gated. Same auth surface as the legacy API. The contracts below match what's wired in [`internal/api/router.go`](../../internal/api/router.go) and exposed via [`openapi.yaml`](../../openapi.yaml). Underlying models live in [models/rule.go](../../internal/models/rule.go), [models/evaluation.go](../../internal/models/evaluation.go), [models/incident.go](../../internal/models/incident.go).

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
  "notifications": ["wh_xyz", "email:ops@buildr.com"],
  "labels": { "team": "treasury", "env": "prod" }
}
```

Returns `201` + the rule with `id` and the derived `compiledCEL` for explainability. Validation failures return `400 VALIDATION` (e.g. unknown `templateKind`, invalid spec).

See [templates.md](./templates.md) for per-template spec schemas.

#### `GET /rules` — cursor-paginated list

Filterable via query builder: `?type=ledger_invariant`, `?ledger=buildr`, `?enabled=true`, `?label.team=treasury`.

#### `GET /rules/{id}` — fetch one

#### `PATCH /rules/{id}` — partial update

Toggle `enabled`, change `severity`, edit `schedule`, replace `notifications` / `labels`. `templateSpec` edits require re-validation; the API rejects changes that would invalidate active incidents.

#### `DELETE /rules/{id}` — cascade

Drops the rule and (via FK) all its evaluations and incidents.

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

### Incidents

#### `GET /incidents` — list

Filterable: `?status=open`, `?ruleId=…`, `?severity=high`, `?since=2026-06-01T00:00:00Z`.

#### `GET /incidents/{id}` — fetch

```json
{
  "id":               "inc_…",
  "ruleId":           "rul_…",
  "fingerprint":      "asset:USD/2",
  "status":           "OPEN" | "ACKNOWLEDGED" | "RESOLVED",
  "severity":         "high",
  "openedAt":         "…",
  "lastSeenAt":       "…",
  "occurrenceCount":  4,
  "firstEvaluationId":"ev_…",
  "lastEvaluationId": "ev_…",
  "evidence":         { … },
  "ack":              { "by": "…", "at": "…", "note": "…" },
  "resolution":       null,
  "parentIncidentId": null,
  "labels":           { "team": "treasury" }
}
```

#### `POST /incidents/{id}/ack`

```json
{ "by": "ops@buildr.com", "note": "investigating" }
```

Idempotent. Status transitions `OPEN → ACKNOWLEDGED`.

#### `POST /incidents/{id}/resolve`

Two body shapes — distinguished by presence of `transactionRefs`:

```json
// auto / fixed-by-booking
{ "by": "ops@buildr.com", "note": "GBP corridor caught up", "transactionRefs": ["tx_…"] }
```

Status transitions to `RESOLVED` with `resolution.kind = "fixed_by_booking"` (or `"auto"` if the system path closed it).

#### `POST /incidents/{id}/accept` — business acceptance

```json
{
  "by":        "treasurer@buildr.com",
  "note":      "Settlement lag on GBP corridor — confirmed by treasury.",
  "expiresAt": "2026-07-17T00:00:00Z"
}
```

Note is **required**. Evidence at acceptance time is frozen onto `resolution.evidenceSnapshot`. If `expiresAt` is set and the rule still fails at expiry, a *new* incident opens with `parentIncidentId` pointing at this one — flapping stays visible.

---

## Events (⏳ planned — task #8 / V1 GA)

Published to the Webhooks module — same dispatch model as other Formance events.

| Event | Fires when | Default delivery |
|---|---|---|
| `reconciliation.incident.opened`       | First failing eval for a fingerprint | ✅ webhook + email |
| `reconciliation.incident.updated`      | Subsequent failure or severity change | digest only |
| `reconciliation.incident.acknowledged` | Human ack'd | digest only |
| `reconciliation.incident.resolved`     | Auto or `fixed_by_booking` | ✅ webhook + email |
| `reconciliation.incident.accepted`     | Business acceptance | ✅ webhook + email |
| `reconciliation.incident.reopened`     | Same fingerprint fails after a closed incident | ✅ webhook + email |

Each payload carries the full `Incident` row plus the latest `Evaluation`'s `evidence`.

Email digest is owned in-module (per-recipient aggregation is awkward to push down to Webhooks). All other delivery is the customer's problem (Jira / PagerDuty / Slack via their own Webhook consumer) — see [project memory `reconciliation-v1-scope-discipline`](../../../.claude/projects/-Users-arnaud-Documents-GitHub-reconciliation/memory/reconciliation_v1_scope_discipline.md).

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
| 409 | `CONFLICT`           | Active incident exists for same fingerprint (rare; safeguard) |
| 422 | `BUSINESS_RULE`      | E.g. accept-without-note, resolve-on-already-resolved |
| 500 | `INTERNAL`           | Engine error, resolver timeout — also raises an `engine.error` meta-incident |
