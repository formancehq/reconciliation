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

A `BEFORE UPDATE OR DELETE` row trigger **plus** a `BEFORE TRUNCATE` statement trigger raise on `audit_entry`, `rule_revision` and `period_seal`. The triggers — not the grant — are the enforcement that always holds: a table's owner keeps implicit privileges that no `REVOKE` can remove.

Both are needed, and the second was missing at first. A `FOR EACH ROW` trigger does not fire on `TRUNCATE`, so the entire journal could be emptied with nothing raised — verified by doing it. Removing every entry is strictly easier than editing one, so a guard that catches the edit and misses the wipe is not a guard.

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
| Alter a closure | `HASH_MISMATCH` at the closure boundary | The closure is re-derived from its own fields during the walk, breakdown included. |
| Restate a period's figures inside a closure | `HASH_MISMATCH` | The breakdown enters the closing hash through the state hash, which is re-derived before the closing hash is checked. |
| Alter a closure covering an empty prefix | `HASH_MISMATCH` at the closure's own entry | Boundary sequence 0 has no entry for the walk to reach, so a verification starting at 1 re-derives such a closure explicitly. |
| `TRUNCATE` any journal table | Refused outright | Statement-level trigger; a row trigger would not fire. |
| Delete the tail, below a closure | `SEQUENCE_GAP` | A closure commits to a boundary that no longer exists. |

The walk stops at the first violation. Past a break, every downstream comparison is meaningless, so listing more would be listing noise — the same reason the Ledger checker halts on the first mismatch.

### The one thing it does not detect

**Past the most recent seal, deletion of the journal's tail is not detectable from the journal alone.** The head is derived from the same table an attacker just truncated, so there is nothing left to contradict them.

Two things bound this:

- `POST /audit-entries/verify` does not silently clamp an explicitly requested range down to a shorter head. A caller who names an end and finds fewer entries is told so.
- A closure commits to a boundary, so anything removed below it is caught with no range needing to be specified.

This is the strongest practical argument for closing the journal promptly rather than eventually, and it is the gap Ledger V3 closes for free (§6).

---

## 4. Closures

A **closure** is a contiguous, sealed segment of the journal. Its range is not derived from anything — `firstSequence` is written when the closure is *opened*, and `lastSequence` is the chain head when it is closed. Closures partition the journal with no gap and no overlap.

```
stateHash   = BLAKE3(context ‖ for each period: periodID ‖ entryCount ‖ alertCount
                                ‖ unresolvedCount ‖ periodStateHash ‖ ended ‖ frozen)
closingHash = BLAKE3(context ‖ closureID ‖ firstSeq ‖ lastSeq ‖ entryCount
                              ‖ lastAuditHash ‖ stateHash ‖ closedBy ‖ closedAt)
signature   = Ed25519(closingHash)
```

### Why closing takes no argument

This replaced a period seal, which named the period it closed and derived its range from that name: `first = previous seal's last + 1`. That one derivation is where all the difficulty lived. Because the range was deduced at close time from a caller-supplied label, three separate mistakes became expressible — and each was permanent, since a seal cannot be corrected:

| Mistake | What it did |
|---|---|
| Closing a period earlier than the last one | Sealing `2026-06` after `2026-08` gave June the range 8–8: the single entry recording August's own closure |
| Closing past a period still open | Sealing `2026-06` while unsealed `2026-05` entries existed swallowed them into June, and May could then never be sealed |
| Closing a period that had not begun | Sealing `2099-01` closed the books through 2099 and left every real period unsealable |

Each needed a guard, and the guards in turn made weekly and monthly seals mutually exclusive, because two labels could not partition the same journal.

`POST /closures` takes no arguments. There is one open closure, and closing closes it. The mistakes above are not caught — they are **unexpressible**. This follows Ledger V3's chapters, where `processCloseChapter` discards its request payload outright.

### Closing opens its successor

The successor closure is inserted in the same transaction, so writes continue immediately. This is what removed the sharpest edge of the old design: sealing a period froze it while its rules kept running, so the next evaluation failed with 409 and was rolled back — leaving no trace that a control had even been attempted.

### The label survives, demoted

A closure is an operational boundary. It cannot answer *"prove the control ran for May"*, because a business period is not a wall-clock window: `periodID` comes from the evaluation's point-in-time, so a backfill of May run in August belongs to May. Verified in the journal — entries recorded on 21 August carrying the label `2026-07`, because the PIT read was 15 July.

So each closure carries a **per-period breakdown**, and `stateHash` covers it, so the signature binds it. The two figures are deliberately different questions:

- `entryCount` on the closure — everything in the range, including entries belonging to no period at all, such as `rule.created`
- `entryCount` on a period — only entries carrying that label

