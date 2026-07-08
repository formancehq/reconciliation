# ADR-003 — Live reads + immutable `_recon` capture (query checkpoints removed)

**Status:** Accepted (implemented — see [migration log](../drafts/ledger-v3-migration-log.md), "checkpoint alternative" workstream). Supersedes the Tier-1 aligned-checkpoint model of [ADR-002](./adr-002-pit-consistency.md); narrows [ADR-001](./adr-001-cel-kernel.md) §11.
**Linked from:** [ADR-002](./adr-002-pit-consistency.md), [RFC §4.4.2/§4.5](../drafts/rfc-ledger-native-storage.md)
**Last updated:** 2026-07-08

---

## 1. Decision in one sentence

Reconciliation reads its data ledgers **live** (no query checkpoints) and records each evaluation as an **immutable capture transaction** in the control ledger `_recon`; cross-ledger skew is absorbed by tolerance, and the runtime kernel/template cross-check is removed.

---

## 2. What prompted this

Since the ledger-native migration (ADR-002), an evaluation pinned **one query checkpoint** and read every ledger source at it — a globally consistent cross-ledger cut. In practice this was a poor fit for reconciliation:

- A query checkpoint is **cluster-wide** (`db.Checkpoint()` hard-links the SSTs of the entire bucket store — all ledgers, all accounts), yet a rule reads only 2–3 account sets. It cannot be scoped.
- It is **heavyweight**: two Raft ops per evaluation (create + delete), an async read-index materialization wait (~100–130 ms), SST pinning that blocks cluster compaction, and lifecycle/reaper machinery — paid on **every** evaluation, including continuous ones firing every *T* seconds.
- It was created and **destroyed per evaluation**, so the reproducibility it could offer was never realized.

Using a full cluster freeze to atomically read a handful of accounts is a large cost/benefit mismatch. The owner steered the design toward an **alternative to checkpoints** (no hybrid, no checkpoint fallback).

---

## 3. Consistency & atomicity — what a checkpoint actually bought (verified in the ledger source)

The only thing a checkpoint uniquely provided reconciliation is a **skew-free cross-*read* cut**. "Skew" = the temporal gap between two *separate* read RPCs; it never occurs inside one read. The landscape:

| Rule's account universe | Atomic without a checkpoint? | Mechanism |
|---|---|---|
| 1 ledger, 1 query (any number of accounts) | **Yes, natively** | one `AggregateVolumes` = one `readStore.NewSnapshot()` |
| 1 ledger, sub-sets by **address prefix** | **Yes, one call** | `AggregateVolumes` + `group_by_prefixes` (not yet used) |
| 1 ledger, sub-sets by **metadata** (2 queries) | No | two snapshots → possible skew |
| **Multiple ledgers** | **No — only a checkpoint** | ledgers share one log; only a checkpoint freezes them together |

So skew — hence any need for atomicity — arises **only** for multi-ledger rules and single-ledger multi-metadata-query rules. Everything else is atomic for free. For those two cases we accept **per-source reads + tolerance**: a period close reconciles settled state (stable regardless of read instant); continuous monitoring self-corrects on the next tick and tolerance absorbs the transient. `min_log_sequence` (a live-read freshness floor) is available but not required.

---

## 4. Decision A — remove the runtime kernel/template cross-check

The three aggregate templates (`source_parity`, `ledger_invariant`, `account_threshold`) re-ran their per-asset check through the CEL kernel and asserted it matched the direct `big.Int` math. Under any single anchored read both paths read identical state, so the check only re-verified arithmetic equivalence on identical inputs — at 2·N / T·N / N extra reads per evaluation — and it structurally blocked live reads (two live reads of the same expression can diverge under concurrent writes).

Direct math is now authoritative. The rendered CEL stays in `evidence.compiledCEL` (explainability) and `eng.Compile` stays at rule-create (validation). `eng.Evaluate` no longer runs built-in templates — it is reserved for the post-GA raw-CEL power mode (a documented narrowing of ADR-001 §11). The equivalence property moved to a golden test.

---

## 5. Decision B — live reads, no checkpoints

`EvaluateRule` no longer acquires a checkpoint. Ledger sources read live (`checkpointID = 0`, internally consistent per read). Consistency is per-source (§3), skew absorbed by `tolerance`. All checkpoint machinery is removed: create/delete, the readiness wait, the SST-pinning lifecycle, the crash-orphan reaper, and the `checkpointID` anchor across the engine, templates, resolver, and client read helpers.

