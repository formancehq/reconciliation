# The Audit Journal

✅ **Shipped** — `internal/audit/`, `internal/storage/audit*.go`, `internal/storage/period_seal.go`, migration 12.

Reconciliation already kept a complete history: every evaluation persisted, every alert transition recorded. What it could not do was *prove* that history had not been retouched. This document describes the journal that closes that gap, why it is built the way it is, and what changes when the module moves onto Ledger V3.

---

## 1. What it is

One append-only, hash-chained journal — `reconciliations.audit_entry` — covering every state-bearing operation of the module:

| Kind | Recorded when |
|---|---|
| `rule.created` | A control is defined. The memento carries revision 1 of its full spec. |
| `rule.revised` | A control changes. The memento carries the new revision's full spec. |
| `rule.deleted` | A control is tombstoned. Its history is retained. |
| `evaluation.committed` | Any rule execution — PASS, FAIL or ERROR. |
| `alert.transition` | Any alert lifecycle change, including the ones whose notification is suppressed. |
| `period.sealed` | A reconciliation period is closed. |

Two properties are worth stating plainly, because they are what an auditor is actually buying:

**Passes are journalled.** A journal of failures answers "what went wrong". An auditor asks the harder question — *prove the control ran on the days nothing went wrong* — and only the passing entries answer it.

**Suppressed notifications are journalled.** A repeated, materially-identical failure is deliberately not re-paged ([notification-suppression.md](./notification-suppression.md)). The transition is still recorded, and the `notify: false` decision is bound into the hash, so "we were never told" becomes a checkable claim rather than an assertion.

### The derivability corollary

The chain is the only hash-bound dataset. Everything else is a projection of it, which is the rule Ledger V3 states explicitly: *if a dataset is derivable from the audit chain by replay, re-derive it and compare; if it is not derivable, it must be hash-bound.*

Applied here: an alert's current `status`, `ack`, `resolution` and `snooze` are all reconstructable by replaying the chain. They therefore do not need to be immutable — they need to be **verifiable**. That is a different and much stronger claim than "we keep the history".

---

## 2. The mechanism

### Two counters

```sql
seq      bigserial NOT NULL   -- physical insertion order; gaps are normal
sequence bigint    NOT NULL   -- dense logical counter; a gap means an entry was removed
```

A `bigserial` cannot carry gap detection: a rolled-back transaction consumes a value, so its holes are ordinary. The dense logical counter is assigned under the chain lock and is what makes a deleted entry detectable. Ledger V2's `logs` table splits the same two roles across `(seq, id)`.

### The write path

```
pg_advisory_xact_lock(classid, 0)                     -- freeze the head for this transaction
  → SELECT sequence, hash ORDER BY seq DESC LIMIT 1   -- read the head
  → memento = canonicalise(payload)                   -- Go, one implementation
  → hash = BLAKE3_keyed(prev_hash ‖ sequence ‖ at ‖ kind ‖ subject ‖ memento)
  → INSERT
```

Holding the lock across the read and the insert is the whole mechanism — without it, two concurrent appends read the same predecessor and fork the chain. Ledger V2 takes `pg_advisory_xact_lock` in `InsertLog` for exactly this reason, with the comment *"we need the last log does not change until the transaction commit"*.

The append runs **inside the business transaction**. A rolled-back operation journals nothing; a committed one cannot be missing. This is why the append lives in the storage methods rather than in a caller: `appendAlertEvent` is the single insertion point for alert transitions, so there is no code path that can transition an alert without being recorded.

One global lock, not one per rule. Gap detection only means something across the whole journal — a per-rule chain would let an entire rule's history be removed without leaving a hole anywhere. The cost is that journalled writes serialise, which is affordable here and was not for the ledger: V2 abandoned per-row chaining under thousands of transactions per second, while reconciliation writes a handful of evaluations per rule per minute.

### The canonical form

Postgres `jsonb` discards key order and rewrites number literals, so a hash taken over a `jsonb` round-trip cannot re-verify. The hashed payload is therefore stored as a **memento**: a frozen `bytea` copy alongside the queryable JSON. V2 does the same, hashing `encode(memento,'escape')` rather than its `data jsonb` column.

The canonical form is JCS-inspired with four rules, deliberately few so a client in any language can reimplement them and re-verify an entry:

1. Object keys sorted by UTF-8 byte sequence.
2. No insignificant whitespace.
3. Number literals preserved exactly as written — never re-parsed through a float.
4. Standard JSON string escaping, HTML escaping disabled.

Rule 3 costs nothing here because reconciliation evidence carries amounts as decimal strings, which removes the precision hazard that usually breaks this kind of scheme.

