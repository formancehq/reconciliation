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

**The first step is to *sign the writes*, not to build closures.** The ledger already exposes per-batch Ed25519 signing (`SignedApplyBatch`) and a dense `audit_sequence`; together they give an external auditor everything they ask — *did the control run, is anything missing, was anything changed* — verifiable from a public key alone. Closures were first in the original plan only because the Postgres design had no native chain; on V3 they become a later, separable phase that adds a compact per-period *summary* + a freeze. See §6.

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

## 6. Proposed design — phased

**The external-auditor guarantee does not need closures.** What an auditor actually asks — *did the control run, is anything missing, was anything changed* — is answered directly by the ledger's **native per-batch Ed25519 signing** plus its **dense `audit_sequence`**. So the first step is to sign the writes and hand the auditor a recipe; the closures/seals machinery (a self-contained per-period *summary* + a freeze) is a later, separable phase. This reverses the original ordering, which put closures first because the Postgres design had no native chain to lean on.

> Mapping to the original workstreams: Phase 1 folds in old **W3** (sign) + **W4** (attribution) + **W1** (surface/cite); Phase 2 is old **W2** + **W5** (closures); **W6** (surfacing stubs) is independent.

### Phase 1 — External verifiability by signing the writes (the first step)

The whole external guarantee, from a public key alone, with none of the seal machinery.

- **P1.1 — Sign every `_recon` write.** Register a reconciliation Ed25519 key with the ledger (`RegisterSigningKey`) and send each `ApplyBatch` as a `SignedApplyBatch` (`ApplyRequest.signed`). The public key is retrievable via the ledger's `ListSigningKeys`; the signature is bound into the ledger's own audit chain. Any dropped, reordered, altered, or forged record fails verification — and nobody without reconciliation's private key can re-sign one.
- **P1.2 — End-user attribution in the memento.** The ledger's `CallerSnapshot` records *reconciliation's* service identity, so the human who ack'd/resolved must be bound **inside** the signed payload: `subjectMiddleware` (inside the authed group) reads the verified token subject; the write path binds a subject snapshot (subject + one-byte source tag) into the metadata value. Demote the self-declared `by` to a note. Without this the signing proves *reconciliation* acted, but loses *who*.
- **P1.3 — Serve the signed entries + audit sequence.** Surface the ledger's `audit_sequence` on captures/alerts/transitions, and expose the `_recon` audit entries (sequence + payload + signature) through a read endpoint so an auditor needs no ledger credentials. An optional `POST /audit-entries/verify` façade over the ledger's `CheckStore` is a *convenience*, not the trust root — the auditor verifies the signatures themselves.
- **P1.4 — Publish the verification recipe.** One page + a reference script: get the public key, read the entries in sequence order, check for gaps (completeness), verify each Ed25519 signature over its payload (authorship + integrity). Reproducible in any language, no involvement from us.

### Phase 2 — Business-period attestation via signed closures (later)

Closures add value the raw signed stream does not, and only then:

- **P2.1 — Signed closures.** A contiguous sealed segment citing the ledger's chapter head (`close_audit_sequence` / `last_audit_hash`) + a per-period breakdown, with an **unkeyed** closing hash reproducible from published fields and an Ed25519 signature — a compact, self-contained artifact an auditor verifies **without replaying the stream**. Argument-less close, successor opened in the same transaction, only **ended** business periods freeze (so `continuous` keeps running). Expose `GET /audit-signing-keys`, `GET /periods/{id}`, `POST /closures/{id}/verify`. Reuse `internal/audit/{seal,keys,canonical,memento}.go` verbatim.

### Independent — Surfacing (any time)

- **W6 — Fill the stubs.** the per-alert `/events` endpoint (currently an empty page) and `GET /rules/{id}/revisions(/{n})`.

## 7. Reuse from `feat/audit-chain`

The crypto and canonical-form code is **storage-agnostic and reusable verbatim**: `internal/audit/{canonical,memento,seal,keys}.go` (JCS-inspired canonical form, the frozen `bytea`→metadata-value memento, the closing-hash + Ed25519 seal, the salt/pepper key derivation). Only `internal/storage/*.go` (the Postgres tables, triggers, advisory-lock write path, migrations) is discarded — its role is exactly what W1 delegates to the ledger.

The **memento is the bridge** the Postgres design invested in for this moment: the business payload we put inside the metadata value, replayable in logical-sequence order.

## 8. Open questions / risks

**Phase 1 (signing):**
- **Signing-key custody** — one Ed25519 key, private half held by reconciliation, public half registered with the ledger and served to auditors. Load from `--audit-signing-key-seed` (a secrets manager); keep it out of logs and out of `_recon`. Decide rotation (the ledger's `RegisterSigningKey` supports multiple keys; retired publics stay verifiable).
- **`require_signatures`** — whether to also flip the ledger's `SetSigningConfig` so unsigned writes to `_recon` are *rejected*, not merely unsigned. Stronger, but makes the key load-bearing for availability.
- **Auditor read-access** — the auditor needs the audit entries (sequence + payload + signature). Serve them through a recon endpoint (P1.3) so they need no ledger credentials; confirm `audit_sequence` + the batch signature are on the client read path.

**Phase 2 (closures):**
- **Closure ↔ ledger chapter** — decide whether a recon closure *is* a ledger `CloseChapter` on `_recon`, or a recon-level artefact that merely cites the current chapter boundary. The former couples cadence to the ledger's chapter rotation; the latter keeps reconciliation in control of the business-period breakdown.
- **Single-open-closure invariant** — Postgres enforced it with a partial unique index; on V3 there is no recon-side single-writer. Decide where the invariant lives (a `_recon` marker account CAS, mirroring the alert-state markers).
