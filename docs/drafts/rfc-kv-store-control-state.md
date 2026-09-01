# RFC: A generic K/V store for reconciliation's control state (Ledger 3.1)

Status: ⏳ **Draft / forward-looking** — tracked in [EN-1932](https://formance-team.atlassian.net/browse/EN-1932). Depends on the planned Ledger 3.1 generic K/V store for satellite services (CAS + atomic batches), which does not exist yet. Reviews where that primitive would help the Ledger V3-native reconciliation (`feat/reconciliation-ledger-v3`). Companion to [rfc-ledger-native-storage.md](./rfc-ledger-native-storage.md) and [audit-chain-v3.md](../technical/audit-chain-v3.md).

---

## 1. Context

Ledger 3.1 plans a **generic key-value store for satellite services**, with **compare-and-swap (CAS)** and **atomic batches**. Reconciliation on the V3-native branch currently stores *all* of its control state — rule definitions, alert lifecycle, (future) closures — as accounts + typed metadata on the `_recon` control ledger, using the ledger's **transaction** primitives. This RFC reviews, concretely, where a native K/V store would help, with the driving use case being **updatable rules** (aligning with reconciliation 2.4.1).

## 2. The current model: the transaction log as a makeshift CAS store

Reconciliation stores two very different kinds of state on the same transaction-oriented ledger:

**Immutable audit trail** — captures (evaluation receipts), alert transitions as events, the activity timeline. These are genuinely append-only transactions and belong on the ledger's log (+ its audit chain — see [audit-chain-v3.md](../technical/audit-chain-v3.md)). **Good fit; leave as-is.**

**Mutable control state** — rule definitions, current alert `status`/`ack`/`resolution`/`snooze`, and (planned) closures. These are *mutated* over their lifetime. The ledger has no native mutable-with-CAS primitive, so reconciliation simulates one:

- **Rule definitions** = last-write-wins typed metadata on the `rule:{id}` account (`SaveAccountMetadata`, `internal/ledgerstore/rule.go`). An update overwrites; there is **no concurrency guard** — the code explicitly notes *"a guarded create would cost a round-trip"* and skips it. Two concurrent `PATCH`es race and the last writer wins silently. The `revision` is a content hash; there are no first-class `rev:{n}` records (revisions are reconstructed from the activity stream).
- **Alert lifecycle state** = an EPHEMERAL `ALERT` marker account moved between `st:{state}` addresses by a guarded Numscript, where **the bare source acts as compare-and-swap** — the batch fails if the marker is not at the expected state (`internal/ledgerstore/alert_transition.go` `moveMarker`, `internal/ledgerstore/alert.go`). This is a clever but indirect CAS: it needs marker accounts, EPHEMERAL purging, a `status` metadata mirror, and a Numscript per transition.

In short: **reconciliation is already doing CAS and atomic multi-key writes — it just expresses them through balance guards and EPHEMERAL markers, because that is the only compare-and-swap the transaction model offers.**

## 3. Where a native K/V store (CAS + atomic batches) helps

| Control state | Today (transaction model) | With a K/V store |
|---|---|---|
| Rule definition + update | LWW metadata upsert, **no concurrency guard** — 2.4.1 updatable-rule parity is unsafe under concurrent edits | `CAS(rule:{id}, expectedVersion, newDef)` → safe optimistic-concurrency updates; a stored version replaces the content-hash revision |
| Rule revisions | reconstructed from the activity stream; no `rev:{n}` | first-class `rule:{id}:rev:{n}` keys, directly addressable |
| Alert `status`/`ack`/`resolution`/`snooze` | EPHEMERAL marker moved by guarded Numscript (bare source = CAS) + a status-metadata mirror | `CAS(alert:{id}:state, from, to)` on a plain value — removes the marker accounts, the EPHEMERAL purge, the per-transition Numscript, and the mirror |
| Multi-field transition (status + resolution + snooze together) | one atomic Apply batch (CreateTransaction + metadata set/delete) | one K/V atomic batch of key writes |
| Closure single-open invariant ([audit-chain-v3](../technical/audit-chain-v3.md) W2/W5) | proposed `_recon` marker-account CAS | `CAS(closure:open, …)` on a plain key |

## 4. The reconciliation 2.4.1 alignment — the driver

Reconciliation 2.4.1 supports **updating rules**. On V3 today, a rule update is an unguarded LWW metadata overwrite: two concurrent edits silently clobber each other, and there is no optimistic-concurrency token. A generic K/V **CAS** gives exactly the primitive needed — *update the rule only if its stored version still matches* — so reconciliation can offer safe, concurrent rule updates that align with 2.4.1 **without** bolting the marker/guard machinery onto rule storage. This is the single clearest place the K/V store pays for itself.

## 5. The clean split

The K/V store does **not** replace the ledger's transaction log for reconciliation — it complements it:

- **Mutable control state** (rule defs + versions, alert lifecycle, closures) → **K/V store** (CAS + atomic batches).
- **Immutable audit trail** (captures, transitions-as-events, the hash chain) → **the ledger's transaction / audit log** (see audit-chain-v3.md).

This separation is the real architectural win. Today both are forced through the transaction model, which is *why* mutable state needs the marker / EPHEMERAL / Numscript workarounds.

## 6. What it means concretely (the evolution)

- Replace `ledgerstore`'s rule metadata upsert with K/V CAS writes plus a `version` field; revisions become `rev:{n}` keys.
- Replace the alert marker / EPHEMERAL / guarded-Numscript transition machinery with a K/V CAS on `alert:{id}:state` and an atomic batch for the mirrored fields — while keeping the transition **event** as an immutable ledger transaction (the audit trail).
- Closures ([audit-chain-v3](../technical/audit-chain-v3.md) W2/W5) use K/V CAS for the single-open invariant.
- Reconciliation-V3 is pre-GA, so this is a **refactor of the `ledgerstore` layer, not a data migration**.

## 7. Open questions

- **Atomicity across the two stores.** Does the 3.1 K/V store share a cluster/namespace with the `_recon` ledger so that a K/V CAS and a ledger transaction can commit in **one** atomic batch? Several transitions mutate control state (K/V) *and* append an audit event (ledger tx) that must land together — if the atomic batch cannot span both, reconciliation needs a saga/recovery.
- **Value shape + typing.** Rule defs are JSON blobs; alert state is small scalars. What value size / typing does the K/V store offer?
- **Query surface.** Listing rules and filtering alerts today rides the ledger's metadata forward-index. Does the K/V store offer range/prefix scans + secondary indexes, or does reconciliation keep the metadata-index approach for reads and use K/V only for the mutable-write path?
- **Dependency + sequencing.** This is gated on the Ledger 3.1 K/V store shipping; until then, the marker/EPHEMERAL model is the correct V3-native approach.
