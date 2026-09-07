# Reconciliation — Ledger Clarity Documentation

Reconciliation is evolving from a one-shot drift check between Ledger and Payments into a **continuous-controls product for ledger state** (working name: *Ledger Clarity*). It observes financial invariants you define, produces evidence whenever an invariant breaks, raises a stable, dedup'd alert, and supports a documented resolution — either by booking a corrective transaction or by formally accepting the discrepancy with note, author, and audit trail.

The internal kernel that powers it (CEL over a typed object model + `Source` abstraction) is implementation detail. Customers buy *clarity over their ledger*, not a rules engine.

> **State of the work.** This is an in-flight V1. Tasks ✅ and 🚧 mark what's landed, in progress, and planned.
> See the task index at the bottom of this page.

---

## Documentation

### [Product (`prd/`)](./prd/README.md)
The full PRD (v0.5), positioning, scope, phasing, and the architecture decision records that pin down the most expensive technical choices.

### [Technical (`technical/`)](./technical/README.md)
Architecture, the API reference, the lifecycle workflows, the template catalog, and how to run the stack locally.

### [Drafts (`drafts/`)](./drafts/README.md)
V1.1+ feature carve-outs and EE+ "Finance Ops" notes — not committed scope.

---

## Quick links

| Topic | Link |
|-------|------|
| **PRD v0.5** | [prd/README.md](./prd/README.md) |
| **API reference** | [technical/api.md](./technical/api.md) |
| **Lifecycle workflows** | [technical/workflows.md](./technical/workflows.md) |
| **Template catalog** | [technical/templates.md](./technical/templates.md) |
| **Architecture overview** | [technical/architecture.md](./technical/architecture.md) |
| **Ledger v3 storage model** | [technical/ledger-v3-storage.md](./technical/ledger-v3-storage.md) |
| **End-to-end demo UI** | [`../../poc-reconciliation-demo`](../../poc-reconciliation-demo) (sibling repo) |
| **ADR-001 — CEL kernel choice** | [prd/adr-001-cel-kernel.md](./prd/adr-001-cel-kernel.md) |
| **ADR-002 — PIT consistency model** | [prd/adr-002-pit-consistency.md](./prd/adr-002-pit-consistency.md) |
| **ADR-004 — multi-source comparisons** | [prd/adr-004-multi-source-comparisons.md](./prd/adr-004-multi-source-comparisons.md) |

---

## V1 build status

| # | Scope | Status |
|---|-------|--------|
| 1 | Local docker stack isolated from `stack/` | ✅ shipped |
| 2 | Migrations + Go models for `Rule` / `Evaluation` / `Alert` / `AlertEvent` / `Resolution` | ✅ shipped |
| 3 | Internal CEL kernel (`internal/engine/`) — types, Source, builtins, resolvers, budget | ✅ shipped |
| 4 | Template catalogue — six named-source templates | ✅ shipped (the three positional V1 templates were retired; per-account fan-out dropped with them, see the ADR-004 amendment) |
| 5 | Service layer — Rule / Evaluation / Alert orchestration + resolution paths + event-log append | ✅ shipped |
| 6 | API endpoints + OpenAPI | ✅ shipped |
| 7 | End-to-end demo UI ([poc-reconciliation-demo](../../poc-reconciliation-demo)) — replaces the planned dockertest harness | ✅ shipped |
| 8 | V1 GA additions — event delivery via the ledger events sink ✅ · in-process cron scheduler ✅ (single-instance) · email digest / fctl / EE gating / metering 🚧 | 🚧 in progress |
| 9 | V2 additive API — `balance_equation`, `exchange_rate_bounds`, `source_consensus`, and `coverage_ratio_bounds`, isolated from V1 persisted contracts | ✅ implemented |

## V2 compatibility boundary

V2 is additive. Existing V1 rules remain on the unprefixed `/rules` and `/alerts` routes and retain
their persisted `source_parity.left` / `.right` fields and `leftSource` / `leftBalance` /
`rightSource` / `rightBalance` evidence. V2 resources live under `/v2`, carry
`contractVersion: 2`, and use named `sources[]` entries instead of positional left/right fields.
There is no automatic rewrite or backfill of V1 rules, captures, alerts, or accepted evidence
snapshots. See [the API reference](./technical/api.md#v1v2-coexistence) and
[ADR-004](./prd/adr-004-multi-source-comparisons.md).

> **Ledger-native migration (branch `feat/reconciliation-ledger-v3`).** The storage layer above was
> subsequently reshaped: reconciliation is now **Postgres-free**, running on a Ledger v3
> control-ledger (default name `reconciliation`; `_recon` in design shorthand), reading the reconciled ledgers live, recording an immutable capture for
> every evaluation, and delivering
> events via the ledger's native sink. Steps 2 (migrations/models → ledger accounts), 5 (event-log →
> ledger log), and the reads are superseded — see the
> [migration log](./drafts/ledger-v3-migration-log.md), [architecture.md](./technical/architecture.md),
> and the consolidated [Ledger v3 storage model](./technical/ledger-v3-storage.md).

Recon reads data-ledgers live via gRPC `AggregateVolumes` / `ListAccounts` (ADR-003, superseding
ADR-002), not the REST `/aggregate/balances` PIT path — so [formancehq/ledger#1416](https://github.com/formancehq/ledger/issues/1416)
(PIT + metadata empty under `ACCOUNT_METADATA_HISTORY: DISABLED`) is off recon's read path.
