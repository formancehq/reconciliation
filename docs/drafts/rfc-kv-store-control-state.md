# RFC: A generic K/V store for reconciliation's control state (Ledger 3.1)

Status: ⏳ **Draft, forward-looking.** Tracked in [EN-1932](https://formance-team.atlassian.net/browse/EN-1932), blocked by the ledger story [EN-1426](https://formance-team.atlassian.net/browse/EN-1426). Reviewed on 2026-10-07 against `feat/reconciliation-ledger-v3` and the ledger `release/v3.0` branch at `e2d0f4713`. Companion to [rfc-ledger-native-storage.md](../archive/rfc-ledger-native-storage.md) and [audit-chain-v3.md](../technical/audit-chain-v3.md).

---

## 1. Context

Reconciliation on Ledger v3 keeps all of its control state on the control ledger (`reconciliation` by default, `_recon` in design shorthand): rule definitions, the alert lifecycle, captures and the rule timeline, as accounts, typed metadata and transactions. A planned Ledger 3.1 generic key-value store could hold the mutable part of that state. The original driver is reconciliation 2.4.1 parity: rules are updatable, and safe concurrent updates need a compare-and-swap on a version.

This RFC records what the K/V store would change, what it would not change, and what the code must do now so that the switch stays cheap.

The decision: **reconciliation does not wait for the K/V store.** The defects found in the 2026-10-07 review are fixed with Ledger v3 primitives that exist today. The re-implementation hides every compare-and-swap behind one storage port, so that moving to the K/V store later replaces an adapter and leaves the service untouched.

## 2. The current model

Two kinds of state share the control ledger.

**Immutable records.** Captures (one per evaluation), alert transitions and the rule timeline are transactions. They belong on the ledger log and its audit chain, and they stay there.

**Mutable control state.** Rule definitions and the current alert status, acknowledgement, resolution and snooze change over their lifetime. The ledger has no mutable value with compare-and-swap, so reconciliation builds one:

- **Rules** are typed metadata on `rule:{id}`, set atomically by an `activity` transaction (`internal/ledgerstore/rule.go`). An update reads the rule, then overwrites it. The idempotency key of an update carries a random UUID (`uniqueActionKey`), so it never conflicts: two concurrent `PATCH /rules/{id}` both commit and the last one wins.
- **Alerts** keep their status in an EPHEMERAL marker account, `alert:st:{open|ack}:…`, that holds one `ALERT` unit. A Numscript transition moves the marker with a bare source, so the transaction fails with insufficient funds when the marker is not where the writer expected (`moveMarker` in `internal/ledgerstore/alert_transition.go`). Readers use a `status` metadata mirror on the `alert:item:…` account.

Reconciliation already does compare-and-swap and atomic multi-key writes. It expresses them through balance guards because that is the only compare-and-swap the transaction model offers.

## 3. Premise check

The K/V store does not exist yet, and nobody has specified its properties.

- [EN-1426](https://formance-team.atlassian.net/browse/EN-1426) ("Add a generic KV Store to the ledger") is a Backlog story with no assignee and no fix version. Its description is two sentences: store ad hoc data, for example ledger-connect plugin cursors. It sits under [EN-1336](https://formance-team.atlassian.net/browse/EN-1336) (Ledger v3.1, Backlog, priority Low).
- The ledger has no K/V store code or branch on `release/v3.0` (`e2d0f4713`) or `main`. Its only key-value code is internal: Pebble and `internal/pkg/kv/sharded_map.go`.
- "Compare-and-swap" and "atomic batches" come from this RFC, not from a ledger specification. Section 8 lists what the ledger team must confirm.

## 4. What Ledger v3 already provides

These checks ran on 2026-10-07 against a local ledger that speaks protocol 16 (`v3.0.0-beta.7`), with a control ledger built by the reconciliation provisioner and its stored Numscript library.

| Primitive | Observed behavior | Use |
|---|---|---|
| Self-posting guard | `send [ALERT 1] (source = $st_open destination = $st_open)` commits when `$st_open` holds the marker, and fails with `FailedPrecondition` (insufficient funds) when it does not | Guard a write that must not move the marker, such as the repeat-failure bump or a snooze |
| Atomic multi-transaction batch | An `ApplyBatch` that holds a capture and a failing guarded move commits nothing. A batch of two transactions commits both | Commit one evaluation, the capture and every alert transition, as one batch |
| Transaction `reference` as a durable unique constraint | A second transaction with the same `reference` fails with `AlreadyExists` ("transaction reference … already exists"), in any later batch. The ledger never deletes a reference | Let only one writer win the first open of an alert, and each reopen |
| Idempotency key as a short-lived unique constraint | The same key with different content fails with `AlreadyExists` ("idempotency key conflict"). An identical replay succeeds without a second write | Deduplicate retransmits. Not a durable constraint, see the next two rows |
| A rejected batch consumes its key | After a guard failure, the same key fails again with the same content, and with `AlreadyExists` with different content | A retry after a conflict must derive a new key from the state it re-read |
| Key lifetime | Keys expire after `--idempotency-ttl`, 24 hours by default (`cmd/server/server.go:207` at `e2d0f4713`) | A key is a uniqueness window, not a durable constraint |
| Metadata value limit | One metadata value above 16,384 bytes fails with `InvalidArgument` | A capture whose evidence exceeds 16 KiB is rejected today. The evidence must be bounded or stored elsewhere |

A Numscript `kept` destination asserts that the source holds the funds without writing a posting, and `oneof {$open $ack}` closes from whichever state holds the marker. Both were checked in the Numscript interpreter the ledger pins, not yet against a running ledger.

## 5. Review findings and the K/V store

The 2026-10-07 review reproduced three defects against the same ledger.

1. **A repeat failure overwrites an acknowledgement.** `alert_bump` moves no marker, so an evaluation that read `OPEN` before an operator acknowledged the alert still commits. The mirror says `OPEN` while the marker sits on `st:ack`. Every later acknowledge, resolve and auto-resolve then fails with `FailedPrecondition`, and the alert can no longer close.
2. **Two evaluations of one rule open the same alert twice.** Both read "no alert" and both run `alert_open`, whose mint has no guard. The result is two `ALERT` markers, an alert ID that returns 404, and a live-alert count of 1 after the alert is resolved. Nothing serializes the evaluations of a rule: the scheduler does not skip a rule whose previous run is still going, and a manual `POST /rules/{id}/evaluate` can overlap a scheduled run.
3. **An accepted alert reopens on the next failing evaluation.** The code stores `ExpiresAt` and never reads it, so the acceptance window that the PRD promises does not exist.

| Finding | Does the K/V store fix it? | Fix with today's primitives |
|---|---|---|
| Bump after acknowledge | Only if every write uses compare-and-swap. The missing piece is the guard on that write, not the primitive | Self-posting guard on the bump and on snooze |
| Double open | Yes, with create-if-absent | Transaction `reference` on `(rule, period, fingerprint, generation)`, where the generation counts past closes |
| Concurrent rule `PATCH` | Yes, with a compare-and-swap on a version. This is the clearest gain | A version token per rule (section 6), or the key `(rule, version N)` inside the TTL |
| Evaluation not atomic (capture first, then one transaction per alert) | Only if one batch can hold K/V writes and ledger transactions. Otherwise atomicity gets worse | One `ApplyBatch` per evaluation |
| Acceptance window ignored | No. This is evaluation logic | Leave accepted, unexpired alerts out of the transition plan |
| Overlapping evaluations of one rule | Partly, with a lease | A per-rule evaluation token that each evaluation batch moves, as in the [EN-1941](https://formance-team.atlassian.net/browse/EN-1941) counter spike |
| List endpoints read the full history | Unknown until the K/V query surface is known | Ledger cursors, newest first |

The K/V store would remove mechanism: the marker accounts, the EPHEMERAL purge, the status mirror and one Numscript program per transition. It fixes none of the three defects on its own, and nobody has scheduled it.

## 6. Requirement: design for the switch now

The re-implementation must keep the K/V store a drop-in change. Four rules apply.

1. **One storage port for mutable state.** The service builds a unit of work, and the adapter commits it in one atomic call:

   ```go
   // ControlState commits every precondition, write and record of a unit of
   // work atomically, or none of them.
   type ControlState interface {
       // Load returns the current values and versions of the given keys.
       Load(ctx context.Context, keys ...Key) (Snapshot, error)
       // Commit returns the new version of every written key, or
       // ErrConflict when any precondition fails.
       Commit(ctx context.Context, uow UnitOfWork) (Versions, error)
   }

   type UnitOfWork struct {
       IdempotencyKey string         // from business identity and the versions read
       Expect         []Precondition // a key at a version, or a key that is absent
       Put            []Write        // new values
       Records        []Record       // immutable capture and activity records
   }
   ```

   The port, its in-memory reference adapter, the Ledger v3 adapter and the contract suite of rule 4 live in `internal/controlstate`, `internal/controlstate/memstate`, `internal/controlstate/ledgerstate` and `internal/controlstate/controlstatetest`. The Ledger v3 adapter passes the suite against a live ledger (`TestIntegration_ControlStateContract`).

   Listing with filters and pagination goes through a separate query port. In both worlds the ledger metadata index backs it, unless the K/V store offers scans.
2. **Versions are explicit integers.** Every mutable entity carries a monotonic version. The content hash (`revision`) stays as the identity of a rule configuration in captures. The API exposes the version as an `ETag`. `PATCH /rules/{id}` and the alert actions accept `If-Match` and answer `409 Conflict` on a stale version. Today a lost race answers `500`.
3. **Ledger details stay in the adapter.** Account addresses, assets, Numscript programs, markers and idempotency-key formats never reach the service. The Ledger v3 adapter maps a precondition to a guarded token move (`ver:{entity}:{n}` to `ver:{entity}:{n+1}`, or the alert marker), an absent key to a transaction `reference` on the entity's generation, a write to account metadata and a record to transaction metadata, all in one `ApplyBatch`. The K/V adapter maps preconditions and writes to K/V compare-and-swap operations and keeps records as ledger transactions.
4. **One contract test suite runs against every adapter.** It covers the three reproduced races, a conflict followed by a retry with a fresh key, a replay inside the TTL, and an evaluation batch that fails as a whole.

With these rules, the K/V migration changes the adapter and its tests. The service, the API and the evaluation planner stay as they are.

## 7. The split

The K/V store complements the ledger log. It does not replace it.

- **Mutable control state** (rule definitions and versions, the alert lifecycle, closures) moves to the K/V store when the store ships.
- **Immutable records** (captures, transition events, the audit chain) stay ledger transactions, as [audit-chain-v3.md](../technical/audit-chain-v3.md) describes.

Reconciliation on Ledger v3 is pre-GA, so the switch is a refactor of the storage adapter, not a data migration.

## 8. Questions for the ledger team (EN-1426)

1. **Atomicity across stores.** Can one `ApplyBatch` hold K/V compare-and-swap operations and ledger transactions? This question decides the design. Without it, every transition writes its state in one store and its audit record in another, and reconciliation needs an outbox to stay consistent. That is worse than today.
2. **Compare-and-swap semantics.** Does the store compare a version or the value? Does it offer create-if-absent and delete-if-version?
3. **Values.** What are the size limit and the typing? A rule definition is a JSON document of a few kilobytes. Alert state is a few scalars.
4. **Reads.** Are there prefix or range scans, secondary indexes and ordering? Without them, reconciliation keeps the ledger metadata index for lists.
5. **Audit.** Are K/V writes signed and covered by the audit chain like transactions? Reconciliation's audit story depends on the answer.
6. **Retention.** Do keys expire, or do they follow the ledger retention?