They are not meant to reconcile by eye, and an auditor reading a closure needs to know which one they are looking at. The read path still speaks in business periods: `GET /periods/2026-05` resolves the label to the closures that observed it, so the auditor's three calls are unchanged.

### Why the closing hash is unkeyed

The chain hashes are keyed, so nobody outside the installation can check them. Left there, a client's auditor would have to trust our own verification endpoint — precisely the "black box" objection this feature exists to answer.

The closing hash is therefore **unkeyed and reproducible from the closure's published fields**, and signed with **Ed25519**. An auditor holding the public key recomputes it themselves and verifies the signature with no involvement from us. Confirmed by an independent Python reimplementation that reproduces the hash byte-for-byte from the API response alone, breakdown included.

They cannot independently verify `lastAuditHash` — it is keyed and opaque to them. They do not need to: the signature binds us to the statement *"at sequence N the chain head was this and the derived state was that"*. If a later export presents a different head for the same closure, the signature convicts us.

This is deliberately **not** the Ledger's receipt scheme, which is HMAC-SHA256 and symmetric; its own documentation notes that no external auditor needs to verify a receipt. Here that is the entire point.

### The closing barrier

A period whose calendar span is over stops accepting alert transitions once a closure observes it. Enforced in `appendAlertEvent` — the single door — so no path can bypass it. After the books are closed, the books do not move.

The barrier is keyed on the **business period**, not on the closure, and only periods that have **ended** are frozen. Three consequences, all intentional:

- **A period still in progress is attested but not frozen.** A closure running mid-month records what it saw and leaves the month open, so the old hazard of closing a live period simply does not arise.
- **`continuous` is never frozen.** It has no end, so a live-monitoring rule keeps running across every closing. Verified: a continuous rule evaluated successfully immediately after a closing that froze `2026-07`.
- **Weekly and monthly rules can be attested by the same closing.** They are two labels in one breakdown, not two competing partitions, so the mutual exclusion the old seals forced is gone.

### What is no longer a conflict

Closing twice in a row is legal and produces an empty second closure, where sealing the same period twice used to be a 409. The conflict is gone because what made it one is gone: a second seal of the same period either contradicted the first or did nothing, whereas a second closing attests a second, genuinely empty segment — and an empty closure is a real answer, so refusing it here would mean refusing it everywhere.

Two concurrent closings therefore produce two closures rather than one and an error. Both are signed, both verify, and the partition is intact. Confirmed by racing two `POST /closures` against the local stack.

Exactly one closure is open at any time, enforced by a partial unique index rather than by application logic — the Ledger states the same invariant and gets it from a single-writer FSM, which we do not have. A second open closure would give two ranges the same starting sequence and quietly double-attest everything after it.

### The cadence is configuration

Closing is automated by a cron stored in the database, not a boot flag:

```
PUT /closing-schedule  {"cron": "0 0 1 * *"}   # monthly
PUT /closing-schedule  {"cron": "0 0 * * *"}   # daily
PUT /closing-schedule  {"cron": ""}            # manual only
```

Changing it mid-flight only makes the next closure longer or shorter; closures already closed are unaffected. That is what makes the cadence safe to change at all — and it is why this is a stored setting rather than a flag, following the Ledger's `set-schedule`. The worker re-reads it each minute and closes when a firing has fallen due since the open closure was opened, which makes rotation both idempotent across several workers and catch-up safe after downtime.

## 5. Operating it

| Concern | What to do |
|---|---|
| **Pepper** | Set `--audit-chain-pepper` from a secret manager before first boot. Changing it afterwards is fatal by design. Without it, the service logs a warning at every start naming what is weaker. |
| **Signing key** | Set `--audit-signing-key-seed`; the private key then never touches the database. When unset, one is generated on first boot and stored — the seed is **not** logged, because logs usually have broader read access and longer retention than the database, and writing a signing key there would invert the very key separation the seal signature exists to provide. Read it from `audit_chain_config.signing_private_seed` when you are ready to move it into configuration. |
| **Key rotation** | Supply a new seed. The previous key is retired but retained in `audit_signing_key`, so seals it signed stay verifiable — `GET /audit-signing-keys` serves retired keys too. |
| **Hardening** | Run the service as a role that does not own the `reconciliations` schema, so the `REVOKE` has teeth. |
| **Backups** | The journal is the artefact of record. A backup that captures the projections but not `audit_entry` captures the claims and not the proof. |

---

## 6. Moving to Ledger V3