Large payloads are bound **by digest rather than by value**: an evaluation's evidence stays on the evaluation row, and the memento carries `evidenceDigest`. Copying it would double the storage for no extra guarantee, since altering the row changes its canonical digest and breaks the chain just the same.

### The keys

```
chainKey = BLAKE3-derive("formance:reconciliation:audit-hash:blake3:v1",
                         len(salt) ‖ salt ‖ len(pepper) ‖ pepper)
```

The **salt** is generated once and persisted. The **pepper** is optional and supplied out-of-band (`--audit-chain-pepper`). The distinction is the point: with a pepper configured, a stolen database dump is not enough to forge entries, because the key material is not all in the database. Without one the chain still detects accidental corruption and application-level tampering, but an attacker with full database access and this source code could in principle rebuild it.

Ledger V2 had no equivalent — it hashed inside a PL/pgSQL function, so database access was sufficient to rewrite the chain. This is a deliberate improvement on the precedent, not a copy of it.

A `key_check` (a MAC of a fixed string under the chain key) is stored so boot can detect a missing or wrong pepper. A mismatch is **fatal**, mirroring the Ledger's fatal cluster-id mismatch: continuing would append entries no later verification can reconcile with the ones before them, producing a chain that reports itself broken forever from a configuration mistake. A tamper signal that fires for benign reasons is a tamper signal nobody acts on.

### Attribution

The `by` field on acknowledgements and resolutions used to be filled in by the client. An auditor can do nothing with a self-declared name, so attribution now comes from the verified bearer token, and the field survives only as a human-readable note.

What enters the hash is a subject snapshot: the token subject, a one-byte **source tag** (`0x01` issuer, `0x02` client id, `0x03` system component, `0x00` none), its value, and the sorted scopes. The tag is what makes a scheduled run hash differently from a caller-less request even when both carry an empty subject — so *"no human touched this control run"* is a cryptographic claim, not a convention. This mirrors V3's `CallerSnapshot` encoding.

`subjectMiddleware` must stay inside the authenticated route group: it decodes the claims without re-verifying the signature, which is sound only because `auth.Middleware` has already verified the token. Mounting it outside that group would make attribution forgeable, which is precisely the flaw it exists to fix.

### Immutability

A `BEFORE UPDATE OR DELETE` trigger raises on `audit_entry`, `rule_revision` and `period_seal`. The trigger — not the grant — is the enforcement that always holds: a table's owner keeps implicit privileges that no `REVOKE` can remove.

`REVOKE UPDATE, DELETE, TRUNCATE … FROM PUBLIC` is applied as defence in depth, and becomes meaningful when the service connects as a role that does not own the tables. **That is the recommended production posture** and is an operator step, not something the migration can achieve on its own.

---

## 3. What it detects

| Attack | Detected as | Why |
|---|---|---|
| Alter a hashed field | `HASH_MISMATCH` | The recomputed hash differs from the stored one. |
| Edit a memento | `MEMENTO_DIGEST_MISMATCH` | The payload no longer matches its digest. |
| Delete an interior entry | `SEQUENCE_GAP` | The dense counter skips a value. |
| Reorder or splice entries | `BROKEN_LINK` | An entry's `prev_hash` no longer matches its predecessor. |
| Rewrite an entry and its hash | `HASH_MISMATCH` downstream | The next entry was hashed against the original value. Rewriting forward requires the chain key. |
| Alter a period seal | `HASH_MISMATCH` at the seal boundary | The seal is re-derived from its own fields during the walk. |
| Delete the tail, below a seal | `SEQUENCE_GAP` | A seal commits to a boundary that no longer exists. |

The walk stops at the first violation. Past a break, every downstream comparison is meaningless, so listing more would be listing noise — the same reason the Ledger checker halts on the first mismatch.

### The one thing it does not detect

**Past the most recent seal, deletion of the journal's tail is not detectable from the journal alone.** The head is derived from the same table an attacker just truncated, so there is nothing left to contradict them.

Two things bound this:

- `POST /audit-entries/verify` does not silently clamp an explicitly requested range down to a shorter head. A caller who names an end and finds fewer entries is told so.
- A period seal commits to a boundary, so anything removed below it is caught with no range needing to be specified.

This is the strongest practical argument for sealing periods promptly rather than eventually, and it is the gap Ledger V3 closes for free (§6).

---

## 4. Period seals

A seal closes a contiguous range of the chain under a period label. The **range**, not the label, is what it covers — exactly as a Ledger V3 chapter closes at an audit-sequence boundary rather than filtering on content. `firstSequence` continues from the previous seal's end, so seals form a partition with no gap and no overlap.

