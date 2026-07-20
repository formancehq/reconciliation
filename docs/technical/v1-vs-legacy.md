# V1 vs Legacy `/policies`

This document describes the diff between the legacy reconciliation behaviour (what runs today against the `policy` / `reconciliation` tables and the `/policies` API) and the V1 redesign currently in flight. The legacy path is **not removed** — it's preserved as a facade (task #6) so existing customers' policies port 1:1.

> If you're new to the codebase: read this first, then [architecture.md](./architecture.md).

## Summary

| Dimension | Legacy `/policies` | V1 Ledger Clarity |
|---|---|---|
| **Entity model** | `Policy` (config) + `Reconciliation` (one-shot result) | `Rule` + `Evaluation` (every run) + `Alert` (stable per rule+fingerprint) + `AlertEvent` (append-only history) + `Resolution` (auditable closure) |
| **Surface** | Single hardcoded comparison: ledger query vs payments pool | Four typed templates over an internal CEL kernel; resolvers per `Source` kind |
| **Schedule** | Manual `POST /policies/{id}/reconciliation` | On-demand (`POST /rules/{id}/evaluate`) + PostgreSQL-backed worker scheduler |
| **Asset handling** | One pass/fail per request; "different number of assets" hard fails | Per-asset outcomes, fingerprint-dedup'd; one alert per asset |
| **Drift convention** | Implicit (`ledgerBalance + poolBalance == 0`) — only **negative** drift flags `NOT_OK` (legacy bug) | Same arithmetic; templates treat **any** drift outside tolerance as failure |
| **Tolerance** | Strict zero only | Per-asset tolerance, configurable per template |
| **Payments-side read** | `/api/payments/pools/{id}/balances?at=` — genuine point-in-time (an earlier "silently empty under v3" read was a misdiagnosis; see §5) | Point-in-time per source: `V3 /balances?at=` for an explicit past instant, `V3 /balances/latest` for as-of-now |
| **Notifications** | None (HTTP response only) | Webhook events + email digest at V1 GA |
| **Resolution** | None — every fail recomputes from scratch | Three paths: `auto` / `fixed_by_booking` / `accepted_by_business`, all audit-trailed |
| **Reopen behaviour** | N/A | Same alert row flips back to OPEN in place; lifetime `occurrence_count` keeps growing; the prior resolution is preserved in the `alert_event` log |
| **Engine errors vs data alerts** | Mixed (any failure surfaces as `status: NOT_OK` w/ error string) | Separated: data → alert; engine error → `engine.error` meta-alert on a distinct channel |
| **Auditability** | Best-effort; no PIT recorded on the result | Every `Evaluation` persists the PITs each source resolved at; resolutions are immutable |

---

## 1. Entity model: from two tables to four

### Legacy

```text
reconciliations.policy           — config only
reconciliations.reconciliation   — one row per POST /policies/{id}/reconciliation
```

### V1

```text
reconciliations.rule         — typed template + spec + schedule + severity + labels (compiled_cel for explainability)
reconciliations.evaluation   — one row per execution (PASS / FAIL / ERROR); pit_per_source + evidence
reconciliations.alert        — stable per-fingerprint record; ack + resolution + lifetime occurrence_count
reconciliations.alert_event  — append-only log: one row per evaluation that touched the alert + one per manual transition
reconciliations.policy       — preserved verbatim, backs the /policies facade
reconciliations.reconciliation — preserved verbatim
```

