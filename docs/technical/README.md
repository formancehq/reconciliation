# Technical Documentation

Engineering reference for the V1 reconciliation work. Start with the architecture doc if you're new to the project; use the topical docs as reference once you have the shape.

## Documents

| Document | What it covers |
|---|---|
| [architecture.md](./architecture.md) | How the CEL kernel, templates, the ledger-native store (`_recon`), and the service layer fit together — plus the account model + transition workflow. |
| [api.md](./api.md) | The V1 `/rules` / `/alerts` API + the events sink (evaluations are non-durable). |
| [workflows.md](./workflows.md) | Lifecycle flows: rule create → evaluate (at a checkpoint) → alert → resolve / accept, plus reopen, engine-error, and event delivery. |
| [templates.md](./templates.md) | The V1 GA template catalog — spec shapes, validation rules, what each compiles to, evidence format. |
| [alert-period-model.md](./alert-period-model.md) | Why alerts are scoped by reconciliation period (cadence), and how it's implemented. |
| [scheduler.md](./scheduler.md) | The in-process cron scheduler — how rules fire automatically, and the single-instance caveat. |
| [notification-suppression.md](./notification-suppression.md) | Why a still-broken alert shouldn't re-page on every tick — and why suppression now lives at the consumer. |

## Status legend used throughout

- ✅ **Shipped** — merged on main, exercised by tests
- 🚧 **In progress** — actively being written; subject to change before merge
- ⏳ **Planned** — design committed but not yet implemented; see the V1 build status in [docs/README.md](../README.md)
- ❌ **Not in V1** — explicit V1.1 / V2 carve-out

When a doc references a piece of code that doesn't exist yet, it's marked ⏳ inline.
