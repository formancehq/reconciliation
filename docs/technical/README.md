# Technical Documentation

Engineering reference for the reconciliation service. There is one API surface, mounted
unversioned at `/rules` and `/alerts`; the V1 contract and its three positional templates were
retired once `balance_bounds` gave the last of them a target, and the `/v2` prefix went with it. Start with the architecture doc if you're new to the project; use the
topical docs as reference once you have the shape.

## Documents

| Document | What it covers |
|---|---|
| [architecture.md](./architecture.md) | How the CEL kernel, templates, the ledger-native store (`_recon`), and the service layer fit together — plus the account model + transition workflow. |
| [ledger-v3-storage.md](./ledger-v3-storage.md) | Exact Ledger v3 account types, assets, Numscript programs, indexes, queries, provisioning, and timeline persistence. |
| [api.md](./api.md) | The API surface, rule/evaluation/alert contracts, and the events sink. |
| [workflows.md](./workflows.md) | Lifecycle flows: versioned rule create → live evaluation + capture → alert → resolve / accept, plus reopen, engine-error, and event delivery. |
| [templates.md](./templates.md) | The template catalogue — spec shapes, exact arithmetic, validation, fingerprints, and evidence. |
| [alert-period-model.md](./alert-period-model.md) | Why alerts are scoped by reconciliation period (`periodType`), and how it's implemented. |
| [scheduler.md](./scheduler.md) | The in-process cron scheduler — how rules fire automatically, and the single-instance caveat. |
| [notification-suppression.md](./notification-suppression.md) | Why a still-broken alert shouldn't re-page on every tick — and why suppression now lives at the consumer. |
| [audit-chain-v3.md](./audit-chain-v3.md) | ⏳ Planned. Reviews the abandoned Postgres audit journal (PR #94) against the Ledger V3-native architecture — what the ledger subsumes, what stays reconciliation's (external Ed25519 attestation + `/verify` + closures), and the proposed workstreams. |
| [stale-holds.md](./stale-holds.md) | The `stale_holds` time-based control: where an age signal can come from now that ledger reads are live-only, why the clock stays out of the kernel, and the hold-modelling assumptions the template rests on. |
| [ADR-004](../prd/adr-004-multi-source-comparisons.md) | Why V2 is additive, how equations and exchange rates are defined, and why arithmetic never uses floating point. |

## Status legend used throughout

- ✅ **Shipped** — merged on main, exercised by tests
- 🚧 **In progress** — actively being written; subject to change before merge
- ⏳ **Planned** — design committed but not yet implemented; see the V1 build status in [docs/README.md](../README.md)
- ❌ **Not in V1** — explicit V1.1 / V2 carve-out

When a doc references a piece of code that doesn't exist yet, it's marked ⏳ inline.