The [ledger-native storage RFC](https://github.com/formancehq/reconciliation/blob/feat/reconciliation-ledger-v3/docs/drafts/rfc-ledger-native-storage.md) — which lives on `feat/reconciliation-ledger-v3`, not here — makes reconciliation a stateless service whose state lives as account metadata in a `_recon` control ledger, with no Postgres. Most of this document's machinery is then **subsumed by the ledger itself** — which is the outcome to want, not a loss.

| Built here | On V3 | Why |
|---|---|---|
| The hash chain | **Subsumed.** Every `AddMetadata` write to `_recon` produces a log bound by the ledger's own keyed-BLAKE3 audit chain. | We stop maintaining a chain and start citing one. |
| Dense sequence + advisory lock | **Subsumed, and better.** Raft's single-writer FSM assigns `audit_sequence` with no lock and no serialisation cost. | The one real cost of this design disappears. |
| Immutability trigger + REVOKE | **Subsumed.** Pebble rows are audit-log projections; there is no UPDATE path to close. | |
| Tail-truncation gap (§3) | **Closed.** The chain is Raft-replicated across nodes, so truncating one node's tail contradicts the others. | The limitation we have to document today stops existing. |
| Microsecond timestamp truncation | **Gone.** Protobuf timestamps, no `timestamptz`. | See the commit that fixed it — a Postgres-specific hazard. |
| Chain verification endpoint | **Stays ours**, as a façade over the ledger's `checker` and `/v3/_/audit-entries`. | V3 keeps verification internal and exposes no verify operation, so the endpoint remains the differentiator. |
| Ed25519 seal signature | **Stays ours.** | The ledger's chain is keyed and internal; externally-verifiable attestation is exactly the gap V3 does not fill. |
| Closures | **Stays ours, thinner.** Our closure is already shaped like a chapter — auto id, range opened with it, close takes no argument — so on V3 it keeps its state hash, per-period breakdown and signature but cites the ledger's chain head instead of computing one. The business period label stays on the evidence, where the ledger has no equivalent. | |
| The canonical memento | **Stays ours, and is the bridge.** | The ledger binds *orders* by their protobuf bytes; the memento is the business payload we put inside the metadata value. Replaying mementos in logical-sequence order is what re-anchors today's Postgres chain into `_recon`. |
| Rule revisions | **Reshaped.** Ledger metadata is scalar, typed and last-write-wins with no native append, so revisions become distinct keys or `rule:{id}:rev:{n}` accounts rather than rows. | |

### The one thing that must not be lost in translation

V3's `CallerSnapshot` records **the caller as the ledger sees it** — which, once reconciliation is a ledger client, is *reconciliation's own service identity*. Every entry would read as "made by the reconciliation service", and the end user would vanish from the record.

So the end-user subject has to stay in **our** memento, hashed as part of the payload the ledger binds. Relying on the ledger's caller snapshot alone would silently destroy the attribution this work exists to establish — the same failure mode as the self-declared `by` field, arrived at from the opposite direction.

### Migration shape

1. Export the Postgres chain: entries in logical-sequence order, mementos verbatim.
2. Replay each memento as an `AddMetadata` action on `_recon`; the ledger's chain binds them as it goes.
3. Record a boundary artefact — the final Postgres head hash and the first `_recon` audit sequence — and sign it with the existing key. Verification then crosses the storage change the way it crosses a closure.
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

POST /closures                          close the journal — no arguments
GET  /closures                          every closure, the open one included
POST /closures/{id}/verify              re-derive and check a closure's signature
GET  /closing-schedule                  the cron rotating closures
PUT  /closing-schedule                  change it, with no restart

GET  /periods/{periodID}                what is attested for a business period

GET  /rules/{ruleID}/revisions          a control's frozen definitions
GET  /rules/{ruleID}/revisions/{n}      what was actually being checked
```

Plus `auditSequence` on evaluations, alerts and alert events, and `periodID` on evaluations — without those links the evidence exists but is attached to nothing verifiable.

That claim was only two-thirds true for a while, in a way worth recording: `GET /alerts` served `auditSequence` by accident, because the list handler renders the model directly and the struct tag carried it through, while `renderAlert` and `renderAlertEvent` — every single-alert read, every transition response, and the whole event timeline — dropped it. The stored column was populated the entire time. A link that exists in the database and not in the API is not a link, and the accidental one was the reason nobody noticed.

### The three calls an audit takes

```bash
curl $API/periods/2026-05                                   # what we attest for May, and by which closure
curl "$API/audit-entries?periodID=2026-05"                  # the dense journal for that period
curl -X POST $API/audit-entries/verify -d '{"periodID":"2026-05"}'   # the chain is intact
```

Unchanged by the move to closures, deliberately: closing stopped taking a business period, but reading never did. The auditor does not read thousands of rows. They check one hash, confirm the chain, then sample inside a journal whose integrity is already established.
