# Drafts

V1.1+ ideas and EE+ "Finance Ops" notes. **Not committed scope.** Material here is exploratory — if it survives design-partner contact, it graduates into the PRD or a topical doc under [`technical/`](../technical/).

> Use this folder the way `ledger-v3-poc/docs/drafts/` is used: a working space for RFCs and forward-looking notes.

---

## V1.1 carve-outs

Documented in the main PRD ([§6.2 fast-follow catalog](../prd/README.md#62-v11-fast-follow-catalog)) but worth listing here as a working agenda:

| Item | What's needed |
|---|---|
| `account_inactivity` template       | `ledgerPostings(...)` source + `lastActivity(source)` builtin + `now()` |
| `posting_rate` template             | `postings(source).count` builtin |
| `metadata_invariant` template       | `accounts(source).all(a, has(a.metadata.X))` |
| `cross_account_ratio` template      | Per-account iteration + multi-source comparison |
| Snooze + flap suppression           | Alert-layer feature; new state + lifecycle transitions |
| Daily digest                        | Per-recipient aggregation owned in-module |
| fctl `rules explain` command        | Pretty-print `rule.explanation_cel` with spec context |

---

## V2 — EE+ "Finance Ops" pack

The strategic vector for the engine kernel — see [PRD §2](../prd/README.md#2-context--opportunity). Continuous reconciliation against corporate GLs (NetSuite / Sage / Xero / SAP).

Open questions:

- **Adapter sequencing.** NetSuite first? QuickBooks? Sage? Driven by design-partner demand once V1 lands.
- **Period semantics.** GL accounting periods (month-close, fiscal year) are first-class — the engine needs `Period` as an object-model type with builtins like `lastMonth()`, `thisFiscalYear()`.
- **Multi-entity `forEach`.** Trial-balance reconciliation per legal entity is a common shape.
- **Cross-ledger (Ledger v3).** A single rule comparing balances across multiple Formance ledgers becomes natural once v3 ships. Kernel-side it's just `ledgerSet(ledgerA, q1) vs ledgerSet(ledgerB, q2)`.

---

## Open product questions

| Question | Notes |
|---|---|
| Product name — Ledger Clarity vs Ledger Transparency | Awaiting input from Maxence / Clem + the three design partners |
| Acceptance expiry — auto-reopen the alert in place at `expiresAt`, or wait for the next eval to flip it? | Leaning auto-reopen at `expiresAt` (predictable timing for ops) — design partner feedback to confirm |
| Daily digest scope — per-recipient only, or per-team / per-ledger | Leaning per-recipient first |
| Scheduler host | Decided: separate PostgreSQL-backed worker with durable jobs and fencing |
| Raw CEL exposure post-GA | Likely design-partner-gated initially; mature into general EE access once builtin namespace stabilizes |

---

## RFC-shaped placeholders

When one of the above grows enough teeth to merit its own RFC, add it here as a sibling markdown file. Suggested template:

```markdown
# RFC — <Short title>

**Status:** Draft / Under review / Accepted / Rejected
**Owner:** <Name>
**Date:** YYYY-MM-DD

## Problem
## Proposal
## Tradeoffs
## Alternatives considered
## Open questions
```

Same shape as `ledger-v3-poc/docs/drafts/`.