```
sealingHash = BLAKE3(context ‖ periodID ‖ firstSeq ‖ lastSeq ‖ entryCount ‖ lastAuditHash ‖ stateHash)
signature   = Ed25519(sealingHash)
```

`stateHash` covers the period's resulting alert state via a deterministic **explicitly ordered** scan — never an aggregate whose input order is merely conventional, which is the determinism trap in V2's block hasher.

The seal's own journal entry lands at `lastSequence + 1` and therefore belongs to the following period, the way a chapter's seal order is proposed after the boundary it describes. A consequence worth knowing: a quiet period's range is not literally empty, it contains the previous period's seal.

### Why the sealing hash is unkeyed

The chain hashes are keyed, so nobody outside the installation can check them. Left there, a client's auditor would have to trust our own verification endpoint — precisely the "black box" objection this feature exists to answer.

The sealing hash is therefore **unkeyed and reproducible from the seal's published fields**, and signed with **Ed25519**. An auditor holding the public key recomputes the hash themselves and verifies the signature with no involvement from us.

They cannot independently verify `lastAuditHash` — it is keyed and opaque to them. They do not need to: the signature binds us to the statement *"at sequence N the chain head was this and the derived state was that"*. If a later export presents a different head for the same period, the signature convicts us.

This is deliberately **not** the Ledger's receipt scheme, which is HMAC-SHA256 and symmetric; its own documentation notes that no external auditor needs to verify a receipt. Here that is the entire point.

### The closing barrier

A sealed period stops accepting alert transitions. Enforced in `appendAlertEvent` — the single door — so no path can bypass it. After the books are closed, the books do not move; this is the property every auditor looks for in a closing process, and the module had no form of it before.

Under a periodic cadence the successor period opens a fresh case for the same fingerprint, so ordinary monitoring is unaffected. What is refused is a genuine write into closed books: a late evaluation backdated into a sealed period, or an operator editing history.

Two consequences, both intentional:

- **`continuous` cannot be sealed.** It has no end, and its alerts would be frozen with no successor period to move to.
- **Sealing a period that is still live makes its rules unevaluatable.** A daily-cadence rule whose current day is sealed fails its next evaluation with 409, because driving its alerts hits the barrier. Seal periods that are over — that is what sealing means. The failure is loud and names the period rather than silently accepting evidence into closed books.

Sealing is **not idempotent**: a second seal is a 409, since it would either contradict the first or silently do nothing.

---

## 5. Operating it

| Concern | What to do |
|---|---|
| **Pepper** | Set `--audit-chain-pepper` from a secret manager before first boot. Changing it afterwards is fatal by design. Without it, the service logs a warning at every start naming what is weaker. |
| **Signing key** | Set `--audit-signing-key-seed`; the private key then never touches the database. When unset, one is generated on first boot, stored, and the seed logged **once** so it can be moved into configuration. |
| **Key rotation** | Supply a new seed. The previous key is retired but retained in `audit_signing_key`, so seals it signed stay verifiable — `GET /audit-signing-keys` serves retired keys too. |
| **Hardening** | Run the service as a role that does not own the `reconciliations` schema, so the `REVOKE` has teeth. |
| **Backups** | The journal is the artefact of record. A backup that captures the projections but not `audit_entry` captures the claims and not the proof. |

---

## 6. Moving to Ledger V3

The [ledger-native storage RFC](../drafts/rfc-ledger-native-storage.md) makes reconciliation a stateless service whose state lives as account metadata in a `_recon` control ledger, with no Postgres. Most of this document's machinery is then **subsumed by the ledger itself** — which is the outcome to want, not a loss.

| Built here | On V3 | Why |
|---|---|---|
| The hash chain | **Subsumed.** Every `AddMetadata` write to `_recon` produces a log bound by the ledger's own keyed-BLAKE3 audit chain. | We stop maintaining a chain and start citing one. |
| Dense sequence + advisory lock | **Subsumed, and better.** Raft's single-writer FSM assigns `audit_sequence` with no lock and no serialisation cost. | The one real cost of this design disappears. |
| Immutability trigger + REVOKE | **Subsumed.** Pebble rows are audit-log projections; there is no UPDATE path to close. | |
| Tail-truncation gap (§3) | **Closed.** The chain is Raft-replicated across nodes, so truncating one node's tail contradicts the others. | The limitation we have to document today stops existing. |
| Microsecond timestamp truncation | **Gone.** Protobuf timestamps, no `timestamptz`. | See the commit that fixed it — a Postgres-specific hazard. |
| Chain verification endpoint | **Stays ours**, as a façade over the ledger's `checker` and `/v3/_/audit-entries`. | V3 keeps verification internal and exposes no verify operation, so the endpoint remains the differentiator. |
| Ed25519 seal signature | **Stays ours.** | The ledger's chain is keyed and internal; externally-verifiable attestation is exactly the gap V3 does not fill. |
| Period seals | **Stays ours, thinner.** A chapter is a cluster-wide operational boundary, not a business period, so the business seal keeps its own range + state hash + signature — but cites the ledger's chain head instead of computing one. | |
| The canonical memento | **Stays ours, and is the bridge.** | The ledger binds *orders* by their protobuf bytes; the memento is the business payload we put inside the metadata value. Replaying mementos in logical-sequence order is what re-anchors today's Postgres chain into `_recon`. |
| Rule revisions | **Reshaped.** Ledger metadata is scalar, typed and last-write-wins with no native append, so revisions become distinct keys or `rule:{id}:rev:{n}` accounts rather than rows. | |

