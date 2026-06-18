# ADR-002 — Consistency model: PIT-per-source, not global snapshot

**Status:** Accepted (implemented in [`internal/engine/engine.go`](../../internal/engine/engine.go) and [`internal/templates/`](../../internal/templates/))
**Linked from:** [PRD §9](./README.md), [v1-vs-legacy.md §6](../technical/v1-vs-legacy.md)
**Last updated:** 2026-06-17

---

## 1. Decision in one sentence

The engine guarantees **per-source point-in-time consistency**, *not* atomic cross-source snapshots. Each `Source` resolves to a PIT-stable snapshot of its own backend; cross-source consistency is the customer's tolerance to encode in the template.

---

## 2. Why this matters

Today's reconciliation API takes **two** timestamps in every request:

```go
type ReconciliationRequest struct {
    ReconciledAtLedger   time.Time `json:"reconciledAtLedger"`
    ReconciledAtPayments time.Time `json:"reconciledAtPayments"`
}
```

— and validates them both as past timestamps ([`internal/api/service/reconciliation.go:24-38`](../../internal/api/service/reconciliation.go)). The caller picks both PITs, and the reconciliation compares ledger-at-T1 against payments-at-T2. That's not laziness — it's the only honest model when the underlying systems don't share a clock.

V1 keeps this contract but makes it **explicit**, **uniform**, and **auditable**.

---

## 3. What the underlying systems guarantee

### Ledger v2

`/aggregate/balances` accepts a `pit` parameter. The result is **PIT-consistent on the ledger side**: a posting that commits at PIT+1ms is invisible to a read at PIT by construction. Append-only log + immutable history.

### Payments v3

The legacy SDK call to `/api/payments/pools/{id}/balances?at=…` silently returns `[]` under payments v3 — the PIT-aware route was not implemented. The endpoint that works is `/v3/pools/{id}/balances/latest`, which is the **current** snapshot — not historically queryable.

This means the payments side is effectively **"as of now, eventually consistent with the upstream provider"**. We can't ask "what was the pool balance an hour ago"; only "what is it right now."

### External GL (V2+)

Each adapter brings its own consistency semantics — NetSuite at a given period close, Sage at a posting boundary. The engine model has to accommodate this without flattening to a global PIT.

---

## 4. The consistency model V1 ships

| Property | Guarantee |
|---|---|
| **Per-source PIT consistency** | Each `Source` resolves to a snapshot stable against subsequent writes on that source |
| **Cross-source atomicity** | Not provided. Two sources resolved in the same evaluation may reflect different real-world instants |
| **Auditability** | Every evaluation persists the resolved PIT per source, so an auditor can replay each side at the original PIT |
| **Safety margin** | Engine subtracts a configurable safety margin from the requested PIT before passing it to resolvers, avoiding races with in-flight commits |
| **Tolerance** | The template's tolerance parameter is the mechanism for absorbing legitimate cross-source skew (settlement lag, FX revaluation timing, etc.) |

---

## 5. How it flows through the engine

```mermaid
flowchart LR
    Caller["Caller: PIT = T, safetyMargin = 30s"] --> Engine[Engine.Evaluate]
    Engine --> Adjust["Effective PIT = T - 30s"]
    Adjust --> Bind["Bind ledgerSet.PIT,\npool.PIT in evalCtx"]
    Bind --> Eval[Run CEL]
    Eval --> Ledger["ledgerSet resolver:\nV2.GetBalancesAggregated(pit=T-30s)"]
    Eval --> Pool["pool resolver:\nV3.GetPoolBalancesLatest()\n(latest, not PIT)"]
    Ledger --> Record[Record pit_per_source: { ledger_set:0 → T-30s }]
    Pool --> Record2[Record pit_per_source: { payments_pool:0 → T-30s }]
    Record --> Persist[INSERT evaluation]
    Record2 --> Persist
```

**Key implementation details**

- Engine reads `EvalInput.PIT` and `EvalInput.SafetyMargin` from the caller (the service layer; templates also subtract margin before any scout calls so the math matches).
- The `Source` Go struct carries a `PIT` field set by the source-constructor builtin at eval time.
- Each resolver's signature takes `pit time.Time` even when the underlying API doesn't honour it — the value is **recorded for audit** even if the resolver internally falls back to "latest" (payments case). The recorded PIT then appears in `evaluation.pit_per_source`.