**Findings closed:** F26 (checkpoint lifecycle/reaper) and F32 (checkpoint read-index race) are now without object.

---

## 6. Decision C — audit-grade capture in `_recon`

Every evaluation records an **immutable capture transaction** on the control ledger — the durable "what reconciled and when": positive assurance on a pass, break evidence on a fail, recorded independently of the alert lifecycle.

- **Chart**: `capture:rule:{ruleId}:per:{period}` (NORMAL) bucket + `capture:pool:rule:{ruleId}` (NORMAL) mint source; asset `CAPTURE` (precision 0); numscript `capture` mints one `CAPTURE` from the pool into the bucket. `−balance(bucket, CAPTURE)` counts captures for the (rule, period); **one evaluation = one transaction**, so the bucket's transaction log is the period's ordered series of captures.
- **Snapshot**: the observed state rides the **transaction metadata** (`COMMITTED_TRANSACTION`, self-describing: `type, rule_id, template_kind, period, evaluation_id, captured_at, verdict, trigger, evidence`). The transaction is immutable and receipt-signed — the audit record. The address is a bucket; the transaction carries the distinguishing context (`evaluation_id`, `captured_at`, `trigger`).
- **Idempotent** per (rule, period, evaluation): a gRPC retransmit dedups; a genuinely new evaluation gets a fresh key.

This **revises the "evaluations non-durable" decision** (RFC §4.4.2): the durable record is the capture transaction (ledger-native, immutable), not a queryable Postgres evaluation table.

---

## 7. What we keep, what we lose

- **Kept**: exactness where it exists for free (single-ledger reads are atomic); tolerance-bounded consistency for continuous/heterogeneous (ADR-002 §5); durable audit — now **stronger** (immutable, receipt-signed capture) and covering passes, not just breaks.
- **Lost**: **replay by re-reading a past cut** — the checkpoint was the only thing that offered it, and it discarded it anyway (deleted per eval). The recorded capture (the numbers) is the audit substrate, per ADR-002 §8. **Provable simultaneous atomicity** for multi-ledger rules — replaced by per-source + tolerance.

---

## 8. Future / upstream (documented, not implemented)

- **EN-1480** ([atomic multi-ledger read](https://formance-team.atlassian.net/browse/EN-1480)) — a ledger RPC that aggregates N `(ledger, query)` items against **one** ephemeral snapshot, returning a certifiable cut (log sequence + audit hash + signed receipt). This — not a "scoped checkpoint" — is the right primitive if provable multi-ledger atomicity becomes a hard requirement. It is server-side by construction (a client cannot hold a snapshot across RPCs, nor sign with the ledger key).
- **`group_by_prefixes`** — make single-ledger, prefix-partitioned parity/invariant atomic in one call.
- **Log-fold projections** — maintain scoped running balances from the event stream (atomic cut without a checkpoint) — deferred: reintroduces durable state, high event volume, and a metadata-scope correctness trap.
- **Capture enhancements** — throttle continuous captures (on-change / heartbeat) if volume bites; store full pass-side observed balances (all outcomes, budget-bounded); attach a ledger-signed read proof (EN-1480).

---

## 9. Consequences

1. `EvaluateRule` acquires no checkpoint; ledger reads are live; the capture is written inside the evaluation.
2. Built-in template evaluation is typed Go over a single anchored read; CEL is authoring-surface + validation + explanation (ADR-001 §11 narrowed).
3. Reconciliation stays stateless in process; its durable state (rules, alerts, captures) lives entirely in `_recon`.
4. Customer language stays honest (ADR-002 §4): atomic-where-free + tolerance-bounded across sources; every evaluation leaves an immutable, receipt-signed audit record.

---

## 10. Relationship to ADR-001 / ADR-002

- **ADR-001** (CEL kernel): unchanged as the authoring model; §11's "CEL is the runtime evaluator for built-ins" is narrowed — built-ins evaluate in typed Go, CEL runs only in the future power mode.
- **ADR-002** (consistency): its **Tier-1 aligned-checkpoint** anchor is **retired** by this ADR. Its Tier-2 (per-source PIT + tolerance) reasoning now applies uniformly to the cases that need a cut (multi-ledger); §7 (checkpoint cadence) and the checkpoint lifecycle commitments are obsolete.