### The one thing that must not be lost in translation

V3's `CallerSnapshot` records **the caller as the ledger sees it** — which, once reconciliation is a ledger client, is *reconciliation's own service identity*. Every entry would read as "made by the reconciliation service", and the end user would vanish from the record.

So the end-user subject has to stay in **our** memento, hashed as part of the payload the ledger binds. Relying on the ledger's caller snapshot alone would silently destroy the attribution this work exists to establish — the same failure mode as the self-declared `by` field, arrived at from the opposite direction.

### Migration shape

1. Export the Postgres chain: entries in logical-sequence order, mementos verbatim.
2. Replay each memento as an `AddMetadata` action on `_recon`; the ledger's chain binds them as it goes.
3. Record a boundary artefact — the final Postgres head hash and the first `_recon` audit sequence — and sign it with the existing key. Verification then crosses the storage change the way it crosses a period seal.
4. Keep the Postgres journal read-only for as long as the retention policy requires. It remains verifiable on its own terms; nothing about it needs reinterpreting.

This is the argument for investing in the canonical memento now, even though it looks over-engineered for Postgres alone.

---

## 7. Where this came from

Ledger V2's log is the closest precedent — same database, same constraints — and its scars shaped five decisions here.

| V2 did | We do | Because |
|---|---|---|
| Hashed in a PL/pgSQL trigger reproducing Go's `json.Marshal` by hand | Hash in Go, once; the database only forbids rewriting | The two implementations drifted. V2 carries a migration literally named *"Fix hashing function"*. |
| Excluded the log id from the hash (`"id":0` placeholder) | Hash the sequence | A renumbering was otherwise detectable only by the gap it opened. V3 hashes the sequence; so do we. |
| Made hashing a per-ledger flag: `SYNC` / `ASYNC` / `DISABLED` | No flag, no off switch | A compliance guarantee that can be disabled is worth much less to an auditor — and a per-scope flag creates a question V2 cannot answer cleanly: *was hashing on for this period?* |
| Retreated to block hashing under load | Keep per-entry chaining | Block granularity tells you something in a batch of N was altered, not which row. Our write rate is three to four orders of magnitude below the ledger's, so the retreat is unnecessary. |
| Derived the key from nothing outside the database | Salt **plus** optional operator pepper | Database access alone should not be enough to rebuild the chain. |

And one thing V2 got right that we copied without changing: the **memento** — hash a frozen canonical byte copy, never the queryable JSON column.

---

## 8. API

See [api.md](./api.md) and `openapi.yaml`. In brief:

```
GET  /audit-entries                     walk the journal; filters incl. actor=system|human
GET  /audit-entries/{sequence}          one entry with its memento
POST /audit-entries/verify              recompute a range; {} verifies everything
GET  /audit-signing-keys                the public keys an auditor needs

GET  /periods                           sealed periods
GET  /periods/{periodID}                a seal, or status OPEN
POST /periods/{periodID}/seal           close a period
POST /periods/{periodID}/verify         re-derive and check a seal's signature

GET  /rules/{ruleID}/revisions          a control's frozen definitions
GET  /rules/{ruleID}/revisions/{n}      what was actually being checked
```

Plus `auditSequence` on evaluations, alerts and alert events, and `periodID` on evaluations — without those links the evidence exists but is attached to nothing verifiable.

### The three calls an audit takes

```bash
curl $API/periods/2026-05                                   # the seal: one hash for the month
curl "$API/audit-entries?periodID=2026-05"                  # the dense journal for that period
curl -X POST $API/audit-entries/verify -d '{"periodID":"2026-05"}'   # the chain is intact
```

The auditor does not read thousands of rows. They check one hash, confirm the chain, then sample inside a journal whose integrity is already established.
