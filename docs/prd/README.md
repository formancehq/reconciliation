# Ledger Clarity — PRD

| Field          | Value                                  |
| -------------- | -------------------------------------- |
| Owner          | Arnaud                                 |
| Status         | Draft v0.5                             |
| Last updated   | 2026-06-17                             |
| Code module    | `reconciliation` (unchanged)           |
| Product name   | **Ledger Clarity** *(working title)*   |
| Tier           | EE (V1) + EE+ "Finance Ops" pack (V2+) |
| Sub-pages      | [ADR-001](./adr-001-cel-kernel.md) · [ADR-002](./adr-002-pit-consistency.md) |

---

## 1. TL;DR

**Ledger Clarity** is Formance's continuous-controls product for ledger state. It observes financial invariants you define (drift, balance thresholds, account-set equality), produces **evidence** whenever an invariant breaks, opens a **business incident**, and supports a documented **resolution** — either by **booking a corrective transaction** or by **formally accepting the discrepancy** with note, author, evidence, and audit trail.

The source-agnostic engine that powers it (see [ADR-001](./adr-001-cel-kernel.md)) is implementation detail. **Customers buy clarity over their ledger, not a rule engine.**

The product narrative is a five-stage business lifecycle:

> **Observe → Detect → Incident → Evidence → Resolve or Accept**

---

## 2. Context & opportunity

### Signal

Three clients (Buildr + 2) have independently asked for the same shape — *"alert me when X happens to my ledger state."* All three accepted **headless delivery** (email / webhook), no UI required. The asks cluster into three primitives: sum-of-subsets equality, threshold guards, inactivity detectors.

### What ships today

Reconciliation v1 ([`internal/api/service/reconciliation.go:43`](../../internal/api/service/reconciliation.go)) compares **one dynamic set of ledger accounts** (resolved at run-time from a metadata query) against **one dynamic payments pool** (membership resolved at run-time from the Payments module), on the same asset basis, on demand, synchronously, with no notification surface. The dynamic-set primitive already lives on both sides — it's hardcoded into a fixed *(ledger query, payments pool)* pair.

### What's actually missing

- **Symmetry of sources.** The two sides are hardcoded as "ledger query" and "payments pool" — no way to compose N sides, swap in a different source kind.
- **Operators beyond `drift == 0`.** Equality is the only predicate.
- **Time, repetition, delivery.** No scheduler, no incident lifecycle, no notification surface.
- **Resolution.** No way to mark "fixed by booking" or "accepted by business" — every break is just a recomputed comparison.

### Strategic vector

Generalizing source + predicate also opens a second product line: **continuous reconciliation against external corporate GLs** (NetSuite / Sage / Xero / SAP) — a high-WTP enterprise use case for any company using Formance as their financial backbone. Ledger v3 opens cross-ledger queries; the same engine extends to multi-ledger invariants (consolidated balance sheets, intercompany) with no shape changes.

### Out of scope — sibling project's domain

Transaction-level reconciliation (pairwise matching of postings ↔ external statement lines, "2-way / 3-way match", fuzzy matching, unmatched-item workflows). This module asserts **aggregate** correctness; the sibling project resolves **line-level** divergence. Breaks found here become tickets the transaction-recon tool investigates.

---

## 3. Product framing

> Ledger Clarity is a **business process** product, not a generic rules engine. Customers don't pay for an expression language — they pay to know **when their financial state is wrong, why, since when, with what evidence, and how to handle it.**

The product surface centres on the five-stage lifecycle:

| Stage         | What the customer sees                                                                  | What the system does                                                                |
| ------------- | --------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| **Observe**   | A library of pre-built controls (templates) over their ledger state                     | Rule definitions, scheduled or on-demand                                            |
| **Detect**    | A break is identified at a known point in time                                          | Evaluator runs predicate; failure becomes a fingerprinted candidate incident         |
| **Incident**  | A dedup'd, ack-able, severity-tagged record they can operate against                    | Open / acknowledged / resolved lifecycle; flap-aware                                |
| **Evidence**  | The exact numbers and accounts that made the rule fail, frozen at break time            | Evaluation record with PIT-consistent snapshot                                      |
| **Resolve or Accept** | Either *booked a correction* or *formally accepted the discrepancy*               | Two resolution paths, both audit-trailed                                            |

