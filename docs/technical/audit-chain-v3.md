# The Audit Journal on Ledger V3

Status: ⏳ **Planned** — design review. Tracked in [EN-1930](https://formance-team.atlassian.net/browse/EN-1930). No audit-journal code has shipped on `feat/reconciliation-ledger-v3` yet; the Postgres implementation ([PR #94](https://github.com/formancehq/reconciliation/pull/94), branch `feat/audit-chain`) is **closed/never merged**, so this is greenfield.

This doc reviews the abandoned Postgres audit-chain feature against the Ledger V3-native architecture and proposes how to rebuild the parts that still matter. It is the companion to [audit-chain.md](https://github.com/formancehq/reconciliation/blob/feat/audit-chain/docs/technical/audit-chain.md) (the Postgres design) and builds on [ledger-v3-storage.md](./ledger-v3-storage.md) and [ADR-003](../prd/adr-003-checkpoint-anchor-and-crosscheck.md).

---

## 1. The question

PR #94 added a tamper-evident, hash-chained journal to reconciliation — so the history it keeps becomes history it can **prove** — modelled on the Ledger V2 Postgres log. On the V3-native branch reconciliation has no Postgres: all state lives as Numscript-batch transactions + typed account metadata on the `_recon` control ledger. So:

> Do we already have, in Ledger V3, everything needed for the audit features PR #94 describes — and if not, what stays reconciliation's to build?

## 2. Verdict

**The audit *storage, immutability, and hash chain* are essentially free on V3, and stronger than the Postgres version. The *externally-verifiable attestation* — an outside auditor validating history from a public key with no involvement from us — is the real remaining work, and it is much thinner on V3 than the Postgres build.**

Roughly **two-thirds** of PR #94 is subsumed by the ledger. The **one-third** that stays reconciliation's (external Ed25519 attestation + a `/verify` façade + closures + token-derived attribution + revision/event surfacing) is genuine but small, and its storage-agnostic crypto already exists on `feat/audit-chain`.

## 3. What Ledger V3 subsumes (verified against the ledger, not just claimed)

Evidence from `github.com/formancehq/ledger` `release/v3.0`:

| Audit-chain piece (Postgres) | On V3 | Verified reality |
|---|---|---|
| Hash chain (keyed-BLAKE3) | **Subsumed** | The ledger maintains a **keyed-BLAKE3 audit chain** — key derived from the immutable ClusterID (`internal/domain/processing/hash_blake3.go`), one `AuditEntry` per proposal, hashing the `audit_sequence`, the `CallerSnapshot`, the batch Ed25519 signature, and each item's serialized *business intent* (schema-drift-proof). `misc/proto/audit.proto`, `internal/infra/state/audit_envelope.go`. Raft-replicated. |
| Dense sequence + `pg_advisory_xact_lock` | **Subsumed, and better** | Raft's single-writer FSM assigns `audit_sequence` with **no lock and no write serialization** — the one real cost of the Postgres design disappears. |
| Immutability trigger + `REVOKE` | **Subsumed** | Pebble rows are projections of the audit log; there is no UPDATE path to guard. |
| Tail-truncation blind spot (audit-chain.md §3) | **Closed** | The chain is Raft-replicated; truncating one node's tail contradicts the others. |
| µs `timestamptz` truncation bug | **Gone** | Protobuf timestamps, no `timestamptz`. |
| Server-side chain checker | **Available to cite** | The ledger ships a real verifier — `CheckStore` (`internal/application/check/checker.go`) — that recomputes the keyed chain and cross-checks ~20 projections. |

## 4. What stays reconciliation's — the gap

The ledger's chain and seal are **keyed** commitments, verifiable only by the ledger (which holds the ClusterID) or through its internal `CheckStore`. Specifically:

- The chapter `sealing_hash` is **unkeyed BLAKE3 over `chapter_id ‖ close_sequence ‖ last_audit_hash ‖ state_hash`** with **no signature** (`internal/infra/state/sealer.go`). Because it anchors on `last_audit_hash` (the tip of the *keyed* chain), an external party needs the ClusterID to verify it.
- **Receipts are HMAC-SHA256** (symmetric, one shared secret) — `internal/infra/receipt/receipt.go`. The ledger's own docs note no external auditor needs to verify a receipt.
- The ledger exposes **no public verify RPC**.

So the "black-box" objection PR #94 exists to answer — *don't make the client's auditor trust our verify endpoint* — is **exactly the property Ledger V3 does not provide**. This is what reconciliation must still build.

### 4.1 The asset the Postgres design under-leveraged

The ledger *does* have Ed25519 — for **per-batch authorship**: `SignedApplyBatch` (`misc/proto/signature.proto`), signing keys registered via `RegisterSigningKey` with public keys retrievable via `ListSigningKeys`/`Discovery`. The batch signature is stored on `AuditEntry.signature` and bound into the chain.

**If reconciliation signs its `ApplyBatch` to `_recon` with a registered Ed25519 key, every audit record becomes third-party-verifiable as authored by reconciliation, from a public key alone.** That is genuine external non-repudiation of *who wrote each record* — a real partial answer to the gap that the audit-chain.md §6 table omits.

### 4.2 The one thing that must not be lost in translation

V3's `CallerSnapshot` records the caller **as the ledger sees it**. Once reconciliation is a ledger client, that is *reconciliation's own service identity* — every entry would read "made by the reconciliation service" and the end user would vanish. So the **end-user subject must stay in reconciliation's own memento**, hashed as part of the payload the ledger binds. Relying on the ledger's caller snapshot alone would silently destroy the attribution this feature exists to establish (`CallerSnapshot` = `common.proto:1780-1805`).

## 5. What recon-on-V3 already has today

| Dimension | Status | Where |
|---|---|---|
| Immutable captures incl. passes, queried live as ledger transactions, carrying `periodID` | ✅ | `internal/ledgerstore/capture.go`; ADR-003 §6 |
| Rule-scoped activity timeline (`GET /rules/{id}/timeline`) — revisions (as frozen snapshots) + evaluations + alert transitions + snoozes | ✅ | `internal/ledgerstore/activity.go`, PR #90 |
| Delete is history-safe — clears the live rule, keeps capture/activity history, writes a `rule.deleted` snapshot (not a cascade) | ✅ | `internal/ledgerstore/rule.go` |
| Immutability delegated to the ledger (recon computes no chain of its own) | ✅ (correct for V3) | ADR-003 §6; `architecture.md` |
| Ledger `audit_sequence` surfaced in the recon API | ❌ | recon uses the ledger **tx id**, not the log's `audit_sequence` |
| Token-derived actor attribution | ❌ | `by` is still caller-supplied in the request body (`internal/api/service/alert.go`) |
| Period closure / seal / closing write-barrier | ❌ | absent; `periodID` is only an alert scoping key |
| External Ed25519 attestation + `/verify` endpoint | ❌ | none |
| First-class `/rules/{id}/revisions` and per-alert `/events` | ❌ | `/events` is an empty stub (`internal/ledgerstore/adapters.go`); revisions only reconstructable from the timeline |

## 6. Proposed design — the workstreams (point by point)

Ordered by leverage. Each is independently shippable behind the existing `_recon` model.

### W1 — Cite the ledger's chain (biggest win, verified feasible)
Surface the ledger's `audit_sequence` (from the committed log of each `_recon` write) on captures, alerts, and transitions — replacing the tx-id handle. Build `POST /audit-entries/verify` as a **thin façade over the ledger's `CheckStore` + `ListAuditEntries`**, scoped to the `_recon` ledger. Reconciliation stops maintaining a chain and starts **citing** one.

### W2 — External attestation via signed closures (the actual deliverable)
Implement reconciliation **closures**: a contiguous sealed segment that **cites the ledger's chapter head** (`close_audit_sequence` / `last_audit_hash`) plus a per-period breakdown, computes an **unkeyed** closing hash reproducible from published fields, and signs it with **Ed25519**. Expose `GET /audit-signing-keys` (public keys, retired ones included) and `POST /closures/{id}/verify`. This is the primitive the ledger does not provide.

### W3 — Sign the writes (free authorship non-repudiation)
Register a reconciliation Ed25519 signing key with the ledger and sign the `ApplyBatch` to `_recon`. Every audit record is then externally verifiable as authored by reconciliation (§4.1). Complements W2.

### W4 — Token attribution in the memento
Add `subjectMiddleware` **inside** the authenticated route group; bind a subject snapshot (token subject + one-byte source tag: issuer / client / system / none, sorted scopes) into reconciliation's memento (§4.2). Demote the self-declared `by` to a human-readable note. Makes `?actor=system|human` a claim worth trusting.

### W5 — Closures, not seals
Adopt the argument-less closing model the branch already evolved to (`feat(audit): replace period seals with closures`): one open closure, closing closes it and opens its successor in the same transaction, and only **ended** business periods freeze — so `continuous` keeps running and weekly/monthly rules can be attested by the same closing. On V3 this maps cleanly to the ledger's `CloseChapter` (which discards its request payload outright). Optional cadence via a stored `PUT /closing-schedule` cron.

### W6 — Fill the surfacing stubs
Implement the per-alert `/events` endpoint (currently returns an empty page) and `GET /rules/{id}/revisions(/{n})` — revisions as `rule:{id}:rev:{n}` accounts, since ledger metadata is scalar last-write-wins with no native append.

## 7. Reuse from `feat/audit-chain`

The crypto and canonical-form code is **storage-agnostic and reusable verbatim**: `internal/audit/{canonical,memento,seal,keys}.go` (JCS-inspired canonical form, the frozen `bytea`→metadata-value memento, the closing-hash + Ed25519 seal, the salt/pepper key derivation). Only `internal/storage/*.go` (the Postgres tables, triggers, advisory-lock write path, migrations) is discarded — its role is exactly what W1 delegates to the ledger.

The **memento is the bridge** the Postgres design invested in for this moment: the business payload we put inside the metadata value, replayable in logical-sequence order.

## 8. Open questions / risks

- **`audit_sequence` exposure through the module's gRPC client** — the recon `ledger.Client` must read the ledger's per-log `audit_sequence`; confirm it is on the `Transaction`/`Log` read path the client already uses (W1's prerequisite).
- **Closure ↔ ledger chapter** — decide whether a recon closure *is* a ledger `CloseChapter` on `_recon`, or a recon-level artefact that merely cites the current chapter boundary. The former couples cadence to the ledger's chapter rotation; the latter keeps reconciliation in control of the business-period breakdown.
- **Pepper vs ClusterID** — the Postgres design added an operator pepper so a stolen DB dump couldn't rebuild the chain. On V3 the chain key is the ClusterID (ledger-held); reconciliation's *own* differentiator is the Ed25519 seal key, so the pepper concern moves to signing-key custody (`--audit-signing-key-seed`, kept out of logs and out of the ledger).
- **Single-open-closure invariant** — Postgres enforced it with a partial unique index; on V3 there is no recon-side single-writer. Decide where the invariant lives (a `_recon` marker account CAS, mirroring the alert-state markers).