Legacy tables stay; the V1 tables sit beside them. The legacy `/policies` API is implemented as a thin facade over `Rule` (task #6, ⏳).

See [internal/storage/migrations/migrations.go](../../internal/storage/migrations/migrations.go) for the additive migration #4.

---

## 2. Surface: from one comparison to a typed catalog

### Legacy

Hardcoded in [internal/api/service/reconciliation.go:43](../../internal/api/service/reconciliation.go) — the only operation is `(ledger query) vs (payments pool), drift==0, per asset`.

### V1

Four typed templates ([internal/templates/](../../internal/templates/)):

- **`ledger_vs_pool_drift`** — port of the legacy semantics; same arithmetic, now with tolerance + per-asset outcomes
- **`ledger_invariant`** — sum-of-signed-balance terms ≤ tolerance (the Buildr-style trust integrity check)
- **`account_threshold`** — per-asset min/max bounds, aggregate or per-account scope
- **`source_parity`** — two balance sources (ledger or pool, either side) agree within tolerance; the generalised cross-source match, built on the shared `Source` primitive that also backs `ledger_vs_pool_drift`

Each compiles deterministically to CEL strings the internal kernel evaluates. The kernel ([internal/engine/](../../internal/engine/)) is **not** a V1 GA public surface — see [ADR-001](../prd/adr-001-cel-kernel.md).

---

## 3. Asset handling

### Legacy

```go
if len(paymentsBalances) != len(ledgerBalances) {
    res.Status = models.ReconciliationNotOK
    res.Error = "different number of assets"
    return res, nil
}
```

One reconciliation = one pass/fail. Asset count mismatch short-circuits.

### V1

- Each template discovers the asset universe at eval time (union of both sides for drift; spec keys for invariant/threshold).
- Per-asset `Outcome { Fingerprint: "asset:USD/2", Passed: bool, Evidence: {...} }`.
- One `Alert` row exists per `(rule, fingerprint, period)` triple (e.g. `asset:USD/2`). USD breaking is a separate alert from EUR breaking, so they resolve independently; and the same break in a new period is a fresh case (`period` is `continuous` for live-monitoring rules).

---

## 4. Drift status logic (the legacy bug)

### Legacy bug

[internal/api/service/reconciliation.go:148-160](../../internal/api/service/reconciliation.go):

```go
drift.Set(paymentBalance).Add(&drift, ledgerBalance)
switch drift.Cmp(big.NewInt(0)) {
case 0, 1:                 // drift == 0 OR drift > 0
    // → no error, status remains OK
default:                   // drift < 0
    err = fmt.Errorf("balance drift for asset %s", asset)
}
```

A *positive* drift (more cash held than recorded obligations) is silently OK. The convention assumes ledger balances are negative (obligations) and payments positive (cash held); any deviation in the opposite direction was a bug nobody noticed because it doesn't surface in customer tests.

### V1

Templates treat any `abs(drift) > tolerance` as failure, regardless of sign. The convention is documented in the template spec and the resulting `Evidence.signedDrift` makes the sign visible to the operator.

---

## 5. Payments-side read

### Legacy

The SDK call hits `/api/payments/pools/{id}/balances?at=…`. An earlier baseline check read `[]` here and concluded "the PIT path is not implemented under payments v3" — that diagnosis was **wrong**. Re-verified against payments v3.3.1: the route returns the pool balance **valid at `at`**, correct and historically distinct, on both the v1 (`/api/payments/pools/{id}/balances?at=`) and v3 (`/v3/pools/{id}/balances?at=`) routes.

The `[]` is real but narrower: a pool balance has a validity window `[createdAt, lastUpdatedAt]`, and the **current** balance sits as a point at the last movement until it is superseded. So a read strictly *after* the last balance movement — which is exactly what a read at ~now does — matches no window and returns `[]`, while `/balances/latest` returns the current balance unconditionally. The baseline symptom (`paymentsBalances: {}` → "zero on both sides" → `status: OK`) is consistent with a read at ~now (or past the demo data's last event), not with a missing PIT path.

### V1

Each source reads at its own point-in-time (PIT-per-source — see [ADR-002](../prd/adr-002-pit-consistency.md)). `SDKPaymentsResolver.PoolBalance` reads the pool point-in-time via `Payments.V3.GetPoolBalances` (`?at=`) when an explicit past instant is requested, and `Payments.V3.GetPoolBalancesLatest` for the "as of now" default — because a PIT read at ~now hits the empty balance-window tail described above ([internal/engine/sdk_resolvers.go](../../internal/engine/sdk_resolvers.go)). The legacy `/policies` path (which requires `reconciledAtPayments` in the past) always reads point-in-time. Callers set per-source PITs on `POST /rules/{id}/evaluate` via `sourcePITs`; residual cross-source skew is still absorbed by the template's `tolerance`.

---

## 6. Ledger-side feature-flag gotcha — fixed in ledger v2.4.11

A baseline finding on `Ledger ≤ v2.4.10`: `/aggregate/balances` with `pit=…` + a **metadata** filter silently returned `{}` when the ledger had `ACCOUNT_METADATA_HISTORY: DISABLED`. Same call without `pit` worked; same `pit + address` filter worked.

Filed [formancehq/ledger#1416](https://github.com/formancehq/ledger/issues/1416) — **fixed in ledger v2.4.11** ([ledger#1422](https://github.com/formancehq/ledger/pull/1422), commit `dd1d8c9`): the aggregated-balances PIT path now falls back to the current `accounts.metadata` column under `DISABLED`, so PIT + metadata returns correct results regardless of the history flag. Confirmed empirically against `reco-ledger` (DISABLED) on v2.4.11.

V1 stance: Reconciliation **requires ledger ≥ v2.4.11**, so there is **no create-time refusal** for metadata-filtered templates — the earlier "consult feature flags and reject on history-off" mitigation (and the `LedgerResolver.Features()` plumbing it would have used) was **dropped** as a workaround for an upstream bug that no longer exists. Address-based filters were always PIT-safe regardless of the flag.

---

## 7. Resolution model

### Legacy

None. Every failing reconciliation just produces another `NOT_OK` row.

### V1

Three closure paths on `Alert`, persisted in the `resolution` jsonb column (current resolution) and in `alert_event` rows (historical resolutions across reopen cycles):

| Kind | Trigger | Required artefacts |
|---|---|---|
| `auto` | Next evaluation passes for the same fingerprint | None — system-attributed |
| `fixed_by_booking` | Operator marks resolved, optionally referencing corrective transactions | Author, timestamp, transactionRefs (optional), note (optional) |
| `accepted_by_business` | Operator declares the discrepancy acceptable | Author, timestamp, **note (required)**, evidence snapshot frozen at acceptance |

Re-opens after RESOLVED create a *new* row with `—` pointing at the prior one — flapping is visible, MTTR is clean. See [workflows.md](./workflows.md) for the lifecycle diagram.

---

## 8. Engine errors vs data alerts

### Legacy

A failed SDK call surfaces as `status: NOT_OK` with the SDK error as text. Mixed with real data discrepancies.

### V1

- **Data alerts** — opened from per-fingerprint failing `Outcome`s.
- **`engine.error` meta-alerts** — opened when the kernel or a resolver itself fails (CEL builtin throws, source resolver times out, budget exceeded). Distinct fingerprint + `kind: engine.error` label so digests can route them to an engine-health channel instead of the financial-alert feed.

---

## 9. Auditability

### Legacy

`Reconciliation` rows store the balances seen but not the PITs used. A reviewer can see "drift was X on Sept 4" but cannot replay the underlying ledger or payments query deterministically.

### V1

Every `Evaluation` row stores `pit_per_source` (the PIT each `Source` resolved at). Combined with the per-alert append-only `alert_event` log — which captures every transition, including each historical resolution across reopen cycles — an auditor query *"show every alert resolved in Q3, who closed it, how, with what evidence"* becomes one join.

---

## What's NOT changing

- The legacy `/policies` API behaviour is preserved. Existing customers keep working.
- `reconciliations.policy` and `reconciliations.reconciliation` tables stay.
- Auth integration with the rest of the Formance stack is unchanged.
- The sibling "transaction reconciliation" project (line-level matching) is **out of scope** here — see [PRD §3](../prd/README.md#3-goals--non-goals).

---

## Where to dig deeper

- The engine internals: [architecture.md](./architecture.md) and [ADR-001](../prd/adr-001-cel-kernel.md).
- The PIT semantics & cross-source consistency: [ADR-002](../prd/adr-002-pit-consistency.md).
- Why we're not exposing CEL at V1 GA: see [ADR-001 §Future commitments](../prd/adr-001-cel-kernel.md).
- Product positioning: reconciliation is a *business process*, not a generic rules engine — the lifecycle is observe → detect → resolve/accept.