The engine internals (rules, expressions, kernel) exist to serve this lifecycle, not the other way around.

---

## 4. Goals / Non-goals

### Goals (V1 GA)

- Three template types: `ledger_vs_pool_drift` (port of today), `ledger_invariant`, `account_threshold`.
- Cron + on-demand evaluation.
- **Incident lifecycle including resolution** — auto-resolve on passing evaluation, manual *fixed by booking* (optional transaction refs), manual *accepted by business* (required note + author + evidence snapshot + optional expiry).
- Event publication: `reconciliation.incident.opened | updated | acknowledged | resolved | accepted | reopened`.
- Webhook delivery via the existing Webhooks module + an email digest owned in-module.
- Backwards compatibility: existing `Policy` evaluates as `ledger_vs_pool_drift`.
- EE gating + usage metering.

### Non-goals (V1)

- **No raw-CEL public API.** Templates are the entire surface. Raw CEL = design-partner-gated post-GA. See [ADR-001 addendum](./adr-001-cel-kernel.md#9-implications--consequences).
- **No vendor-specific integrations** (Jira, Asana, Monday, Linear, ServiceNow, PagerDuty, Slack). We publish clean events; Webhooks delivers; customer workflow handles ownership/routing.
- No UI.
- No cross-ledger queries (Ledger v3 GA).
- No external GL adapters (V2).
- No streaming / CDC ingestion.
- **No automated remediation via the Transaction Plane** — resolution stays human-first.

### Out of scope — sibling project's domain

- Transaction-level matching, unmatched-item workflows, posting suggestion.

---

## 5. Users & jobs-to-be-done

| Persona                          | JTBD                                                                                          | Today's workaround                            |
| -------------------------------- | --------------------------------------------------------------------------------------------- | --------------------------------------------- |
| Treasury / Finance Ops           | "Tell me within an hour if my cash held ≠ obligations recorded"                              | Manual daily extract, spreadsheet diff        |
| Compliance / Risk                | "Prove that customer-funds accounts never go negative and never commingle"                   | Periodic audit query, no continuous check     |
| Platform engineer                | "Alert me if any sub-account stops moving — usually means an upstream stall"                 | Custom Prometheus rule on derived metric      |
| Implementation team              | "Ship the deal without building a sidecar alerting service"                                  | Bespoke schedulers per customer               |
| **Finance / Controller (V2+)**   | "Continuously prove that Formance matches the corporate GL — auditor-ready"                  | Manual month-end trial-balance reconciliation |

### Anchor scenarios

- **S1 — Buildr trust account integrity** *(equality, V1)*
- **S2 — Threshold guard** *(threshold, V1)*
- **S3 — Inactivity** *(staleness, V1.1)*
- **S4 — Existing ledger-vs-pool drift** *(preserved as template, V1)*
- **S5 — GL trial-balance reconciliation** *(V2/V3 horizon, validates engine shape)*

---

## 6. Conceptual model

```
Rule          → Evaluation → (on fail)  Incident  →  Resolution
(definition)    (one run,    (stateful,   (audit-trailed
                always       dedup'd,      closure path)
                persisted)   ack-able)
                                            │
                                            ▼
                                       Event published
                                            │
                                            ▼
                                  Webhooks module / email digest
```

See [docs/technical/architecture.md](../technical/architecture.md) for the implementation view and [docs/technical/workflows.md](../technical/workflows.md) for the lifecycle diagrams.

### 6.1 V1 GA template catalog

| Template                | Semantic                                                 | Code |
| ----------------------- | -------------------------------------------------------- | ---- |
| `ledger_vs_pool_drift`  | Port of today's drift check                              | ✅ [ledger_vs_pool_drift.go](../../internal/templates/ledger_vs_pool_drift.go) |
| `ledger_invariant`      | Σ signed balances ≤ tolerance                            | ✅ [ledger_invariant.go](../../internal/templates/ledger_invariant.go) |
| `account_threshold`     | Each / aggregate balance within `[lo, hi]`               | ✅ [account_threshold.go](../../internal/templates/account_threshold.go) (aggregate mode only — per-account is V1.1) |

### 6.2 V1.1 fast-follow catalog

| Template                | Compiles to (internal)                                              |
| ----------------------- | ------------------------------------------------------------------- |
| `account_inactivity`    | `accounts(ledgerSet(q)).all(a, now() - a.lastActivity <= maxIdle)`  |
| `posting_rate`          | `postings(q, "1h").count <= maxPerHour`                             |
| `metadata_invariant`    | `accounts(ledgerSet(q)).all(a, has(a.metadata.kyc_level))`          |
| `cross_account_ratio`   | `accounts(pairs).all(p, p.a.balance >= k * p.b.balance)`            |

See [docs/technical/templates.md](../technical/templates.md) for the live reference (spec shapes, validation rules, what each compiles to).

---

## 7. Phasing

| Phase        | Scope                                                                                                                                                                                                                                          | Why                                                                                                |
| ------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| **V1 beta**  | `ledger_vs_pool_drift` (port), `ledger_invariant`, `account_threshold` (aggregate). **On-demand evaluation only.** Persisted evidence. Minimal incident lifecycle (open → resolved + acceptance). Internal CEL kernel. No scheduler, notifications, fctl, or metering. | Validates rule → evaluation → incident → resolution model with design partners before scheduler/ops complexity lands |
| **V1 GA**    | Cron scheduler · webhook + email digest · full resolution model · fctl · EE gating · usage metering                                                                                                                                            | Production-ready for the three named clients                                                       |
| **V1.1**     | `account_inactivity`, posting-window rules, `metadata_invariant`, `cross_account_ratio` · snooze · flap suppression · richer resolution UX                                                                                                     | Catalog-only & lifecycle polish — no engine change                                                 |
| **V2**       | External GL adapters · cross-ledger on Ledger v3 · richer resolution workflows · raw-CEL design-partner GA                                                                                                                                     | Opens EE+ Finance-Ops product line                                                                 |

**Beta → GA gate:** design-partner sign-off that the lifecycle and evidence shape work, before we commit to the public scheduler/notification surface.

---

## 8. Open questions & risks

See the full v0.5 spec for §16 (open questions) and §17 (risks). Highlights:

- **Product name.** Ledger Clarity (lean) vs Ledger Transparency.
- **Acceptance expiry behaviour.** New incident parent-linked, or re-open same? Lean **new incident**, parent-linked.
- **Scheduler host.** In-process vs Temporal. Lean Temporal; needs architecture review.
- **CEL builtin naming review** before any post-GA exposure to customers.
- **Notification fatigue** is the biggest product risk. Mitigation: incident dedup, severity-aware delivery, digest mode default for low/medium.

---

## 9. What's already proven (status as of 2026-06-17)

- ✅ Local stack stands up end-to-end isolated from `stack/` ([docs/technical/local-dev.md](../technical/local-dev.md))
- ✅ Storage layer for `Rule` / `Evaluation` / `Incident` / `Resolution` ([migration #4](../../internal/storage/migrations/migrations.go))
- ✅ Internal CEL kernel with 11 passing tests ([internal/engine/](../../internal/engine/))
- ✅ Three V1 GA template evaluators with 18 passing tests ([internal/templates/](../../internal/templates/))
- ⏳ Service layer wiring (task #5 next)
- ⏳ API endpoints + legacy `/policies` facade (task #6)
- ⏳ Integration test suite via dockertest (task #7)
- ⏳ V1 GA additions: scheduler, webhooks, digest, fctl, EE gating, metering (task #8)

---

## Cross-links

- [V1 vs legacy diff](../technical/v1-vs-legacy.md)
- [Architecture overview](../technical/architecture.md)
- [Template catalog reference](../technical/templates.md)
- [Workflows](../technical/workflows.md)
- [API reference](../technical/api.md)
- [Local dev](../technical/local-dev.md)
- [ADR-001 — CEL kernel choice](./adr-001-cel-kernel.md)
- [ADR-002 — PIT consistency model](./adr-002-pit-consistency.md)
