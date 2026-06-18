# Technical Documentation

Engineering reference for the V1 reconciliation work. Start with the diff doc if you're new to the project; use the topical docs as reference once you have the shape.

## Documents

| Document | What it covers |
|---|---|
| [v1-vs-legacy.md](./v1-vs-legacy.md) | The **diff** between today's `/policies` behaviour and the V1 model. Read first. |
| [architecture.md](./architecture.md) | How `internal/engine/`, `internal/templates/`, `internal/storage/`, and the service layer fit together. |
| [api.md](./api.md) | Legacy `/policies` API (preserved as a facade) **and** the V1 `/rules` / `/incidents` API. |
| [workflows.md](./workflows.md) | Lifecycle flows: rule create → evaluate → incident → resolve / accept, plus re-open and engine-error paths. |
| [templates.md](./templates.md) | The V1 GA template catalog — spec shapes, validation rules, what each compiles to, evidence format. |
| [local-dev.md](./local-dev.md) | Bringing up the isolated local stack (`reconciliation/local/`), smoke tests, the auth-mock Caddy snippet. |

## Status legend used throughout

- ✅ **Shipped** — merged on main, exercised by tests
- 🚧 **In progress** — actively being written; subject to change before merge
- ⏳ **Planned** — design committed but not yet implemented; see the V1 build status in [docs/README.md](../README.md)
- ❌ **Not in V1** — explicit V1.1 / V2 carve-out

When a doc references a piece of code that doesn't exist yet, it's marked ⏳ inline.
