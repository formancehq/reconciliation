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

**The first step is to *sign the writes*, not to build closures.** The ledger already exposes per-batch Ed25519 signing (`SignedApplyBatch`) and a native audit trail. Signing answers, from a public key alone, *did the control run* and *was anything changed* — authorship + integrity of every record, with nobody from Formance in the loop. *Is anything missing* (completeness) is a genuinely separate guarantee whose anchor is the ledger, not the public key — the served sequence is bucket-wide and unsigned; see §9. Closures were first in the original plan only because the Postgres design had no native chain; on V3 they become a later, separable phase that adds a compact per-period *summary* + a freeze. See §6.

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

- **P1.1 — Sign every `_recon` write. ✅ done** (`internal/ledger/signing.go`, `client.go`, `cmd/ledger.go`; commit `fcda3fd5`). A reconciliation Ed25519 key is registered with the ledger (`RegisterSigningKey`) *before* provisioning, so the whole chain — provisioning included — is signed; each `ApplyBatch` is serialized, signed over those exact bytes, and sent as a `SignedApplyBatch` (`ApplyRequest.signed`). The ledger verifies every signed batch **unconditionally** (unknown key id or bad signature → rejected, independent of the `require_signatures` flag: `admission.resolveBatch`), so a write being accepted *is* a passed verification. Registration is idempotent by a `ListSigningKeys` pre-check, which is load-bearing, not cosmetic: the ledger accepts an *unsigned* `RegisterSigningKey` only while its keystore is still empty (the bootstrap window), so a blind re-register would be rejected as a missing signature on the second boot. `--audit-signing-key-seed` pins the key across restarts; empty generates one and logs its seed. Any dropped, reordered, altered, or forged record fails verification — and nobody without reconciliation's private key can re-sign one.
- **P1.2 — End-user attribution in the memento. ✅ core done (ack/resolve/accept).** The ledger's `CallerSnapshot` records *reconciliation's* service identity, so the human who ack'd/resolved is now bound **inside** the signed payload. `subjectMiddleware` (mounted right after `auth.Middleware`, `internal/api/subject.go`) reads the verified token subject — it decodes the bearer JWT's `sub` *without re-verifying*, safe because auth has already verified and rejected an invalid token upstream, so no keyset needs wiring here and it lights up on any auth-enabled deployment. `service.resolveActor` (`internal/api/service/actor.go`) then makes the verified subject the authoritative actor and records provenance as a `models.Actor{subject, source, declared}` on the `Ack`/`Resolution` — `source=token` (verified, authoritative) vs `source=declared` (self-declared `by`, demoted to a note and only accepted when there is no verified subject). It rides into the signed metadata via the existing `alertToMetadata`/`stampTransition` marshalling, and surfaces on the read path (`GET /alerts` → `ack.actor`/`resolution.actor`). In local dev (auth disabled) there is no token, so transitions record `source=declared` — the pre-P1.2 behaviour, now explicitly tagged. **Fast-follow:** snooze/unsnooze still take a raw `by` string (their store signatures pre-date the `Actor` model); threading the actor through them is a small follow-up.
- **P1.3 — Serve the signed entries + audit sequence.** Surface the ledger's `audit_sequence` on captures/alerts/transitions, and expose the `_recon` audit entries (sequence + payload + signature) through a read endpoint so an auditor needs no ledger credentials. Note *where the request signature lives*: it rides the ledger's per-proposal `AppliedProposal` / audit entry, **not** the `Log` message — `Log.response_signature` is the ledger's own *response* signing (a separate server→client mechanism), so the read path must pull the batch signature from the audit entry, not the log. An optional `POST /audit-entries/verify` façade over the ledger's `CheckStore` is a *convenience*, not the trust root — the auditor verifies the signatures themselves.
- **P1.4 — Publish the verification recipe.** One page + a reference script: get the public key, read the entries, verify each Ed25519 signature over its payload (authorship + integrity — reproducible in any language, no involvement from us). Completeness is a *separate* guarantee with a different anchor — the ledger, not this recipe — because the served sequence is bucket-wide and unsigned; see §9. The recipe should say so rather than imply a filtered-gap check proves nothing was dropped.

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
- **Signing-key custody** — one Ed25519 key, private half held by reconciliation, public half registered with the ledger and served to auditors. Load from `--audit-signing-key-seed` (a secrets manager); keep it out of logs and out of `_recon`. **Rotation is not a drop-in seed swap:** the ledger's *unsigned* bootstrap registration only works while the keystore is empty, so a fresh key introduced while an old one is still registered must go through the ledger's **signed** rotation path (`RegisterSigningKey` signed by an existing key, `parent_key_id` linking the lineage); retired publics stay verifiable. Today `RegisterConfiguredSigningKey` self-bootstraps the *first* key and is a no-op once present — wiring the signed rotation path is the follow-up.
- **`require_signatures`** — whether to also flip the ledger's `SetSigningConfig` so unsigned writes to `_recon` are *rejected*, not merely unsigned. Stronger, but makes the key load-bearing for availability.
- **Auditor read-access ✅ done.** `GET /audit/entries` serves the entries (sequence + payload + signature) so authenticity needs no ledger credentials. Completeness has a different anchor (the ledger) and is resolved in §9 — bucket isolation was considered and rejected there.

