# Technical Documentation

Engineering reference for the V1 reconciliation work. Start with the diff doc if you're new to the project; use the topical docs as reference once you have the shape.

## Documents

| Document | What it covers |
|---|---|
| [v1-vs-legacy.md](./v1-vs-legacy.md) | The **diff** between today's `/policies` behaviour and the V1 model. Read first. |
| [architecture.md](./architecture.md) | How `internal/engine/`, `internal/templates/`, `internal/storage/`, and the service layer fit together. |
| [api.md](./api.md) | Legacy `/policies` API (preserved as a facade) **and** the V1 `/rules` / `/alerts` API. |
| [workflows.md](./workflows.md) | Lifecycle flows: rule create → evaluate → alert → resolve / accept, plus reopen and engine-error paths. |
| [templates.md](./templates.md) | The V1 GA template catalog — spec shapes, validation rules, what each compiles to, evidence format. |
| [alert-period-model.md](./alert-period-model.md) | Why alerts are scoped by reconciliation period (cadence), and how it's implemented. |
| [scheduler.md](./scheduler.md) | The PostgreSQL-backed worker scheduler, multi-pod claims, catch-up, retries, and delivery guarantees. |
| [notification-suppression.md](./notification-suppression.md) | Why a still-broken alert stops re-paging on every scheduler tick — the `notify` flag, and "suppress the message, not the record". |
| [audit-chain.md](./audit-chain.md) | The tamper-evident journal: what is hashed and why, period seals and their Ed25519 signatures, what tampering it detects and the one thing it cannot, plus what Ledger V3 subsumes. |

## Status legend used throughout

- ✅ **Shipped** — merged on main, exercised by tests
- 🚧 **In progress** — actively being written; subject to change before merge
- ⏳ **Planned** — design committed but not yet implemented; see the V1 build status in [docs/README.md](../README.md)
- ❌ **Not in V1** — explicit V1.1 / V2 carve-out

When a doc references a piece of code that doesn't exist yet, it's marked ⏳ inline.