---

## 6. Why we don't try to build a global snapshot

Three reasons:

1. **It's impossible across heterogeneous systems.** Ledger and Payments don't share a clock or a transaction log. Pretending they do produces a leaky abstraction that surprises customers the first time settlement lag exceeds expectations.
2. **Real financial workflows already model settlement lag.** Treasury teams know about T+1 / T+2 cycles. Tolerance is the right vocabulary; "atomic snapshot" is not.
3. **The customer-facing story is honest.** The product promises *PIT-consistent invariants over heterogeneous sources*, not *atomic invariants over a global snapshot*. The distinction matters when an auditor asks "is this reproducible?"

---

## 7. Implications for templates

Templates that compare two sides accept a `tolerance` parameter (per asset):

- `ledger_vs_pool_drift` — `tolerance` defaults to 0 per asset; raise it to absorb known settlement lag.
- `ledger_invariant` — `tolerance` is **required** (set 0 for strict equality); same intent.
- `account_threshold` — single-source, no cross-source tolerance needed.

Documentation guidance (will land alongside the public template docs):

> *Tolerance is how you encode legitimate settlement lag between sources. If your ledger updates at T and your payments provider settles at T+30 min, set a tolerance equal to the largest in-flight transfer you expect at any moment. The reconciliation will fire only when drift exceeds that bound.*

---

## 8. The two upstream issues this model surfaces

Two real implementation gotchas captured during the V1 baseline check:

### A. Payments v3 PIT regression

The SDK's `GetPoolBalances` (legacy path) returns `[]` under payments v3 even with valid PIT. V1's `SDKPaymentsResolver` uses `V3.GetPoolBalancesLatest` instead — accepting that the payments side reads "current" and pushing PIT semantics out to the persisted `pit_per_source` audit record. Documented inline in [`internal/engine/sdk_resolvers.go`](../../internal/engine/sdk_resolvers.go).

### B. Ledger v2 `/aggregate/balances` + PIT + metadata silent failure

When `ACCOUNT_METADATA_HISTORY: DISABLED` on a ledger, `/aggregate/balances?pit=…` with a metadata filter returns `{}` silently — the same call without `pit` works, the same call with an address filter works, and `/accounts` with `pit + metadata` returns matches. Endpoint-specific inconsistency.

Filed: [formancehq/ledger#1416](https://github.com/formancehq/ledger/issues/1416). Captured in [project memory `ledger-aggregate-pit-metadata`](../../../.claude/projects/-Users-arnaud-Documents-GitHub-reconciliation/memory/ledger_aggregate_pit_metadata.md). V1 mitigation: `SDKLedgerResolver.Features()` caches the flag; the service layer (task #5) will refuse metadata-based templates against history-off ledgers with a clear error.

---

## 9. What this commits us to

1. **Every Evaluation stores per-source PITs.** Already shipped on the schema ([migration #4](../../internal/storage/migrations/migrations.go)) and the model ([Evaluation.PitPerSource](../../internal/models/evaluation.go)).
2. **The Engine never offers a "global PIT" abstraction.** If a future template needs cross-source PIT alignment, the alignment happens at the template layer (it owns the resolvers), not in the kernel.
3. **Templates that compare cross-source must accept a tolerance.** Strictly-zero comparisons across heterogeneous sources are a footgun and are documented as such.
4. **Customer-facing language is precise.** Marketing and docs say *PIT-consistent invariants over heterogeneous sources*, never *atomic cross-source consistency*.

---

## 10. Conditions under which we'd revisit

- **Payments grows PIT support** that's compatible with the legacy SDK shape → switch the resolver to use it; the model doesn't change.
- **Ledger v3 introduces cross-ledger snapshot semantics** → the kernel might offer an optional "aligned PIT" mode within a single Formance stack. Cross-vendor sources remain per-source.
- **A customer surfaces a use case where tolerance can't encode the drift they care about** → revisit, but expect this to be a new template (e.g. "balance change over time window") rather than a model change.

We would **not** revisit to pretend two heterogeneous systems share a clock. That's the foot-gun this ADR exists to prevent.