**Phase 2 (closures):**
- **Closure ↔ ledger chapter** — decide whether a recon closure *is* a ledger `CloseChapter` on `_recon`, or a recon-level artefact that merely cites the current chapter boundary. The former couples cadence to the ledger's chapter rotation; the latter keeps reconciliation in control of the business-period breakdown.
- **Single-open-closure invariant** — Postgres enforced it with a partial unique index; on V3 there is no recon-side single-writer. Decide where the invariant lives (a `_recon` marker account CAS, mirroring the alert-state markers).

## 9. Completeness — the resolved model (P1.3)

Two separate guarantees an auditor wants from the served entries, and they have different trust anchors:

1. **Authenticity** — *is each record genuine (authored by reconciliation, unaltered)?* Answered fully and self-containedly by the per-entry Ed25519 signature. `GET /audit/entries` serves `{payload, signature}`; the auditor runs `ed25519.Verify(publicKey, payload, signature)` against the key from `GET /audit/signing-keys`. Nobody from Formance, no ledger access. **Done.**
2. **Completeness** — *could a genuine reconciliation write have been dropped or hidden from the list?* This is **not** answered by the served sequence, for two structural reasons found while building P1.3:
   - **The `audit_sequence` is per-FSM, not per-ledger.** `NextAuditSequenceID` is a single counter on the Raft FSM state (`internal/infra/state/fsmstate.go`), and one gRPC `BucketService` server is one FSM (one `clusterID`, one store). Every ledger that server hosts shares one chain, so filtering to `reconciliation` yields a **monotonic but sparse** subset — gaps are the *expected* footprint of the other ledgers, not evidence of a drop. A "no gaps in the filtered subset" check is therefore invalid on a shared server.
   - **The sequence is not signed.** It is assigned by the FSM *after* the client's batch is signed, and stored on the `AuditEntry` outside the signature. So the number is not covered by the Ed25519 proof — a party serving the entries could misreport or renumber it without breaking any signature.

**Why bucket isolation is the wrong lever.** "Give reconciliation its own bucket so the subset is dense" means a **separate ledger deployment** for `_recon` (there is no sub-server bucket to route into; a bucket *is* an FSM). That is heavy, and — decisively — it still would not deliver completeness, because a dense-but-**unsigned** sequence is no more omission-resistant than a sparse one: the auditor would have to read the ledger to trust the numbers regardless. Isolation buys a tidy sequence, not a guarantee. **Not pursued.**

**Where completeness actually lives — the ledger.** The audit log is the ledger's own cryptographic source of truth; the ledger, not recon's endpoint, is the completeness anchor. An auditor who needs "nothing was dropped" reads the ledger's audit trail directly — `ListAuditEntries` (the dense, full chain) plus `CheckStore`, which recomputes the keyed hash chain and cross-checks ~20 projections (`internal/application/check/checker.go`). This needs ledger read-access, which is the correct scope for a rigorous completeness audit; recon's endpoint remains the zero-credential path for per-entry authenticity. The UI and guide state exactly this split.

**Future hardening (if a customer needs recon-endpoint-only completeness).** Have reconciliation embed its **own** monotonic counter inside each signed batch's metadata. Then completeness is self-contained: the signed counters must be contiguous, so a hidden entry leaves a signed gap and the counter cannot be forged — no ledger access, no bucket isolation. The cost is a serialization point: every `_recon` write must CAS-increment the counter (a guarded Numscript on a counter account, the same bare-source CAS the alert lifecycle already uses), so writes no longer parallelise. Given recon's write volume this is likely acceptable, but it is a deliberate trade to make only when the requirement is real.
