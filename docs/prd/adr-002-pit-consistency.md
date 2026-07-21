# ADR-002 — Consistency model: PIT-per-source, not global snapshot

**Status:** Accepted (implemented in [`internal/engine/engine.go`](../../internal/engine/engine.go) and [`internal/templates/`](../../internal/templates/))
**Linked from:** [PRD §9](./README.md), [v1-vs-legacy.md §6](../technical/v1-vs-legacy.md)
**Last updated:** 2026-07-16

> **Revision 2026-07-16.** §3 and §8.A originally stated payments v3 had no
> faithful point-in-time pool read. That was a misdiagnosis (a read at ~now hits
> the empty balance-window tail — see §3). Re-verified against payments v3.3.1:
> `GET /v3/pools/{id}/balances?at=` is a genuine PIT read. The decision below is
> **unchanged** — per-source PIT, no global snapshot — but the payments side now
> honours PIT per source instead of always reading latest. Sections updated
> accordingly.

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

Payments v3 exposes **two** pool-balance reads, both genuine (verified against v3.3.1):

- `/v3/pools/{id}/balances/latest` — the current snapshot, always populated.
- `/v3/pools/{id}/balances?at=…` — the balance **valid at `at`**. Historically queryable: distinct values at distinct past instants, empty before the pool existed, and `at` is required + must not be in the future.

So the payments side **is** point-in-time queryable — we *can* ask "what was the pool balance an hour ago." The one caveat is a **balance-window tail**: a pool balance carries a validity window `[createdAt, lastUpdatedAt]`, and the *current* balance sits as a point at the last movement until a new movement supersedes it. A read strictly after the last movement matches no window and returns `[]`, whereas `latest` returns the current balance unconditionally. A read at ~now is always past the last movement, so **"as of now" must use `latest`, while an explicitly historical instant uses `?at=`.**

(The earlier claim that the `?at=` path "silently returns `[]` / was not implemented" was a misdiagnosis: it was almost certainly observed by reading at ~now — the tail case above — not by exercising a genuine past instant within the pool's active history.)

### External GL (V2+)

Each adapter brings its own consistency semantics — NetSuite at a given period close, Sage at a posting boundary. The engine model has to accommodate this without flattening to a global PIT.

---

## 4. The consistency model V1 ships

| Property | Guarantee |
|---|---|
| **Per-source PIT consistency** | Each `Source` resolves to a snapshot stable against subsequent writes on that source |
| **Cross-source atomicity** | Not provided. Two sources resolved in the same evaluation may reflect different real-world instants |
| **Auditability** | Every evaluation persists the effective historical PIT per source. A Payments `latest` read instead records its observation time because the upstream response has no snapshot timestamp; its evidence is frozen but exact historical replay is not guaranteed |
| **Safety margin** | Engine subtracts a configurable safety margin from the default requested PIT. Already-effective per-source replay overrides are not adjusted again |
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
    Eval --> Pool["pool resolver:\nV3.GetPoolBalances(at=T-30s) if explicit PIT\nelse V3.GetPoolBalancesLatest()"]
    Ledger --> Record[Record pit_per_source: { ledger_set:0 → T-30s }]
    Pool --> Record2[Record historical PIT, or latest observation time]
    Record --> Persist[INSERT evaluation]
    Record2 --> Persist
```

**Key implementation details**

- Engine reads `EvalInput.PIT`, `EvalInput.SafetyMargin`, and optional per-source overrides `EvalInput.SourcePITs` from the caller. A source with no override resolves at the margin-adjusted default PIT. An override is already an effective replay instant and is used unchanged, preventing a second margin subtraction. Historical instants are recorded in `evaluation.pit_per_source`, keyed by the stable `"<label>#<idx>"` key.
- `EvalInput.PITExplicit` marks a caller-supplied PIT (vs the service defaulting to now). It gates the payments-pool read: an explicit past instant reads `V3.GetPoolBalances(?at=)`; the "as of now" default reads `V3.GetPoolBalancesLatest` (a PIT read at ~now hits the empty balance-window tail from §3). An override always counts as explicit for its source.
- The ledger resolver always reads point-in-time at its source's PIT. Payments historical reads do the same. The Payments `latest` response has no snapshot timestamp, so V1 records the successful observation time and does not claim exact replay for that path.

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

### A. Payments-pool balance-window tail (not a PIT regression)

Originally recorded here as "payments v3 has no PIT read." Corrected: `V3.GetPoolBalances(?at=)` is a genuine PIT read. The real gotcha is the **balance-window tail** (§3): the current pool balance is a point at its last movement, so a read at ~now returns `[]` while `latest` returns the balance. V1's `SDKPaymentsResolver.PoolBalance` therefore reads `?at=` for an explicit past instant and `latest` for the as-of-now default — not a workaround, but the correct read for each case. Documented inline in [`internal/engine/sdk_resolvers.go`](../../internal/engine/sdk_resolvers.go).

### B. Ledger v2 `/aggregate/balances` + PIT + metadata silent failure

When `ACCOUNT_METADATA_HISTORY: DISABLED` on a ledger, `/aggregate/balances?pit=…` with a metadata filter returns `{}` silently — the same call without `pit` works, the same call with an address filter works, and `/accounts` with `pit + metadata` returns matches. Endpoint-specific inconsistency.

Filed: [formancehq/ledger#1416](https://github.com/formancehq/ledger/issues/1416). **Fixed upstream in ledger v2.4.11** — the version V1 targets (see the compose stack), so this is resolved for supported deployments. `SDKLedgerResolver` reads PIT + metadata directly; [`internal/engine/resolvers.go`](../../internal/engine/resolvers.go) notes the read is safe on ledger ≥ v2.4.11. A create-time version-gate guard against older ledgers would be an optional workaround (story B03), not a live requirement — there is no `Features()` flag cache or service-layer refusal in the code.

---

## 9. What this commits us to

1. **Every Evaluation stores per-source PITs.** Already shipped on the schema (the V1 "Ledger Clarity tables" [migration](../../internal/storage/migrations/migrations.go)) and the model ([Evaluation.PitPerSource](../../internal/models/evaluation.go)).
2. **The Engine never offers a "global PIT" abstraction.** If a future template needs cross-source PIT alignment, the alignment happens at the template layer (it owns the resolvers), not in the kernel.
3. **Templates that compare cross-source must accept a tolerance.** Strictly-zero comparisons across heterogeneous sources are a footgun and are documented as such.
4. **Customer-facing language is precise.** Marketing and docs say *PIT-consistent invariants over heterogeneous sources*, never *atomic cross-source consistency*.

---

## 10. Conditions under which we'd revisit

- ~~**Payments grows PIT support**~~ — done. Payments v3 already has it (`?at=`), and the resolver now uses it per source; the model didn't change.
- **Ledger v3 introduces cross-ledger snapshot semantics** → the kernel might offer an optional "aligned PIT" mode within a single Formance stack. Cross-vendor sources remain per-source.
- **A customer surfaces a use case where tolerance can't encode the drift they care about** → revisit, but expect this to be a new template (e.g. "balance change over time window") rather than a model change.

We would **not** revisit to pretend two heterogeneous systems share a clock. That's the foot-gun this ADR exists to prevent.
