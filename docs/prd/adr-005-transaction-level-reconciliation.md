# ADR-005 — Transaction-level (lettering) reconciliation: a log-window cut instead of query checkpoints

**Status:** Proposed. The design is under evaluation and nothing is implemented. The decisions
taken so far are in §10.
**Tracking:** epic [EN-2315](https://formance-team.atlassian.net/browse/EN-2315). Wave 1 is EN-2316
to EN-2323 (R1–R8). Wave 2 is EN-2333 (R9, the period alert's day list), EN-2334 (R10 rewind oracle test) and
EN-2335 (R11 booking guide). EN-2324 reuses the result store for `stale_holds`. Ledger asks (§9):
L2 EN-2327 (done), L6 EN-2328, L7 EN-2329, L8 EN-2326, L9 EN-2356, L10 EN-2369, L5 EN-2331 (closed); EN-2336 tracks the checkpoint read
penalty, which this design does not depend on.
**Date:** 2026-09-28
**Decision owners:** Reconciliation maintainers
**Related:** [feature inventory](./adr-005-feature-inventory.md) (V1 scope review) · [ADR-002](./adr-002-pit-consistency.md) · [ADR-003](./adr-003-checkpoint-anchor-and-crosscheck.md) · [ADR-004](./adr-004-multi-source-comparisons.md) · [design, measurements and evidence](../technical/transaction-level-reconciliation.md)
**Upstream facts verified at:** ledger `release/v3.0` @ `7dd615dba` (rewind source, date filters,
`And` order, `ListLogs`) and @ `03d8792b5` (EPHEMERAL purge, account listing, index-building
error); Connectivity `formancehq/connectivity-plugins-poc` @ `9df05c5b` and
`formancehq/connectivity` @ `e7ca3e29`. The measurements' own SHAs are in the design doc.

---

## 1. Decision in one sentence

A transaction-level control reconciles a **PSP ledger** against a **product ledger**, per **PSP
payment reference**. The payment is first seen on the PSP ledger. The product ledger later books a
transaction carrying the same reference, which letters a *business* hold (an invoice, an order…).

The control defines its cut as **one log id `S` and one transaction id `T` per ledger, taken at
the business cut-off**. It
reconciles two legs:

- the **flow**: what each ledger booked during the window, read with `ListTransactions` filtered
  server-side on the presence of the reference, and joined on it;
- the **stock**: what is still open at the cut, rebuilt exactly as of the cut by **rewinding a live
  listing with the transactions written since the cut-off**.

**Query checkpoints are not used.** The control reads filtered transactions for the flow, and live
listings corrected by the short window of transactions since the cut-off for the stock.

Each run is one asynchronous job. The per-key detail is stored in object storage, and its hash is
anchored in the signed `_recon` capture, which also carries the run's aggregates.

---

## 2. Context

### 2.1 The booking model we recommend: lettering

Every deployment has two ledgers:

- a **product ledger**, which pilots the business;
- a **PSP ledger**, which Connectivity feeds from the PSP (Stripe, Adyen, Formance Payments…).

On both, a lifecycle is booked as *lettering* (the Formance "Lettering" note). Each state becomes an
account, named after the id **of the thing it tracks on that ledger**:

| Step | What happens | Booked as |
|---|---|---|
| 1. First signal | An authorization or a pending payment arrives | A hold account opens with a non-zero balance, for example `…:hold:payment:processing:{id}`. The transaction carries `reference = {id}:hold`, `transition_kind = to_transitional` and `status = pending`. |
| 2. Finality | The payment is captured or succeeds | The confirming transaction drains the hold to exactly zero and books to the final accounts: `reference = {id}:done`, `to_final`. |
| 3. Failure | The payment fails | The hold unwinds to where it came from. |

**The two ledgers do not key their holds by the same id.**

- On the **PSP ledger**, the hold tracks the *external payment* and is keyed by the **PSP payment
  reference**. The payment is seen here first.
- On the **product ledger**, the hold tracks a *business object*: an invoice, an order, a
  subscription period. It is keyed by that object's id, for example
  `main:hold:invoice:{invoice_no}`. When the payment arrives, the product books a transaction that
  **carries the PSP payment reference** and letters that business hold. In the owner's model the
  invoice hold opens **negative** (−X against pending revenue), and the payment is one atomic batch:
  the application, which carries the reference and brings the hold to 0, and a revenue recognition
  that does not touch the hold ([design doc
  §2](../technical/transaction-level-reconciliation.md#2-the-booking-this-control-relies-on)).
- Authorization/capture, where both sides know the authorization number from the start, is the
  special case in which the two ids coincide.

**The join key is therefore the PSP payment reference, on transactions.** Hold addresses are not
the join key. The link from a payment to the invoice it settled exists only in the product
transaction: its postings name the business hold.

Hold accounts are **EPHEMERAL**, so a lettered hold is **purged** at zero. Connectivity already
books this way:

- the `formancepayments` profile uses `fpay:{conn}:payment:hold:pending:{payment_id}` (EPHEMERAL);
- its payment transactions carry `payments.formance.com/payment-id`,
  `formance.com/observation.event-type` and `formance.com/accounting.transition` metadata;
- the payment id and the event type are indexed
  (`plugins/formancepayments/profiles/formancepayments.yaml:64-73, 1143-1147` in
  `formancehq/connectivity-plugins-poc` @ `9df05c5b`).

### 2.2 What a purged hold leaves behind (verified)

This was checked on a live throwaway ledger at `0b4676d97`. The account type was `hold:{id}`,
EPHEMERAL, and the address, source, destination and reference indexes were created up front.

| Question | Observed |
|---|---|
| Is a lettered hold still listed by `ListAccounts` or `AggregateVolumes` under its prefix? | **No.** Only open holds remain, so the book opposite only ever contains open items. |
| Are its transactions still found **by address** (any, source or destination role)? | **No, neither of them.** The lettering transaction is never indexed under the purged volume (`internal/application/indexbuilder/process_logs.go`, `isExcluded`). The *opening* transaction, which was found while the hold was open, **stops being returned** once the hold is purged. |
| Are they found by `reference`? | **Yes**: `reference == "dep_1:done"` returns the lettering transaction. |
| Does the lettering transaction still carry the hold's balance? | **Yes.** Its `post_commit_volumes` includes `hold:dep_1 = 100 − 100 = 0`. |

So a lettered item is **reachable only through its transactions**, and only through **indexed
transaction metadata or `reference`**. The address alone cannot find it. The control reads through
the metadata; `reference`, being single-valued and exact-match, only serves point lookups.

**Since EN-2036** (formancehq/ledger#2058, merged as squash `38c6eef55` on `release/v3.0`, not yet
released), the TRANSACTIONS target reads the retained account→tx mappings, so a purged hold is
reachable by address again. But an address prefix now scales with every hold ever created, purged
ones included; the merged prefix path was not re-measured ([design doc
§7.6](../technical/transaction-level-reconciliation.md#76-where-the-key-comes-from-transaction-metadata-not-the-hold-address)).
None of it changes this design, which never reads the flow by address. The ACCOUNTS target is
unaffected (`03d8792b5`): a purge deletes the volume and metadata rows
(`PrepareEphemeralAccountPurge`), so the stock listing stays bounded to the open holds.

Ask **L5** keeps only what this design needs: the metadata and `reference` paths stay a tested
contract for purged accounts, pinned by recon's own it-tests (EN-2318, EN-2319; §9).

**Consequence for the recommended booking.** Every lettering transaction, on both ledgers, must
carry the **PSP payment reference** as declared, indexed transaction metadata. On the product
ledger, it should also carry the **business id** of the hold it letters. Its postings name that
hold, but the address filters miss it once the hold is purged on a released ledger, and a metadata
field makes "which payments settled invoice X" a query.

### 2.3 The need

- Cadence: **daily**, at a business cut-off.
- Volume: **100 to 1,000,000 items per run**.
- Output: the complete list of items and their gap. That is too large for an alert or a capture, so
  it has to be retrievable asynchronously.

Two legs:

| Leg | Question | Universe | Source of truth |
|---|---|---|---|
| **Flow** (per payment reference) | Is every payment the PSP finalised applied by the product with the same amount, and does every product application point at a payment the PSP really finalised? | The window's final and failed PSP transactions, the window's product applications, and the **references still open from earlier days** (drift ≠ 0) | `ListTransactions` over the window's id range, filtered on the reference's presence (logs remain the immutable re-derivation path), plus the previous run's carried items |
| **Stock** (per hold, on each side) | What is still open at `S`, and for how long? PSP holds are pending payments; product holds are unpaid business objects | Open holds, bounded by construction because lettered holds purge | Live listing **rewound** to the cut with the unfiltered transactions `(T, head_tx]` (§5) |
| **Continuity** (self-check) | `open(S) = open(S_prev) + opened(W) − lettered(W)`, per side, per hold prefix and per asset | Aggregates | `open(S)` from the rewind and `open(S_prev)` from the previous run's stored stock (rewound only when there is none); `opened(W)` and `lettered(W)` from the flow read, which on the product side must therefore also return hold openings (§5) |

The two stock books do not join to each other: an unpaid invoice has no PSP counterpart by design.
The cross-ledger signal is in the flow leg, and in particular in its carry-over: every reference
whose drift is not 0 yet. Mostly **unapplied payments**, payments the PSP finalised that no product
transaction references yet, but also under- and over-applications and applications the PSP has not
finalised yet, which a later booking or PSP event can still settle. That set is reconciliation's own
open-items book. It is carried from day to day in the run artifacts, and it can always be recomputed
from the permanent logs.

The continuity identity is what makes the control **complete**. A window read that dropped an
event breaks the identity, so the loss is detected instead of silently shrinking the universe.

### 2.4 Why the shipped templates cannot express it

- **Aggregate only.** Every shipped template aggregates ([templates.md](../technical/templates.md)).
  A `balance_equation` over the two hold prefixes gives the **net open exposure**. But offsetting breaks cancel out, and settled items are not in any account set at all.
- **No per-item mode left.** Per-account fan-out was dropped for want of an alignment key,
  missing-row semantics and bounded evidence
  ([ADR-004 amendment](./adr-004-multi-source-comparisons.md)). This ADR supplies all three.
- **No history.** `stale_holds` ages open holds, which is part of the stock leg, but it has no flow
  leg and no counterparty side.

---

## 3. Why not query checkpoints (measured)

A checkpoint was the obvious first answer, because it freezes every ledger of a cluster at one Raft
index. We measured it on a throwaway single-node ledger holding two scopes of 1M accounts; the full
table is in the [design doc §7](../technical/transaction-level-reconciliation.md#7-measurements).

| | Live | At a query checkpoint |
|---|---|---|
| `AggregateVolumes`, 1M accounts | 2.9 s | **62 s** |
| `ListAccounts` scan, 1M accounts, one reader | 21 s | **5 min 10 s** |
| Keyed diff of two 1M scopes | 22 s | **10 min 25 s** (sequential at `0b4676d97`); **2 min 38 s** (parallel, on the tip) |
| Concurrent reads of one checkpoint | — | fail (`lock held by current process`, non-retryable `Unknown`) at `0b4676d97`; fixed by EN-2108 on the tip |

The cost is in reopening, not reading: every page reopens both checkpoint databases (finding F-b,
[design doc §7.3](../technical/transaction-level-reconciliation.md#73-what-the-numbers-say)).

The structural limits hold whatever the speed:

- **At most 10 live checkpoints per cluster**, shared with every tenant and with the ledger's own
  cron (`processor_query_checkpoint.go:21-24`).
- **Each create and delete is a Raft order.** The create gates the apply loop.
- **Disk grows with write churn × lifetime, on every replica.** The measured 100k-account checkpoint
  kept its 169 MB alive after the live store moved on.
- **A checkpoint cannot be created in the past.** It freezes the *run* instant, not the business
  cut-off, so a run at 00:07 still has to explain the seven minutes after midnight.

Storing a checkpoint in S3/Azure through the backup subsystem does not help either. A backup is the
whole store, kept for disaster recovery. Reading it back means restoring a node, and "it does not
provide point-in-time queries" (ledger backup README).

---

## 4. Options considered

| # | Option | Verdict |
|---|---|---|
| **C** | **A cut at the business cut-off, plus a rewind.** The cut is `S` (the last log id with `date ≤ cut-off`) and `T` (the last transaction id with `inserted_at ≤ cut-off`), on each ledger. The flow comes from `ListTransactions` over `(T_prev, T]`, filtered server-side (§5). The stock comes from a live listing rewound with the unfiltered transactions `(T, head_tx]` | **Adopted.** No checkpoint and no ledger change. Exact at the business cut-off on each side. Reproducible, because logs are permanent. Cost ∝ the day's payments plus the open items, plus the metadata watch, which reads every log of the ledger since the previous run (decision 25). |
| A | Shared, short-lived query checkpoint per (cluster, run): extract, then delete | **Fallback and oracle only.** Used to validate the rewind (§5) and possibly for a periodic or on-demand full proof. Too slow and too scarce as the steady-state path (§3). |
| B | Ledger-side consistent export (Pebble `NewSnapshot()` at a Raft-ordered trigger, streamed or written through `backup.Storage`) | **Not needed for this use case.** Still the right primitive for a frozen listing of a large *non-lettered* universe (EN-1480 generalised). Passed on to the Ledger team for information, not asked (§9). |
| D | Store the checkpoint in S3/Azure through backup | **Rejected** (§3). |
| E | Pebble primitives: EFOS, `Checkpoint(WithRestrictToSpans)`, `RemoteStorage` | **Rejected**, evidence in the [design doc §6](../technical/transaction-level-reconciliation.md#6-could-pebble-do-better). Pebble is not the bottleneck; the ledger read API is. |
| F | Paginate live without correction | **Rejected.** A 1M listing spans about 1,000 snapshots and tears under writes (measured, [design doc §7.4](../technical/transaction-level-reconciliation.md#74-rewind-proof)). |

### If checkpoint reads became as fast as live reads

[EN-2336](https://formance-team.atlassian.net/browse/EN-2336) asks the Ledger to remove the ~20×
read penalty at a checkpoint. **Option A would stay rejected even then**, for reasons that do not
depend on read speed:

- **The cap.** 10 live checkpoints per cluster, shared by every tenant and by the ledger's own
  checkpoint scheduler; eleven daily rules at midnight already exceed it (ADR-003).
- **The write path.** Each create and delete is a Raft order, and the create pauses the applier.
- **The wrong instant.** A checkpoint captures the run instant, so the stock would still need
  rewinding to `S`.
- **Past days.** Backfill and replay need a cut where no checkpoint exists. Keeping checkpoints for
  90 days is impossible under the cap, and would pin SSTs.
- **The flow needs no snapshot.** Transactions at or below `T` are immutable except for their
  metadata.

A fast checkpoint read would only make the rewind's oracle test (R10) cheaper to run more often.

## 5. Decision A — the cut is a log id, and the stock is rewound to it

**Choosing the cut.** On each ledger, `S` is the last log whose `date` (HLC, strictly monotonic)
is at or before the cut-off.

- Both ledgers are cut at the **same business time**, whatever their cluster and whenever the run
  starts. This is better than a checkpoint's cross-cluster semantics.
- `S` and `T` go into the capture, so the cut is identified exactly and can be re-derived later:
  the logs are immutable. There is no `logSha256`, a hash of the log at `S`: it would hold for one
  ledger protocol version only.
- **Resolving `S`.** `ListLogs` rejects `reverse`, so `S` is **(the first log with
  `date > cut-off`) − 1**: one ascending page of size 1 on the log-date index. `T` is resolved the
  same way on the `inserted_at` index.
- **The date filter is bounded** (decision 24): `cut-off < date ≤ cut-off + δ`, widening δ while
  the page comes back empty. The ledger materializes the whole range of a date filter before it
  pages it (`internal/query/compile.go:1382-1416` and `:1913` at `7dd615dba`), so an open
  `date > cut-off` costs a memory spike the size of the history for an old day's replay or a long
  backfill.
- **Both indexes are mandatory on both ledgers** (decision 12; §8 rule 8). The rule's
  validation rejects a ledger without them, and a run waits while they build, exactly as for the
  key's metadata index.
- **Bisection was considered and dropped.** Halving the contiguous id range would find the cut
  without the indexes, in about 20 reads for 1M rows. It is no faster, was never benched, and
  would be a second, rarely exercised code path. The rule already requires an index on its key,
  so two more do not change what onboarding asks for.

The reads, a worked example, and why the id range makes the metadata-filtered read cheap are in
the [design doc
§3](../technical/transaction-level-reconciliation.md#the-cut-from-a-business-time-to-id-ranges).

**Why the log date, not the transaction `timestamp`.**

- The log date is the insertion time, assigned by the ledger's HLC. It never moves, and it only
  grows. So "every log ≤ `S`" is a set that no later write can change: a day's result is final the
  moment it is computed.
- A transaction's `timestamp` is set by the writer. Connectivity sets it to the PSP event time, so
  it can be **backdated**: a payment captured at 23:58 but ingested at 00:03 carries a `timestamp`
  from day D and a log date from day D+1.
- A cut on `timestamp` would let late ingestion rewrite a day that was already reconciled. The cut
  on the log date instead places that payment in D+1's window, visibly and without losing it.
- The `timestamp` still serves ageing and reporting.

**Flow leg: `ListTransactions` filtered server-side, not `ListLogs`.** Logs cannot be filtered
server-side on metadata (`QueryFilter` allows only `ledger`, `log_id` and log `date` on
`QUERY_TARGET_LOGS`), so a busy product ledger would have its whole traffic read.
`ListTransactions` is filterable: on 1M transactions of which 10 % are payments, the filtered read
took 0.8 s on 8 id ranges, against 15.1 s for the logs ([design doc
§7.2](../technical/transaction-level-reconciliation.md#72-log-and-transaction-reads)).

So the flow is read with `ListTransactions`, filtered on `And(<membership>, id ∈ (T_prev, T])` and
split into parallel id ranges. The membership comes first: the ledger drives an `And` from its first
term, and with the dense id range first the product `Or` read measured 2.7 to 3.4 times slower
(design doc §7.11).

- On the PSP ledger, membership is `payment_ref EXISTS`, plus one `EXISTS` term per
  `psp.movementKeys` field when the rule declares them (decision 23): those transactions feed the
  payment-account book only.
- On the product ledger it is **`Or(payment_ref EXISTS, business_ref EXISTS)`**, with one
  `business_ref` term per hold kind (each `holds` entry names its business-id field). A business
  hold's opening (an invoice issued) carries no payment reference yet, but continuity needs
  `opened(W)`, so every product transaction that touches a business hold carries its `business_ref`
  (§8 rule 3).
- **Membership is the key's presence, not a `kind` tag.** Any transaction that carries the payment
  reference belongs to the flow, including a manual correction.
- It costs O(payments in the window), whatever else the ledger books.

Three caveats come with this choice, and each has a counter-measure:

1. **Transaction metadata is mutable, and logs are not.** The key and state metadata are never
   rewritten (a correction is a new transaction), and the metadata watch over every log since the
   previous run's head, `(head_prev, head]` (`S_prev` on a first run), reports any change to a
   transaction's key, state, business-id or merchant-reference field as `key_metadata_mutated`.
   How the watch is read is decision 25; the structural fix is immutable transaction labels (ask
   **L8**).
2. **The `payment_ref` index becomes mandatory.** While it builds, the read returns a retryable
   `Unavailable` with the reason `INDEX_BUILDING` (EN-2081, `f73eae1f3`), which recon matches on the reason, never on the message.
3. **No free count-based completeness check**, because a filtered range has gaps by design.
   Completeness rests on the index, aligned to the main-store horizon (ledger EN-1748), and on the
   continuity identity (§2.3).

**Stock leg (rewind).** The steps:

1. List the open holds under each of the side's hold prefixes (§6), live. The listing may tear.
2. When the listing ends, read the transactions `(T, head_tx]`, **unfiltered**, **newest first**
   (`ListTransactions`'s default order). The head is `GetLedgerStats.transaction_count`. For a daily
   run this window runs from the cut-off to the run, so it is short. It has the three properties the
   rewind needs:
   - it is **complete**: transaction ids are contiguous, so the count check `hi − lo` holds;
   - it is **immutable**: postings and `post_commit_volumes` never change;
   - it is **independent of any metadata convention**, because it is unfiltered. A hold touched by
     a transaction that forgot its key is still corrected.
3. For every hold touched in that window, discard the listed value and use its balance **just before
   its first touch after `T`**. That balance is the transaction's `post_commit_volumes` minus the
   transaction's own net posting on the hold. Read newest first, each touch overwrites the hold's
   value, so the last one written is the right one, and a hold back at zero is dropped at once
   unless the listing holds it. The fold then holds only the open book, however long the window
   ([design doc §7.16](../technical/transaction-level-reconciliation.md#716-replaying-an-old-day-from-head)).
4. Holds touched but absent from the listing are added back if that balance is non-zero. Holds
   created after the cut have a pre-balance of 0 and drop out.

Why this is exact:

- Only created and reverted transactions move a balance. A revert is its own transaction, with its
  own id and its own `post_commit_volumes` (ledger
  `internal/domain/processing/processor_revert_transaction.go:188-207` at `7dd615dba`).
- A hold untouched in the window held one value throughout the listing.
- A hold touched in it is recomputed from its own transaction.
- It needs no baseline and no stored state, and it works for NORMAL accounts too.

**Why the transactions and not the logs** (decision 22). The logs `(S, head]` have the same three
properties, but a read by transaction-id range takes the main-store path and never waits for the
index to align, so it is faster ([design doc
§7.8](../technical/transaction-level-reconciliation.md#78-window-source-for-balances-logs-or-unfiltered-transactions)).
The daily gain is small (about 2 s at 1M logs a day); it matters for the backfill and for replays,
and it leaves the watch as the only read of the logs, so a run would read none if the watch became
unnecessary (L8).
The logs keep only the metadata watch; there is no purge consistency check in V1 ([design doc
§4](../technical/transaction-level-reconciliation.md#4-the-rewind-an-exact-state-at-s-with-no-checkpoint)).

**Validated** against a checkpoint taken at the cut, with concurrent writers and reverts: the rewind
was wrong on **0** of 1,000,000 rows ([design doc
§7.4](../technical/transaction-level-reconciliation.md#74-rewind-proof) and §7.8).

**The checkpoint's role shrinks to an oracle.** It serves the rewind's regression test (R10) only;
V1 runs no periodic proof against a checkpoint.

## 6. Decision B — matching semantics

- **Key: the PSP payment reference.** Each side names the declared, indexed transaction metadata
  field that carries it, for example `payment_id` on the PSP ledger and `psp_payment_ref` on the
  product ledger. The product side also names the field that carries the **business id** of the hold
  it letters (`invoice_no`…), which gives the payment → invoice link. The key is **never taken from
  the hold address**, even on the PSP ledger where holds are named after the reference: with purged
  holds reachable by prefix, an address prefix costs O(history) per page, 47.8 s for a
  2k-transaction window on a 1M-payment history, while `payment_ref EXISTS` stays O(window) ([design
  doc
  §7.6](../technical/transaction-level-reconciliation.md#76-where-the-key-comes-from-transaction-metadata-not-the-hold-address)).
  The hold address keys the stock only.
- **States are parameters, not a fixed vocabulary.** How external payment states are modelled on
  the PSP ledger is a per-deployment choice. The rule therefore declares, **for each side**, the
  metadata field or fields and the value sets that mean `pending`, `final` and `failed`:

  ```json
  "psp": {"ledger": "psp", "key": "payments.formance.com/payment-id",
          "state": {"field": "formance.com/observation.event-type", "final": ["payin.succeeded"], "failed": ["payin.compensate"], "pending": ["payin.pending"]},
          "holds": [{"prefix": "fpay:stripe:payment:hold:pending:", "openSign": "positive"}], "grace": "7d",
          "paymentAccount": "fpay:stripe:account:*:main", "merchantRef": "merchant_ref"},
  "product": {"ledger": "main", "key": "psp_payment_ref", "grace": "1d",
          "state": {"field": "transition_kind", "final": ["to_final"]},
          "holds": [{"prefix": "main:hold:invoice:", "openSign": "negative", "businessId": "invoice_no"},
                    {"prefix": "main:hold:refund:",  "openSign": "positive", "businessId": "refund_no"}]}
  ```

  A transaction whose state value is in no set takes no part in matching, but it is **never dropped
  silently**: it is counted as `unclassified` in the statement, with a warning, and listed in the
  unclassified file. `formancepayments` books refunds on the original payment id as
  `payin.refunded` (`formancehq/connectivity-plugins-poc` @ `9df05c5b`), so a default mapping shows
  up there instead of vanishing; the [connector mapping
  checklist](../technical/transaction-level-reconciliation.md#mapping-a-connector-for-reconciliation)
  gives refunds their own reference. The rule's validation rejects overlapping sets.
- **Hold prefixes and their signs are parameters too.** Each side lists its hold kinds in `holds`,
  one entry per prefix, each with the sign of an **open** hold under it (`openSign`, `positive` by
  default). On the product side each entry also names its `businessId` field (`invoice_no`,
  `refund_no`…), since each kind of business object has its own id.
  - The sign depends on the integration, not on the side. A hold that is the destination of its
    opening posting opens positive (the `formancepayments` PSP hold); a hold that is its source
    opens negative (the owner's invoice hold, opened against pending revenue).
  - One side can mix signs. A refund hold (money owed to the customer) often opens with the sign
    opposite the invoice hold, so each kind gets its own entry.
  - The sign cannot be inferred: a hold may have been opened before the window, and the stock sees
    only its balance, where −500 could be an open invoice or an over-application.
  - Validation rejects overlapping prefixes on one side, so every hold has exactly one sign.
- **`psp.movementKeys`** (optional, decision 23, §8 rule 10) names the declared,
  indexed metadata fields that key the payment account's other movements, such as payouts and
  fees. The PSP flow read adds one `EXISTS` term per field; those transactions feed the
  payment-account book only, never matching.
- **`psp.merchantRef`** (optional) names the PSP metadata field holding the merchant's reference.
  When set, an unapplied payment whose merchant reference names an open hold is paired with it
  ("invoice X is paid: apply it").
- **An application's amount is its net posting on the accounts under the side's hold prefixes**,
  each counted in the direction that settles it: an invoice hold that opens at −X is settled by +X.
  A transaction that carries the key but moves no hold, such as a revenue recognition booked in the
  same batch as the application, therefore counts for nothing.
- **A PSP payment's amount is its net posting on the side's `paymentAccount`**, the account a final
  event credits with the payment. It is an address pattern where `*` matches one segment
  (`fpay:stripe:account:*:main` for `formancepayments`), matched in memory on the postings the flow
  read already returns, and counted in absolute value.
  - The hold cannot give it. `formancepayments`' `payin.succeeded` takes the payment amount from the
    hold first and from the provider mirror `account:{acct}` for any shortfall, so the net on the
    hold is 0 for a final event that no `pending` preceded, and the `pending` amount, not the paid
    one, when the two differ.
  - The hold still gives the PSP stock and its continuity. A `pending` or `failed` event shows its
    hold movement as `holdAmount` in the flow rows, never as `amount`.
  - The postings are immutable, so this needs no connector change and no amount metadata.
- **No tolerance.** The comparison is exact: a fee or FX difference on a payment is a break, never
  an accepted gap. Fees and FX must be booked explicitly on the side that bears them.
- **Refunds and chargebacks are their own 1-to-1 pairs.** A refund is a **separate PSP payment**,
  with its own reference, on the PSP ledger. On the product ledger it is a **refund hold**, lettered
  by a transaction carrying that reference. It flows through the same classes as any payment.
  Refunds are not modelled as a reversal of the original payment, because PSPs and product
  implementations differ too much for that to be general.
- **Cardinality.** The join is on transactions, per payment reference. One payment may be applied by
  several product transactions: split across invoices, or applied in parts. Their amounts are
  **summed per reference** before comparison. One invoice settled by several payments is a
  stock-side fact (the business hold only letters to zero once), not a flow break.
- **References missing from the window are looked up by key**, on the PSP ledger up to `T` and,
  when that finds a final state, on the product ledger too; a window reference with a `failed`
  event that is not carried is looked up on both ledgers, even when the product books on it the same
  day. So `over_applied` and `reversed_after_application` are caught across days. The cost is
  O(looked-up references), counted in the run's metrics ([design doc
  §5](../technical/transaction-level-reconciliation.md#5-matching-semantics)).
- **Classes.** The flow classes (`matched`, `under_applied` / `over_applied`, `unapplied_payment`,
  `in_progress`, `failed`, `applied_before_final`, `orphan_application`,
  `reversed_after_application`) and the stock classes (`open` with its age bucket, `wrong_sign`,
  `stuck`, `cleared`), with their outcomes and priorities, are defined in the [results
  reference](../technical/transaction-level-results.md#flowndjsongz). `orphan_application` and
  `reversed_after_application` are priority 1; `wrong_sign` and `stuck` are priority 4. The two
  stock books are aged, never joined to each other.
- **Each side has its own `grace`: how long it may lag behind the other.** `product.grace` is how
  long the product has to apply a payment the PSP finalised (`unapplied_payment`); `psp.grace` is
  how long the PSP has to finalise a reference the product already applied
  (`applied_before_final`, unknown references included, which then becomes `orphan_application`).
  An unknown reference gets the same delay because the PSP's `pending` event can land after the
  cut (product at 23:58, PSP at 00:02). A `breakOn` is `firstSeen` plus the lagging side's `grace`.
  At 0, the side may not lag at all: `psp.grace: 0` is the integration where the product applies
  only on the PSP's final state, so an early application is a priority-1 break at once, including
  that cross-cut race, which then resolves the next day.
- **Grace and ageing: proposed defaults, to calibrate with the design partner.**
  - `product.grace` = **1 calendar day**: the product applies a payment as soon as the PSP
    finalises it, so the day of grace only absorbs a payment finalised before midnight and applied
    after it. A team that letters by hand raises it to its usual delay.
  - `psp.grace` = **7 calendar days**: a direct debit (SEPA, ACH) final at D+5 business days spans a
    weekend, and applying at `pending` is common with those debits.
  - Age buckets `0–1 d`, `2–7 d`, `8–30 d`, `> 30 d`.
  - `maxAge` has no default: without one, no hold is ever `stuck`. A rule sets it only where an
    open hold past it is abnormal. For B2B receivables it stays unset: ageing them is credit
    management, and the buckets still show it. `stuck` is the `stale_holds` signal per key.
  - All four are rule parameters.
- **`breakId`.** Ageing compares with the previous run's artifact, matched by a `breakId` that
  hashes the rule, leg, key (`ref`, or `side` + `hold`) and asset, not the class: a break that
  changes class stays the same break, with its comments, and its earlier class is on the previous
  run's row with the same `breakId`. Lifecycle and reopening are in
  the [results reference
  §7](../technical/transaction-level-results.md#7-how-rows-move-from-day-to-day).
- **Arithmetic.** Exact integer minor units, colors collapsed per asset, and multi-asset through
  `asset: "*"` as in ADR-004.
- **Schedule and alerts reuse the existing model.**
  - The rule runs on a **daily schedule by default**. It takes the existing `periodType` (`daily`,
    `weekly` or `monthly`), and the alert identity is `(rule, fingerprint, period)` exactly as in
    [alert-period-model.md](../technical/alert-period-model.md). With `monthly`, the month's alert
    is opened by the first failing daily run and updated by the following ones.
  - **The alert carries the aggregate comparison.** Its evidence holds the net and the gross
    per asset, the class counts per leg, the continuity check, the top-K breaks and the link to the
    day's files.
  - **What the alert says: a reconciliation statement**, never a bare drift: a verdict, a bridge
    whose unexplained residual must be 0, the open items, the gross next to the net, and the breaks
    in priority order ([results reference §4](../technical/transaction-level-results.md#4-the-verdict)
    and [§5](../technical/transaction-level-results.md#5-the-statement)). An `incomplete` run is
    never green. The payment-account book's residual is a P1 break, not an `incomplete` run
    (decision 23); a run that fails every day is decision 26.
  - **What opens the alert:** at least one open break. A known break stays one until it is booked;
    the controller acknowledges or accepts the alert itself, as for any rule
    ([alert-period-model.md](../technical/alert-period-model.md)). The net alone never
    opens it, since pending items move it and offsetting breaks cancel in it. An `incomplete` run
    opens the engine-error alert instead.
  - There is never one alert per payment (the reasoning of the ADR-004 2026-09-08 amendment).

## 7. Decision C — one asynchronous job, detail kept 90 days in the backup storage

1. **The scheduler tick only enqueues the run.** There is no synchronous capture: an aggregate
   read live at the tick would give the exposure at the run instant, not at the cut, and the job
   computes the exact aggregates at `S` anyway, as sums over the rewound rows.
2. **The run is one asynchronous job**, idempotent per (rule, period, cut).
   1. Resolve `S` and `T` on each ledger, then read the flow window and join, including the
      previous run's carried items and a key lookup of the references missing from both.
   2. Stock rewind and ageing.
   3. Write the data files.
   4. Write the run's **capture**: counts, drifts, `S` and `T` per ledger, and the artifact URI and
      the SHA-256 of the manifest, computed before it is written, signed with Ed25519 (EN-1930).
   5. Write the **manifest**, last of the run's files.
   6. Update the alert.

   The writes follow that order, and **a run exists once its manifest is written**: a job that
   stops earlier leaves files and perhaps a capture that no reader counts, and the day's current
   run is unchanged. A run that did not finish is started again from the beginning, with a new
   `runId`, when the process restarts or at the next tick; nothing records its progress, since a
   run takes minutes and the same cut gives the same result. Its leftover files expire under the
   prefix's lifecycle rule.

   The alert is derived from the manifests, never stored beside them: **every tick brings the
   rule's alert up to date from the latest current run's manifest**, and the update is idempotent.
   A job that stops after its manifest and before the alert therefore loses nothing: the next tick
   applies it.
3. **Where the files go: the backup object storage, under a recon prefix.**
   - Recon writes to the S3 or Azure destination the rule's **product ledger** backs up to, under
     `{bucketID}/reconciliation/rule={ruleId}/day={YYYY-MM-DD}/run={runId}/`. Files and paths:
     [results reference
     §2](../technical/transaction-level-results.md#2-where-the-files-are-and-which-run-counts).
   - **The customer is the first reader.** They analyse the files with their own tools (jq, DuckDB,
     pandas); recon's API and UI read them too. So the format stays simple: gzipped NDJSON, a JSON
     Schema per file and a `schemaVersion`, self-contained break rows with a stable `breakId`, and a
     manifest that carries the statement and the triage, so the alert and a dashboard need no other
     file. The [results reference](../technical/transaction-level-results.md) is the source of
     truth for every file and field, and for the [rules a reader can rely
     on](../technical/transaction-level-results.md#8-rules-a-reader-can-rely-on) (byte-identical
     files for a given cut, `incomplete` runs, the current run of a day).
   - The prefix **must stay outside `{bucketID}/backups/`**: the ledger's orphan prune deletes every
     unreferenced object under `backups/data/` and `backups/exports/`, and never touches a sibling
     prefix.
   - Recon brings its own credentials for that destination, and the same drivers (`s3`, `azure`).
     It also accepts `file` for local development.
   - The manifest's hash sits in the signed capture, so the signature covers the files transitively.
     Readers find runs by their manifests, never by their data files.
4. **Retention: a deployment setting, applied by one storage lifecycle rule.**
   - Recon deletes no file. One lifecycle rule of the storage on `{bucketID}/reconciliation/` (S3
     lifecycle, Azure lifecycle management) deletes the files once they are older than the
     retention. The operator sets it at installation, and it is **required**: without it the files
     are never deleted. How to set it: [design doc
     §5](../technical/transaction-level-reconciliation.md#result-artifacts-and-retention).
   - The retention is the `serve` flag `--lettering-retention`, 90 days by default (item 8), and
     the lifecycle rule carries the same age. It is not a rule parameter: it applies to every rule
     under the product ledger's prefix. The manifest's `expiresAt`, the run's day plus the
     retention, is for information only. Recon uses the retention to know up to when a replay
     reproduces a day's files byte for byte (item 7).
   - Expiry loses nothing irrecoverable. The logs are permanent, so any past day can be recomputed
     from the ledgers with the same cut and engine version, which each manifest records (item 7). A
     customer bound to a longer legal retention raises the flag and the lifecycle rule together.
   - The `file` driver has no expiry, which is fine for local development.
5. **A period points at each day's diffs.**
   - The period is the rule's `periodType`, calendar-based in the rule's timezone. There is no
     separate accounting-period model.
   - The period's alert, built from the period's **daily manifests** rather than from the ledgers,
     lists each day that has a current run, with its counts, its net and gross, its breaks and the
     link to its files. A closed period's alert is never rewritten.
   - A day with no complete run is simply not listed: the next complete run's window covers it
     ("window since …"), and each `incomplete` run already raises the engine-error alert
     (decision 26). There is no gap state.
   - **No period file.** Any other view of a period is a query over its daily manifests, which are
     kept for as long ([results reference
     §9](../technical/transaction-level-results.md#9-queries)).
   - The 90-day default retention covers a monthly period plus a review margin.
6. **First run: bounded backfill.** A rule's first run has no previous day, so no carried items.
   Left alone, a payment the PSP finalised before the rule existed, and that the product never
   applied, would never be seen. On the PSP side its hold is already lettered, and it is in no later
   window.
   - The first run therefore reads the flow from **`backfillFrom`**, a rule parameter that
     defaults to **cut-off − max(`psp.grace`, `product.grace`) − 1 day**, instead of from the
     previous day's cut. That seeds the carried items.
   - Everything older than `backfillFrom` is out of scope. The first statement says so explicitly
     ("backfilled since …"), so it cannot be misread as covering all history.
   - On the product ledger, the first run's window starts `psp.grace` earlier than on the PSP
     ledger. An application may precede its payment's final state by up to `psp.grace`, so a payment
     finalised early in the backfill still finds its application, with no lookup by key. Those
     earlier product transactions only feed the join. An application older than that waited past
     `psp.grace` and is a break anyway; it shows as an unapplied payment.
   - The stock books need no backfill. They come from the listing, so an invoice unpaid for 60 days
     is aged correctly from day one.
   - Continuity is available from the first run as well: the rewind can rebuild `open(S_prev)` for
     any past cut, from the live listing and the transactions `(T_prev, head_tx]`.
   - Re-running with an earlier `backfillFrom` is idempotent per (rule, period, cut). It only costs
     a longer window read.
   - The same first run restarts a rule whose chain is stuck on a cause that cannot be fixed inside
     its window; `backfillFrom` then defaults to the earlier of the first-run default above and the
     oldest `firstSeen` of the last complete run's carried items, which is the first-run default
     when nothing was carried (decision 26).
7. **Replaying a past day.** Any past day can be replayed: the logs are permanent, and its cut
   (`S` and `T` on each ledger) is in the signed capture. A replay runs the daily algorithm as of
   that day, and no stock is stored for it.
   - The flow costs the same at any age, one day's id range `(T_prev, T]`.
   - The stock is the live listing rewound from head: about 6 min at the end of the default 90-day
     retention and 26 min a year later, at 1M transactions a day, in the memory of the open book
     (measured on 20M transactions, [design doc
     §7.16](../technical/transaction-level-reconciliation.md#716-replaying-an-old-day-from-head)).
   - Within the retention (item 4), the previous day's carried and stock files seed the replay,
     which reproduces the day's files byte for byte. Beyond it, the carried items are rebuilt from a
     backfill window, as on a first run (item 6), and the statement says so. Mechanics: [design
     doc](../technical/transaction-level-reconciliation.md#replaying-an-old-day).
8. **Execution and retention settings are operator settings, not rule parameters.** They are
   `serve` flags (with the matching environment variables), like `scheduler-interval`, absent from
   the rule contract and the API. The team running the deployment tunes them through Helm or the
   Operator:
   - `--lettering-read-ranges` (default 8): the number of id ranges read concurrently, for the
     flow, the rewind window and the metadata watch;
   - `--lettering-max-concurrent-reads` (default 16): caps the readers across every run
     of the process; a read that would exceed it waits for a slot;
   - `--lettering-retention` (default 90 days): how long a run's files are kept. It sets the
     manifest's `expiresAt` and bounds the byte-identical replays (item 7); the storage's lifecycle
     rule, set to the same age, deletes the files (item 4).

   K, the per-step durations and the read counts are exported as metrics and logs, not written to
   the manifest. How K was chosen: [design doc
   §7.7](../technical/transaction-level-reconciliation.md#77-concurrent-readers-choosing-k).
9. **Reads.** The run's status comes from its capture, for a run whose manifest is written.
   Breaks are paged from the artifact by the API, and the API lists every file of a run with a pre-signed URL, so a customer reads them
   without access to the bucket.

## 8. Booking conventions we recommend

The full table, the reasons and the connector checklist are in the design doc ([recommended booking
design](../technical/transaction-level-reconciliation.md#recommended-booking-design-adr-005-8)). The
rules that the engine's efficiency depends on:

1. **EPHEMERAL holds, one prefix per kind**: one hold per external payment on the PSP ledger, one
   per **business object** (not per state) on the product ledger, each prefix declared in `holds`
   with its sign (§6).
2. **One transaction = one event of one payment reference.**
3. **Declared transaction metadata**: `payment_ref`, `merchant_ref`, `state`, `kind` on the PSP
   ledger; `payment_ref`, a `business_ref` per hold kind and `kind` on the product ledger, where
   **every** transaction that touches a business hold carries its `business_ref`, including the one
   that opens it. `payment_ref` **must be declared and indexed on both ledgers** and `business_ref`
   **must be indexed** on the product ledger, since the flow read filters on them (§5); `merchant_ref` is indexed for investigation and named
   as `psp.merchantRef`. **These fields are write-once** (§5, caveat 1).
4. **Strict amounts, explicit fees and FX** (decision 6): `send [$asset $amount]`, never `*`; PSP
   fees are their own posting to `psp:{conn}:fees`.
5. **Clearing account** `main:clearing:{conn}` on the product side, as an aggregate control total.
6. **`reference = {payment_ref}:{state}`** on the PSP side and `{payment_ref}:{business_ref}` on the
   product side: idempotent on re-delivery.
7. **`timestamp` = event time; the log date is the cut.**
8. **Log-date and `inserted_at` indexes on both ledgers: mandatory** (§5).
9. **Traffic unrelated to payments costs nothing on the flow leg.** Only the rewind window and the
   metadata watch read all traffic; metadata-only writes are best avoided, since each one is a log
   the watch reads.
10. **One payment account per payment kind on the PSP ledger, and a declared key on every movement
    of it**, named as `psp.paymentAccount` (decision 17). No other flow uses it. Every credit is a
    payment final that carries `payment_ref`, and every debit carries a declared key (`payment_ref`
    for a refund, the reference of its own object for a payout or a fee, `psp.movementKeys`). Its
    book is then a strict check, the only one that sees a final with no `pending` and no reference; recon checks
    it (decision 23, [design doc
    §7.9](../technical/transaction-level-reconciliation.md#79-a-final-with-no-pending-and-no-key-the-payment-account-book)).

On the PSP ledger, these conventions come from the **connector mapping**, configured per customer at
implementation time ([checklist](../technical/transaction-level-reconciliation.md#mapping-a-connector-for-reconciliation)).
No connector change is required.

## 9. Upstream asks

| Ask | Why | Size |
|---|---|---|
| **L2** ([EN-2327](https://formance-team.atlassian.net/browse/EN-2327)): drop the per-account INFO line `scanAccount complete` on list paths. **Done** in formancehq/ledger#2128 (`199bee364`): logged at TRACE | A listing of 1M accounts wrote 1M log lines | XS |
| **L5** ([EN-2331](https://formance-team.atlassian.net/browse/EN-2331)): a tested contract that **a purged EPHEMERAL account's transactions stay reachable through indexed transaction metadata and `reference`**. **Closed** with formancehq/ledger#2058 (`38c6eef55`) without such a test; the paths behave correctly (probed on `7dd615dba`), so **recon pins the contract itself**: EN-2318 for the flow, EN-2319 for the logs | The flow leg finds lettered items through indexed transaction metadata, and investigations use `reference`. Nothing in this design reads by address. Not blocking | S |
| **L6** ([EN-2328](https://formance-team.atlassian.net/browse/EN-2328)): `ListLogs` throughput. On the same 1M transactions it is 5–9× slower than `ListTransactions` (13.8k/s against 94.5k/s on one stream). At `7dd615dba` the gap holds (×5.4 on 8 ranges, ×9 on one stream), except in one session of the node where the same reads ran 4 to 9 times faster; that variance is part of the ask (design doc §7.13) | Only the metadata watch still reads logs, the rewind having moved to the transactions (§5); the watch is the largest step of a daily run, so the gap deserves an explanation | S–M |
| **L8** ([EN-2326](https://formance-team.atlassian.net/browse/EN-2326)): **immutable transaction labels**. Key/value pairs set when a transaction is created, never changed by `SavedMetadata` or `DeletedMetadata`. They are declared and typed like metadata, indexed as **add-only** (like `reference` or `timestamp`, with no old-value history to resolve at a pin), and filterable with equality, `EXISTS` and prefix on `ListTransactions`. Because they never change, they can also be filterable on `ListLogs`. | Removes caveat 1 of §5 by construction instead of by convention: a filtered re-read of a past window becomes as reproducible as the logs. Cheaper to index than mutable metadata. Gives the payment key an immutable, auditable home. `reference` comes close (immutable, indexed) but is single-valued, unique and exact-match only, so it cannot drive a window filter | M |
| **L9** ([EN-2356](https://formance-team.atlassian.net/browse/EN-2356), epic EN-1336, Ledger v3.1): make a read's cost independent of the order of an `And`'s terms. Led by a dense id range, the `And` seeks its membership once per row, and seeking an `Or` seeks every term (`internal/query/compile.go:299-346`, `internal/storage/readstore/combinator_or.go:69-85` at `7dd615dba`) | The product `Or` of three keys read 2.7 to 3.4 times slower id range first (design doc §7.11). Recon writes the membership first, so not blocking; other clients pay it unknowingly | S |
| **L10** ([EN-2369](https://formance-team.atlassian.net/browse/EN-2369), epic EN-1336, Ledger v3.1): a `ListLogs` filter on the logs the metadata watch needs, the `SavedMetadata` and `DeletedMetadata` that target a transaction. Today `QueryFilter` allows only `ledger`, `log_id` and the log date, with `And`, `Or` and `Not`, on `QUERY_TARGET_LOGS` (`misc/proto/common.proto` at `7dd615dba`) | The watch reads every log of the ledger to find a few: 4.1M logs in 134–158 s for a 1M-payment product ledger, about 95 % of the run (design doc §7.15). Filtered, it would read only the rare logs it keeps. Not blocking: the full read costs about two minutes a run, which a nightly batch affords (decision 25) | M |
| **L7** ([EN-2329](https://formance-team.atlassian.net/browse/EN-2329)): return the snapshot horizon (the per-ledger log id the read saw) on `AggregateVolumes` and `ListAccounts` | Lets the rewind skip the untouched part of the window; it is already part of EN-1480's scope (`log_sequence`) | S |

Only these are asked, because only these serve this design. L2 is done and L5 is closed.

**Findings passed on for information, not asked.** Reconciliation takes no query checkpoint in an
evaluation, so it does not ask for any of the following. The measurements stay in the design doc
([§8](../technical/transaction-level-reconciliation.md#8-ledger-findings-and-asks), F-b, F-g, F-h)
for the Ledger team to weigh against its own users:

- **Checkpoint reads are about ×20 slower than live reads**: every page reopens both databases.
  Filed at the Ledger team's request as [EN-2336](https://formance-team.atlassian.net/browse/EN-2336)
  (ex-L1), theirs to prioritise. The rewind's test oracle can afford the slowdown.
- **No consistent export**, meaning no single-snapshot multi-page listing, and **checkpoints have
  no owner and no TTL**. These served options A and B only.

## 10. Decisions taken and what remains open

| # | Question | Decision |
|---|---|---|
| 1 | The shared key | The **PSP payment reference**, carried by the transactions on both ledgers. Authorization/capture, where both sides share the authorization number, is the special case (§2.1, §6). |
| 2 | The state vocabulary | **Parameterised per side** in the rule, because it depends on how external payment states are modelled on the PSP ledger (§6). |
| 3 | Grace and ageing | `grace` is per side: `product.grace` 1 day and `psp.grace` 7 days by default (decisions 16, 20). Age buckets 0–1, 2–7, 8–30 and > 30 days; all are rule parameters, to calibrate (§6). |
| 4 | Results storage | The **backup object storage**, under a recon prefix outside `backups/`, kept **90 days** by default. The retention is a deployment setting (`--lettering-retention`), not a rule parameter, and one storage lifecycle rule on `{bucketID}/reconciliation/`, required at installation, deletes the files; recon deletes none. A longer legal retention raises both. No longer-kept monthly anchors (§7). |
| 5 | Scope | Transaction-level reconciliation is **in the reconciliation project's scope**. The PRD is amended accordingly. |
| 6 | Tolerance per payment (fees, FX) | **None.** The comparison is exact, and any difference is a break (§6). |
| 7 | Refunds and chargebacks | **Each is its own 1-to-1 pair**, never a reversal of the original payment (§6). |
| 8 | Schedule, period and alert | A **daily schedule** by default and the existing `periodType`; no accounting-period model. The alert carries the aggregates, the detail sits in the backup storage (§6, §7). |
| 9 | First run | A bounded **backfill** from `backfillFrom` (default: cut-off − the longer `grace` − 1 day), announced in the first statement (§7). |
| 10 | Read path of the flow | **`ListTransactions` filtered on the key's presence** (product side: `payment_ref` or `business_ref`), membership before the id range, over parallel id ranges: O(payments). The logs keep the metadata watch and exact re-derivation (§5). |
| 11 | Hold signs | Each side declares **`holds: [{prefix, openSign}]`**, since the sign cannot be inferred. `wrong_sign` is the sign opposite `openSign`, and continuity runs per prefix (§5, §6). |
| 12 | The cut's indexes | The **`inserted_at` and log-date indexes are mandatory** on both ledgers, and bisection is dropped. An index missing at run time is an engine error (§5). |
| 13 | Concurrent readers | K is an **operator setting** (`--lettering-read-ranges`, default 8, capped by `--lettering-max-concurrent-reads`, default 16), absent from the rule and the API (§7). |
| 14 | Replaying an old day | The **daily algorithm as of that day**: the live listing rewound from head, newest first, with no stored stock or anchor (26 min a year later at 1M transactions a day, extrapolated from 20M measured). Beyond the retention, the carried items are rebuilt from a backfill window (§7). |
| 15 | Result files | For the customer first: gzipped NDJSON under `rule=/day=/run=`, a stable `breakId`, drifts carried to the next run, byte-identical files for a given cut ([results reference](../technical/transaction-level-results.md)). |
| 16 | Application before the PSP's final state | A **legitimate booking choice**, not a break: `applied_before_final` stays pending within `psp.grace`, unknown references included, then becomes `orphan_application` (P1). Missing references are looked up by key, and the alert opens on a break, never on the net alone (§6). |
| 17 | The PSP payment's amount | The **net posting on `psp.paymentAccount`** (an address pattern), not on the hold, which a final event with no `pending` before it moves by 0 (§6). |
| 18 | Cases across days | A PSP `failed` never applied is `failed` (ok), and window references with a `failed` event that are not carried are looked up on both ledgers every run, so `reversed_after_application` is caught. The rest (a `businessId` per `holds` entry, `psp.merchantRef`, the previous stock read from storage, the watch since the previous head) is in §5 and §6. |
| 19 | Simplifications | Flow rows carry no `firstSide`: which side came first follows from `firstSeen` and the row's `psp` and `product` events. The first run does no product-side lookup: its product window starts `psp.grace` before `backfillFrom` (§6, §7). |
| 20 | Default `product.grace` and deferred application | `product.grace` defaults to **1 day**; a business that applies later or by hand raises it. A payment-to-apply hold is an option outside the V1 rule contract ([design doc](../technical/transaction-level-reconciliation.md#when-application-is-deferred-or-manual-a-payment-to-apply-hold)). |
| 21 | Result files, statement and triage | Every check in the statement ties two independent computations, and a flow class reads net amounts (an application undone counts as none). The rest is as the [results reference](../technical/transaction-level-results.md), the source of truth for the format, specifies. Deliberately left out: a write-off state, a flat transactions file, a separate alert threshold, a break's `previousClass` (the previous run's row with the same `breakId` has it) and an `offsetting` flag next to `flowGross`. |
| 22 | Window source of the stock rewind | The rewind reads the **unfiltered transactions `(T, head_tx]`**, newest first, not the logs, and so do replays. The logs keep only the metadata watch (§5, §7). |
| 23 | The PSP payment account | A booking convention (§8, rule 10), with the keys of the account's other movements in `psp.movementKeys`. **A residual of its book is a P1 break**, `unkeyed_payment_movement` on the leg `book`, not an `incomplete` run, so that one keyless final cannot hide the rest of the day ([results reference §5](../technical/transaction-level-results.md#5-the-statement)). |
| 24 | Bounded date filters in the cut | `S` and `T` are resolved with an upper-bounded date filter, widened while empty, because the ledger materializes a date range before paging it (§5). |
| 25 | How the metadata watch is read | **In full, by the run**, over K ranges: about 95 % of a run, 134–158 s at 1M payments a day, which a nightly batch affords. An incremental read in slices during the day is **deferred after V1**: it would save about two minutes a run for a job per ledger and a stored slice chain. Revisit if a run grows too long and L10 ([EN-2369](https://formance-team.atlassian.net/browse/EN-2369), §9), which would shrink the read, is not delivered ([design doc §3](../technical/transaction-level-reconciliation.md#the-cut-from-a-business-time-to-id-ranges)). |
| 26 | A run that fails every day | Some `incomplete` causes repeat on every run while each window grows, until they are fixed. `incomplete.detail` names the first 20 items at fault, and the engine-error alert says whether the next run retries (`missing_index`, `short_range`) or an operator must act. When the fix cannot enter the window, the operator **restarts the rule**: its next run is a first run, with its stock rewound and its flow backfilled from the oldest open item of the last complete run, so nothing stored is trusted and the open items are found again (results doc §2). No re-seed run type, no `diagnostic.json`, no `incomplete.kind`. |

**Open, to review with the Connectivity team (no decision):** decision 23 assumes that the payment
account is credited only by payment finals. `formancepayments` also credits it from payouts,
transfers, compensations and reversed refunds. These carry the payment key, so the book closes on
them, but they are `unclassified` every day. Its conversions and order fills post on the account
under ids of their own, which only `psp.movementKeys` would bring into the book. The facts, the
options and what they mean for the debit book are in the [design doc
§2](../technical/transaction-level-reconciliation.md#mapping-a-connector-for-reconciliation).

**Nothing blocks the tickets.** An accounting-period model (fiscal calendars) can come later as a
new `periodType` without changing this design.

## 11. Consequences

- **Recon gains object storage** (the backup destination, under its own prefix) as its first durable
  dependency outside the ledger. It stays stateless in process. The artifacts are outputs, plus the
  previous run's carried items and ageing. Every one of them can be recomputed from the permanent
  logs.
- **Installing recon includes a lifecycle rule on the storage.** Recon deletes no file: the
  operator sets one lifecycle rule on `{bucketID}/reconciliation/` for each product ledger's
  destination, with the age of `--lettering-retention` (§7 item 4). Without it the files are never
  deleted.
- **The result store is built for lettering first.** Its first other consumer would be
  `stale_holds`, which should keep its flagged holds as an artifact instead of only a query to re-run
  ([stale-holds.md
  §9](../technical/stale-holds.md#9-revisit-after-adr-005--keep-the-flagged-holds-as-a-result-artifact-),
  [EN-2324](https://formance-team.atlassian.net/browse/EN-2324)). EN-2324 generalises it when it
  starts; the parts it would reuse are the layout, the manifest, the hash in the capture and the
  lifecycle rule on the whole prefix.
- **ADR-003 stands.** Evaluations never take query checkpoints; one is used only as a test oracle
  (R10).
- **A new template kind, with its own async execution path**; a job that stops is started again
  from the beginning. The scheduler's
  10 s drain grace does not apply to it ([scheduler.md](../technical/scheduler.md)).
- **Recon starts reading `ListTransactions` in bulk, and `ListLogs`.**
  - From the transactions, it reads every one that moves a balance: created transactions and revert
    transactions, which carry their own id. The flow reads them filtered, in id order
    (`reverse = true`, since the API lists newest first); the rewind and the replays read them
    unfiltered, newest first.
  - From the logs, it reads only `SavedMetadata` and `DeletedMetadata` on transactions, for the
    `key_metadata_mutated` monitoring. It ignores the rest.
