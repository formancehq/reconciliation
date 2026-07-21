# Reconciliation — Ledger Clarity Documentation

Reconciliation is evolving from a one-shot drift check between Ledger and Payments into a **continuous-controls product for ledger state** (working name: *Ledger Clarity*). It observes financial invariants you define, produces evidence whenever an invariant breaks, raises a stable, dedup'd alert, and supports a documented resolution — either by booking a corrective transaction or by formally accepting the discrepancy with note, author, and audit trail.

The internal kernel that powers it (CEL over a typed object model + `Source` abstraction) is implementation detail. Customers buy *clarity over their ledger*, not a rules engine.

> **State of the work.** This is an in-flight V1. Tasks ✅ and 🚧 mark what's landed, in progress, and planned.
> See the task index at the bottom of this page.

---

## Documentation

### [Product (`prd/`)](./prd/README.md)
The full PRD (v0.5), positioning, scope, phasing, and the two architecture decision records that pin down the most expensive technical choices.

### [Technical (`technical/`)](./technical/README.md)
Architecture, the diff vs the legacy `/policies` path, the API reference, the lifecycle workflows, the template catalog, and how to run the stack locally.

### [Drafts (`drafts/`)](./drafts/README.md)
V1.1+ feature carve-outs and EE+ "Finance Ops" notes — not committed scope.

---

## Quick links

| Topic | Link |
|-------|------|
| **PRD v0.5** | [prd/README.md](./prd/README.md) |
| **What's changing vs today's `/policies`** | [technical/v1-vs-legacy.md](./technical/v1-vs-legacy.md) |
| **API reference (legacy + V1)** | [technical/api.md](./technical/api.md) |
| **Lifecycle workflows** | [technical/workflows.md](./technical/workflows.md) |
| **Template catalog** | [technical/templates.md](./technical/templates.md) |
| **Architecture overview** | [technical/architecture.md](./technical/architecture.md) |
| **End-to-end demo UI** | [`../../poc-reconciliation-demo`](../../poc-reconciliation-demo) (sibling repo) |
| **ADR-001 — CEL kernel choice** | [prd/adr-001-cel-kernel.md](./prd/adr-001-cel-kernel.md) |
| **ADR-002 — PIT consistency model** | [prd/adr-002-pit-consistency.md](./prd/adr-002-pit-consistency.md) |

---

## V1 build status

| # | Scope | Status |
|---|-------|--------|
| 1 | Local docker stack isolated from `stack/` | ✅ shipped |
| 2 | Migrations + Go models for `Rule` / `Evaluation` / `Alert` / `AlertEvent` / `Resolution` | ✅ shipped |
| 3 | Internal CEL kernel (`internal/engine/`) — types, Source, builtins, resolvers, budget | ✅ shipped |
| 4 | V1 GA template catalog — `ledger_vs_pool_drift` / `ledger_invariant` / `account_threshold` / `source_parity` | ✅ shipped (per-asset / aggregate; per-account scope parked post-V1 — impl retained) |
| 5 | Service layer — Rule / Evaluation / Alert orchestration + resolution paths + event-log append | ✅ shipped |
| 6 | API endpoints + legacy `/policies` facade + OpenAPI | ✅ shipped |
| 7 | End-to-end demo UI ([poc-reconciliation-demo](../../poc-reconciliation-demo)) — replaces the planned dockertest harness | ✅ shipped |
| 8 | V1 GA additions — webhook events ✅ · durable PostgreSQL scheduler worker ✅ · email digest / fctl / EE gating / metering 🚧 | 🚧 in progress |

Filed upstream: [formancehq/ledger#1416](https://github.com/formancehq/ledger/issues/1416) — `/aggregate/balances` PIT + metadata silently returns empty under `ACCOUNT_METADATA_HISTORY: DISABLED`.
