# ADR-005 — Transaction-level (lettering) reconciliation: a log-window cut instead of query checkpoints

**Status:** Proposed. The design is under evaluation and nothing is implemented. The owner's answers
of 2026-09-24 settle the questions of the first draft (§10).
**Tracking:** epic [EN-2315](https://formance-team.atlassian.net/browse/EN-2315). Wave 1 is EN-2316 to
EN-2323 (R1–R8). Wave 2 is EN-2333 (R9 period summary), EN-2334 (R10 rewind oracle test) and
EN-2335 (R11 booking guide). EN-2324 reuses the result store for `stale_holds`. Ledger asks (§9): L2 EN-2327,
L6 EN-2328, L7 EN-2329, L8 EN-2326, L5 EN-2331.
**Date:** 2026-09-24
**Decision owners:** Reconciliation maintainers
**Related:** [ADR-002](./adr-002-pit-consistency.md) · [ADR-003](./adr-003-checkpoint-anchor-and-crosscheck.md) · [ADR-004](./adr-004-multi-source-comparisons.md) · [design, measurements and evidence](../technical/transaction-level-reconciliation.md)
**Upstream facts verified at:**

- ledger `release/v3.0` @ `0b4676d97` (local checkout) and @ `a08f99bc3` (`origin/release/v3.0` tip, 2026-09-24);
- ledger branch `codex/en-2036-purge-ephemeral-accounts` @ `92b378e4b`, then @ `20a5595d6`;
- Pebble `v2.1.4`;
- Connectivity: `ledger-connect` @ `e7ca3e29` and `ledger-connect-plugins-poc` @ `89f4eb72`.

---

## 1. Decision in one sentence

A transaction-level control reconciles a **PSP ledger** against a **product ledger**, per **PSP
payment reference**. The payment is first seen on the PSP ledger. The product ledger later books a
transaction carrying the same reference, which letters a *business* hold (an invoice, an order…).

The control defines its cut as **one log id per ledger, taken at the business cut-off**. It
reconciles two legs:

- the **flow**: what each ledger booked during the window, read with `ListTransactions` filtered
  server-side on the presence of the reference, and joined on it;
- the **stock**: what is still open at the cut, rebuilt exactly as of the cut by **rewinding a live
  listing with the log window**.

**Query checkpoints are not used.** The control reads filtered transactions for the flow, and live
listings corrected by the short log window since the cut-off for the stock.

The synchronous path reports aggregate figures. The per-key
detail is computed asynchronously and stored in object storage, and its hash is anchored in the
signed `_recon` capture.

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

**The two ledgers do not key their holds by the same id** (owner, 2026-09-24).

- On the **PSP ledger**, the hold tracks the *external payment* and is keyed by the **PSP payment
  reference**. The payment is seen here first.
- On the **product ledger**, the hold tracks a *business object*: an invoice, an order, a
  subscription period. It is keyed by that object's id, for example `…:hold:invoice:open:{invoice_no}`.
  When the payment arrives, the product books a transaction that **carries the PSP payment
  reference** and letters that business hold.
- Authorization/capture, where both sides know the authorization number from the start, is the
  special case in which the two ids coincide.

**The join key is therefore the PSP payment reference, on transactions.** Hold addresses are not
the join key. The link from a payment to the invoice it settled exists only in the product
transaction: its postings name the business hold.

Hold accounts are **EPHEMERAL**, so a lettered hold is **purged** at zero. Connectivity already
books this way:

- the `formancepayments` profile uses `fpay:{conn}:payment:hold:pending:{payment_id}` (EPHEMERAL);
- its transactions carry `payment_id`, `event_type` and `transition_kind` metadata;
- `payment_id` and `event_type` are indexed (`plugins/formancepayments/profiles/formancepayments.yaml:59-67, 715-811`).

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
transaction metadata or `reference`**. The address alone cannot find it. The control reads through the
metadata; `reference`, being single-valued and exact-match, only serves point lookups.

**Re-checked on EN-2036's head** (formancehq/ledger#2058 @ `92b378e4b`, open, awaiting review), with
the same probe (`tools/bench-txlevel probe`) on an isolated ledger: **the observed behaviour is
unchanged.** The PR purges the account row and its metadata too. Its documentation says the
account-to-transaction mappings are immutable history that survives the purge, and its indexer now
excludes only TRANSIENT volumes from those mappings, so the lettering transaction gets one too. The
mappings are nevertheless unreachable, because the query layer gates an address filter on the
account's **current** existence before it reads them:

- an exact address first checks `pebbleAccountExists` in the primary attributes store and returns
  nothing when the account is gone (`internal/query/compile.go:1100-1110`);
- an address prefix enumerates the matching accounts from the same store, which only holds current
  accounts (`internal/query/compile.go:1069-1071`).

The same gate exists on `0b4676d97` and on the `release/v3.0` tip `a08f99bc3`. It is what made the
opening transaction disappear there too: the purged volume was the account's only attribute. So the
conclusion stands whatever EN-2036 becomes.

**Fixed later in the same PR** (head `20a5595d6`, still open, not merged): address filters on
transactions now read the mappings (`MappedAccountPrefixIterator`), and the probe returns both
transactions of a purged hold by address. But the **prefix** path now enumerates every hold ever
created, purged ones included, and materializes their transactions on every page. On real EPHEMERAL
holds, a 2k-transaction window costs 5.7 s at a 100k-payment history and 50.7 s at 1M, against
56–65 ms for `payment_ref EXISTS`. That is reported on the PR
([comment](https://github.com/formancehq/ledger/pull/2058#issuecomment-5817109700)), with options.
None of it changes this design, which never reads by address.

Ask **L5** keeps only what this design needs: the
metadata and `reference` paths stay a tested contract for purged accounts. Reaching them by address
is a Ledger matter, and an address prefix must not be extended to purged accounts (§6, and
[design doc §7.6](../technical/transaction-level-reconciliation.md#76-where-the-key-comes-from-transaction-metadata-not-the-hold-address)).

**Consequence for the recommended booking.** Every lettering transaction, on both ledgers, must carry
the **PSP payment reference** as declared, indexed transaction metadata. On the product ledger, it
should also carry the **business id** of the hold it letters. Its postings name that hold, but the
address index forgets it once the hold is purged, and a metadata field makes "which payments settled
invoice X" a query.

### 2.3 The need

- Cadence: **daily**, at a business cut-off.
- Volume: **100 to 1,000,000 items per run**.
- Output: the complete list of items and their gap. That is too large for an alert or a capture, so
  it has to be retrievable asynchronously.

Two legs:

| Leg | Question | Universe | Source of truth |
|---|---|---|---|
| **Flow** (per payment reference) | Is every payment the PSP finalised applied by the product with the same amount, and does every product application point at a payment the PSP really finalised? | The window's final and failed PSP transactions, the window's product applications, and the **still-unapplied payments carried from earlier days** | `ListTransactions` over the window's id range, filtered on the reference's presence (logs remain the immutable re-derivation path), plus the previous run's pending set |
| **Stock** (per hold, on each side) | What is still open at `S`, and for how long? PSP holds are pending payments; product holds are unpaid business objects | Open holds, bounded by construction because lettered holds purge | Live listing **rewound** to `S` with the log window `(S, now]` (§5) |
| **Continuity** (self-check) | `open(S) = open(S_prev) + opened(W) − lettered(W)`, per side and per asset | Aggregates | `open(S)` and `open(S_prev)` from the rewind; `opened(W)` and `lettered(W)` from the flow read, which on the product side must therefore also return hold openings (§5) |

The two stock books do not join to each other: an unpaid invoice has no PSP counterpart by design.
The cross-ledger signal is in the flow leg, and in particular in its carry-over, the **unapplied
payments**: payments the PSP finalised that no product transaction references yet. That set is
reconciliation's own open-items book. It is carried from day to day in the run artifacts, and it can
always be recomputed from the permanent logs.

The continuity identity is what makes the control **complete**. A window read that dropped an
event breaks the identity, so the loss is detected instead of silently shrinking the universe.

### 2.4 Why the shipped templates cannot express it

- **Aggregate only.** Every shipped template aggregates ([templates.md](../technical/templates.md)).
  A `balance_equation` over the two hold prefixes gives the **net open exposure**, which is phase 1
  below. But offsetting breaks cancel out, and settled items are not in any account set at all.
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

The measurements also located the cost:

- **Every page reopens both checkpoint databases.** The open uses a backup-tuned profile: 32 open
  files and the default 8 MiB cache (`internal/storage/dal/store_readonly.go`).
- **Two concurrent readers on the tip scan faster than one.** The fix for EN-2108 keeps a shared open
  alive while any reader holds it, and 100k × 2 scopes took 8.3 s against 26 s for one scope alone.
  So reopening, not reading, is what costs.

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
| **C** | **A cut at the business cut-off, plus a rewind.** The cut is `S` (the last log id with `date ≤ cut-off`) and `T` (the last transaction id with `inserted_at ≤ cut-off`), on each ledger. The flow comes from `ListTransactions` over `(T_prev, T]`, filtered server-side (§5). The stock comes from a live listing rewound with the logs `(S, now]` | **Adopted.** No checkpoint and no ledger change. Exact at the business cut-off on each side. Reproducible, because logs are permanent. Cost ∝ the day's payments plus the open items. |
| A | Shared, short-lived query checkpoint per (cluster, run): extract, then delete | **Fallback and oracle only.** Used to validate the rewind (§5) and possibly for a periodic or on-demand full proof. Too slow and too scarce as the steady-state path (§3). |
| B | Ledger-side consistent export (Pebble `NewSnapshot()` at a Raft-ordered trigger, streamed or written through `backup.Storage`) | **Not needed for this use case.** Still the right primitive for a frozen listing of a large *non-lettered* universe (EN-1480 generalised). Filed as an ask, not a dependency (§9). |
| D | Store the checkpoint in S3/Azure through backup | **Rejected** (§3). |
| E | Pebble primitives: EFOS, `Checkpoint(WithRestrictToSpans)`, `RemoteStorage` | **Rejected**, evidence in the [design doc §6](../technical/transaction-level-reconciliation.md#6-could-pebble-do-better). Pebble is not the bottleneck; the ledger read API is. |
| F | Paginate live without correction | **Rejected.** A 1M listing spans about 1,000 snapshots and tears under writes (measured, §5). |

## 5. Decision A — the cut is a log id, and the stock is rewound to it

**Choosing the cut.** On each ledger, `S` is the last log whose `date` (HLC, strictly monotonic)
is at or before the cut-off.

- Both ledgers are cut at the **same business time**, whatever their cluster and whenever the run
  starts. This is better than a checkpoint's cross-cluster semantics.
- `S` and its log hash go into the capture, so the cut is identified exactly and can be re-derived
  later.
- **Resolving `S`.** Per-ledger log ids are contiguous from 1, so the head is
  `GetLedgerStats.log_count` (checked on the bench). `ListLogs` rejects `reverse`
  ("options.reverse is not supported on this endpoint"), so `S` is **(the first log with
  `date > cut-off`) − 1**: one ascending page of size 1 on the per-ledger log-date index
  (`LOG_BUILTIN_INDEX_DATE`, "lldt"). That index is recommended on both ledgers. Without it, `S` is
  found by bisecting on log id, about 20 calls for 1M logs (§8.8). `T` is resolved the same way on
  the `inserted_at` index.

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

**Flow leg: `ListTransactions` filtered server-side, not `ListLogs`.**

The first draft read the flow from `ListLogs`. But logs cannot be filtered server-side on metadata
(`QueryFilter` allows only `ledger`, `log_id` and log `date` on `QUERY_TARGET_LOGS`), so a busy
product ledger would have its whole traffic read. `ListTransactions` is filterable. Measured on one
node, over **1M transactions of which 10 % are payments** ([design doc §7.2](../technical/transaction-level-reconciliation.md#72-log-and-transaction-reads)):

| Read of the same 1M-transaction window | 1 stream | 8 id ranges |
|---|---|---|
| `ListLogs`, everything | 72.5 s (13.8k/s) | 15.1 s |
| `ListTransactions`, everything | 10.6 s (94.5k/s) | 2.85 s |
| `ListTransactions`, `payment_ref EXISTS` → the 100k payments | 2.9 s | **0.8 s** |

So the flow is read with `ListTransactions`, filtered on `And(id ∈ (T_prev, T], <membership>)` and
split into parallel id ranges.

- On the PSP ledger, membership is `payment_ref EXISTS`.
- On the product ledger it is **`Or(payment_ref EXISTS, business_ref EXISTS)`**. A business hold's
  opening (an invoice issued) carries no payment reference yet, but continuity needs `opened(W)`,
  so every product transaction that touches a business hold carries its `business_ref` (§8.3).

- `T` is the transaction-id image of the cut: the last transaction with `inserted_at ≤ cut-off`.
  It is resolved with the `inserted_at` index in one page, or by bisecting on id. Per-ledger
  transaction ids are contiguous: an unfiltered `(0, 1M]` returned exactly 1M rows.
- Every returned transaction carries its postings, its metadata and its `post_commit_volumes`.
- **Membership is the key's presence, not a `kind` tag.** Any transaction that carries the payment
  reference belongs to the flow, including a manual correction.
- It costs O(payments in the window), whatever else the ledger books.

Three caveats come with this choice, and each has a counter-measure:

1. **Transaction metadata is mutable, and logs are not.** A `SavedMetadata` can target a
   transaction id. The bench retagged one payment's `kind` after the fact, and the same
   `kind = payment` re-read of the same past window returned 99,999 payments instead of 100,000.
   The adopted `payment_ref EXISTS` filter still returned 100,000, because the key itself was not
   touched; a rewrite of the key would break it in the same way. The original log was untouched.
   - Convention: **the key and state metadata are never rewritten.** A correction is a new
     transaction.
   - **The convention is monitored, not just trusted.** The rewind already reads every log in
     `(S, head]`, so any `SavedMetadata` or `DeletedMetadata` there that targets a transaction and
     touches the key or state field is visible. It is reported as its own anomaly,
     `key_metadata_mutated`, naming the transaction id and the field.
   - The run's artifact records what it read.
   - The logs remain the immutable path for re-deriving any past day exactly.
   - The structural fix is **immutable transaction labels** (ask **L8**).
2. **The `payment_ref` index becomes mandatory** (declared type plus metadata index). While it
   builds, the read returns a retryable `Unavailable` ("index is still building").
3. **No free count-based completeness check**, because a filtered range has gaps by design.
   Completeness rests on the index, whose reads are aligned to the main-store horizon (ledger
   EN-1748), and on the continuity identity (§2.3).

**Stock leg (rewind).** The steps:

1. List the open holds by prefix, live. The listing may tear.
2. When the listing ends, read the logs `(S, head]`. This window runs from the cut-off to the run,
   so it is short, and `ListLogs` is kept for it on purpose: it is **complete** (the count check
   `hi − lo` holds), immutable, and independent of any metadata convention. A hold touched by a
   transaction that forgot its key is still corrected.
3. For every hold touched in that window, discard the listed value and use its balance **just before
   its first touch after `S`**. That balance is the transaction's `post_commit_volumes` minus the
   transaction's own net posting on the hold.
4. Holds touched but absent from the listing are added back if that balance is non-zero. Holds
   created after `S` have a pre-balance of 0 and drop out.

Why this is exact:

- A hold untouched in `(S, head]` held one value throughout the listing.
- A hold touched in it is recomputed from its own log.
- It needs no baseline and no stored state, and it works for NORMAL accounts too.

**Validated** against a checkpoint taken at `S`, while 8 writers ran concurrently ([design doc §7.4](../technical/transaction-level-reconciliation.md#74-rewind-proof)):

- the raw live listing of 1M accounts was wrong on **2,233** rows;
- the rewound listing was wrong on **0** of 1,002,408;
- the correction cost 8,208 logs and 267 ms.

**The checkpoint's role shrinks to an oracle.** It serves the rewind's regression test, and it can
back an optional periodic proof: the rewound stock at `S = checkpoint.max_sequence` must equal the
checkpoint's listing.

## 6. Decision B — matching semantics

- **Key: the PSP payment reference.** Each side names the declared, indexed transaction metadata
  field that carries it, for example `payment_id` on the PSP ledger and `psp_payment_ref` on the
  product ledger. The product side also names the field that carries the **business id** of the hold
  it letters (`invoice_no`…), which gives the payment → invoice link.
  The key is **never taken from the hold address**, even on the PSP ledger where holds are named
  after the reference. Measured with purged holds made reachable (as EN-2331 would), an address
  prefix costs O(history) per page: 47.8 s for a 2k-transaction window on a 1M-payment history,
  against 48 ms for `payment_ref EXISTS`, which stays O(window)
  ([design doc §7.6](../technical/transaction-level-reconciliation.md#76-where-the-key-comes-from-transaction-metadata-not-the-hold-address)).
  The hold address keys the stock only.
- **States are parameters, not a fixed vocabulary** (owner, 2026-09-24). How external payment states
  are modelled on the PSP ledger is a per-deployment choice. The rule therefore declares, **for each
  side**, the metadata field or fields and the value sets that mean `pending`, `final` and `failed`:

  ```json
  "psp":     {"ledger": "psp",  "key": "payment_id",      "state": {"field": "event_type", "final": ["payin.succeeded"], "failed": ["payin.failed", "payin.cancelled"], "pending": ["payin.pending"]}, "holdPrefix": "fpay:stripe:payment:hold:"},
  "product": {"ledger": "main", "key": "psp_payment_ref", "businessId": "invoice_no", "state": {"field": "transition_kind", "final": ["to_final"]}, "holdPrefix": "main:hold:invoice:"}
  ```

  A transaction whose state value is in no set is ignored. The rule's validation rejects overlapping
  sets.
- **No tolerance.** Owner decision, 2026-09-24. The comparison is exact: a fee or FX difference on a
  payment is a break, never an accepted gap. Fees and FX must be booked explicitly on the side that
  bears them.
- **Refunds and chargebacks are their own 1-to-1 pairs.** Owner decision, 2026-09-24. A refund is a
  **separate PSP payment**, with its own reference, on the PSP ledger. On the product ledger it is a
  **refund hold**, lettered by a transaction carrying that reference. It flows through the same
  classes as any payment. Refunds are not modelled as a reversal of the original payment, because
  PSPs and product implementations differ too much for that to be general.
- **Cardinality.** The join is on transactions, per payment reference. One payment may be applied by
  several product transactions: split across invoices, or applied in parts. Their amounts are
  **summed per reference** before comparison. One invoice settled by several payments is a
  stock-side fact (the business hold only letters to zero once), not a flow break.
- **Flow classes**, per payment reference:

  | Class | Meaning | Severity |
  |---|---|---|
  | `matched` | PSP `final`, and product applications summing to the same amount | — |
  | `under_applied` / `over_applied` | PSP `final`, but the product applications sum to less or to more. There is no tolerance | break |
  | `unapplied_payment` | PSP `final`, no product application yet. **Pending while within `grace`**, a break after it | pending, then break |
  | `orphan_application` | A product application points at a reference the PSP never finalised (unknown, `pending` or `failed`) | **critical** |
  | `reversed_after_application` | The PSP reports `failed` on a reference **after** the product applied it. A refund or chargeback is *not* this: it has its own reference | **critical** |

- **Stock classes**, per hold and per side: `open` with its age bucket, `negative_hold` (a required
  state was skipped: "investigate id"), and `stuck` (open past the side's `maxAge`, the `stale_holds`
  signal per key). PSP holds are pending payments; product holds are unpaid business objects. The two
  books are aged, never joined to each other.
- **Grace and ageing: proposed defaults, to calibrate with the design partner** (the owner has no
  prior on them).
  - `grace` for `unapplied_payment` = **3 calendar days**.
  - Age buckets `0–1 d`, `2–7 d`, `8–30 d`, `> 30 d`.
  - `maxAge` has no default: without one, no hold is ever `stuck`.
  - All three are per-rule parameters. **Ageing** (`new` / `persisting` / `cleared`) comes from
    comparing with the previous run's artifact.
- **Arithmetic.** Exact integer minor units, colors collapsed per asset, and multi-asset through
  `asset: "*"` as in ADR-004.
- **Schedule and alerts reuse the existing model.** Owner decision, 2026-09-24.
  - The rule runs on a **daily schedule by default**. It takes the existing `periodType` (`daily`,
    `weekly` or `monthly`), and the alert identity is `(rule, fingerprint, period)` exactly as in
    [alert-period-model.md](../technical/alert-period-model.md). With `monthly`, the month's alert
    is opened by the first failing daily run and updated by the following ones.
  - **The alert carries the aggregate comparison.** Its evidence holds the net and absolute drift
    per asset, the class counts per leg, the continuity check, the top-K breaks and the link to the
    day's files. When a drift shows up, the analysis continues in the detail kept in the backup
    storage.
  - **What the alert says: a reconciliation statement**, never a bare drift
    ([design doc](../technical/transaction-level-reconciliation.md#what-the-controller-sees-a-reconciliation-statement-never-a-bare-drift)).
    - A **verdict**, evaluated in this order: `INCOMPLETE`, `BREAKS`, `RECONCILED_WITH_PENDING`,
      `RECONCILED`.
    - A **bridge** from the PSP control total to the product control total. It explains the net
      difference class by class, and its **unexplained residual must be 0**; otherwise the verdict
      is `INCOMPLETE`, an engine failure and never a green run.
    - The **gross** Σ|drift| next to the net, with an explicit *offsetting* flag.
    - The breaks in **severity order**, each with new versus persisting.
  - **What opens the alert:** any break, whether a non-zero net drift *or* at least one break row.
    An aggregate alone can net to zero over offsetting breaks (+x on one payment, −x on another), so
    the aggregate is what the alert *shows*, never the sole trigger. Unapplied payments still within
    `grace` are pending and open nothing.
  - There is never one alert per payment (the reasoning of the ADR-004 2026-09-08 amendment).

## 7. Decision C — two-phase workflow, detail kept 90 days in the backup storage

1. **Phase 1: synchronous, seconds.**
   - Resolve `S` on each ledger.
   - Take one live `AggregateVolumes` per hold prefix. This is the open exposure *now*, labelled with
     the run instant, because the call does not say which log id its snapshot saw.
   - Write an aggregate capture. The exact aggregates at `S` and the continuity check come with
     phase 2, as sums over the rewound rows. That is cheap, because the open book is small by
     construction. Ask **L7** would make phase 1 exact too.
2. **Phase 2: an asynchronous job**, idempotent per (rule, period, cut) and resumable.
   1. Flow window read and join, including the carried pending set.
   2. Stock rewind and ageing.
   3. Write the artifacts.
   4. Write the **detail capture**: counts, drifts, `S` and `T` per ledger, and the artifact URI and
      SHA-256, signed with Ed25519 (EN-1930).
   5. Update the alert.
3. **Where the files go: the backup object storage, under a recon prefix.** Owner decision,
   2026-09-24.
   - Recon writes to the same S3 or Azure destination the ledger backs up to, under
     `{bucketID}/reconciliation/{ruleId}/{YYYY-MM-DD}/{runId}/`. The files are `manifest.json`,
     `flow.ndjson.gz`, `stock.ndjson.gz`, `pending.ndjson.gz` and `breaks.ndjson.gz`.
   - The prefix **must stay outside `{bucketID}/backups/`**. The ledger's post-manifest orphan prune
     lists and deletes every unreferenced object under `{bucketID}/backups/data/` and
     `{bucketID}/backups/exports/` (`internal/infra/backup/manager.go:229-235`, prefixes at
     `segment.go:54-61`). A sibling prefix is never touched.
   - Recon brings its own credentials for that destination, and the same drivers (`s3`, `azure`).
     It also accepts `file` for local development.
   - The manifest's hash sits in the signed capture, so the signature covers the files transitively.
4. **Retention: 90 days by default, per rule.**
   - The primary mechanism is the storage's lifecycle rule on the recon prefix (S3 lifecycle, Azure
     lifecycle management). Recon's own sweep, driven by the `expiresAt` in each manifest, is the
     fallback.
   - Expiry loses nothing irrecoverable. The logs are permanent, so any past day can be recomputed
     from the ledgers with the same cut.
5. **A period points at each day's diffs.**
   - The period is the rule's `periodType`, calendar-based in the rule's timezone. There is no
     separate accounting-period model.
   - The period's alert, and a period summary built from the period's **daily manifests** rather
     than from the ledgers, list each day with:
     - its counts per class;
     - its net and absolute drift;
     - the breaks it opened and cleared;
     - the link to its files.
   - A break still open at the end of the period keeps the day it first appeared. A break that clears
     after its period has closed shows up in the next period; the closed period is never rewritten.
   - The 90-day default retention covers a monthly period plus a review margin.
6. **First run: bounded backfill.** A rule's first run has no previous day, so no carried pending
   set. Left alone, a payment the PSP finalised before the rule existed, and that the product never
   applied, would never be seen. On the PSP side its hold is already lettered, and it is in no later
   window.
   - The first run therefore reads the flow from **`backfillFrom`**, a rule parameter that defaults
     to **cut-off − `grace` − 1 day**, instead of from the previous day's cut. That seeds the pending
     set.
   - Everything older than `backfillFrom` is out of scope. The first statement says so explicitly
     ("backfilled since …"), so it cannot be misread as covering all history.
   - The stock books need no backfill. They come from the listing, so an invoice unpaid for 60 days
     is aged correctly from day one.
   - Continuity is available from the first run as well: the rewind can rebuild `open(S_prev)` for
     any past cut, from the live listing and the logs `(S_prev, head]`.
   - Re-running with an earlier `backfillFrom` is idempotent per (rule, period, cut). It only costs
     a longer window read.
7. **Reads.** The run's status comes from the capture. Breaks are paged from the artifact by the
   API, or downloaded through a pre-signed URL.

## 8. Booking conventions we recommend

The full table is in the design doc
([recommended booking design](../technical/transaction-level-reconciliation.md#recommended-booking-design-adr-005-8)).
The rules that the engine's efficiency depends on:

1. **EPHEMERAL holds, one prefix per kind.** One hold per external payment on the PSP ledger. One
   hold per **business object** on the product ledger, not one per state. The open book is then a
   prefix listing, with no index.
2. **One transaction = one event of one payment reference.** The flow is then extracted
   transaction by transaction, without splitting multi-reference transactions.
3. **Declared transaction metadata**:
   - PSP ledger: `payment_ref`, `merchant_ref` (the business id passed when the payment was
     created), `state`, `kind`;
   - product ledger: `payment_ref`, `business_ref`, `kind`. **Every** product transaction that
     touches a business hold carries its `business_ref`, including the one that opens it, so the
     flow read returns hold openings too and continuity can be computed.

   **`payment_ref` must be declared and indexed on both ledgers**, because the flow is read by
   filtering on its presence (§5). Index `merchant_ref` and `business_ref` too, for investigation:
   the address index forgets purged holds (§2.2).
   **These fields are write-once.** A correction is a new transaction, never a `SavedMetadata` on an
   existing one, because a filtered re-read would otherwise change a past day (§5, caveat 1).
   **`merchant_ref` is what turns an `unapplied_payment` into "invoice X is paid: apply it".**
4. **Strict amounts, explicit fees and FX** (decision 6). Product applications are
   `send [$asset $amount]`, never `*`. PSP fees are their own posting to `psp:{conn}:fees`.
5. **Clearing account.** A `product:clearing:{conn}` account on the product side gives an aggregate
   control total that lines up with the PSP's `:main`.
6. **`reference = {payment_ref}:{state}`** on the PSP side and `{payment_ref}:{business_ref}` on the
   product side: idempotent on re-delivery.
7. **`timestamp` = event time; the log date is the cut.**
8. **Log-date index on both ledgers.** It is optional: there is a bisection fallback.
9. **Traffic unrelated to payments costs nothing on the flow leg.** The filtered transaction read
   costs O(payments). Only the short rewind window reads every log, so a dedicated receivables
   ledger is no longer needed for performance. Metadata-only writes on holds are still best avoided:
   each one is a log to read in the rewind.

## 9. Upstream asks

| Ask | Why | Size |
|---|---|---|
| **L2** ([EN-2327](https://formance-team.atlassian.net/browse/EN-2327)): drop the per-account INFO line `scanAccount complete` on list paths (`internal/application/ctrl/store.go:189-195`) | A listing of 1M accounts writes 1M log lines (this bench: 3.86M lines, 970 MB) | XS |
| **L5** ([EN-2331](https://formance-team.atlassian.net/browse/EN-2331)): a tested contract that **a purged EPHEMERAL account's transactions stay reachable through indexed transaction metadata and `reference`**. EN-2331 also covers the Ledger-side part, which this design does not need: resolve an exact address from the account→tx mappings (the query currently checks that the account exists, `internal/query/compile.go:1069-1110`), keep the address prefix limited to current accounts, and fix EN-2036's READMEs | The flow leg finds lettered items through indexed transaction metadata (`payment_ref EXISTS`, then a join on its value), and investigations use `reference`. Nothing in this design reads by address. Not blocking | S |
| **L6** ([EN-2328](https://formance-team.atlassian.net/browse/EN-2328)): `ListLogs` throughput. On the same 1M transactions it is 5–7× slower than `ListTransactions` (13.8k/s against 94.5k/s on one stream) | Only the rewind window and exact re-derivations still read logs; the gap deserves an explanation | S–M |
| **L8** ([EN-2326](https://formance-team.atlassian.net/browse/EN-2326)): **immutable transaction labels**. Key/value pairs set when a transaction is created, never changed by `SavedMetadata` or `DeletedMetadata`. They are declared and typed like metadata, indexed as **add-only** (like `reference` or `timestamp`, with no old-value history to resolve at a pin), and filterable with equality, `EXISTS` and prefix on `ListTransactions`. Because they never change, they can also be filterable on `ListLogs`. | Removes caveat 1 of §5 by construction instead of by convention: a filtered re-read of a past window becomes as reproducible as the logs. Cheaper to index than mutable metadata. Gives the payment key an immutable, auditable home. `reference` comes close (immutable, indexed) but is single-valued, unique and exact-match only, so it cannot drive a window filter | M |
| **L7** ([EN-2329](https://formance-team.atlassian.net/browse/EN-2329)): return the snapshot horizon (the per-ledger log id the read saw) on `AggregateVolumes` and `ListAccounts` | Makes the phase-1 aggregate exact at `S` (`agg(S) = agg − Σ net(S, horizon]`) and lets the rewind skip the untouched part of the window; it is already part of EN-1480's scope (`log_sequence`) | S |

Only these five are asked, because only these serve this design.

**Findings passed on for information, not asked.** Reconciliation takes no query checkpoint in an
evaluation, so it does not ask for any of the following. The measurements stay in the design doc
([§8](../technical/transaction-level-reconciliation.md#8-ledger-findings-and-asks), F-b, F-g, F-h)
for the Ledger team to weigh against its own users:

- **Checkpoint reads are about ×20 slower than live reads.** Every page reopens both databases with
  the backup profile. Passed on as a [comment on EN-2108](https://formance-team.atlassian.net/browse/EN-2108?focusedCommentId=25936),
  which already shares that open between concurrent readers. The two remaining checkpoint uses here
  are the rewind's test oracle and ADR-003's optional proof run, and both can afford the slowdown.
- **No consistent export**, meaning no single-snapshot multi-page listing, and **checkpoints have
  no owner and no TTL**. These served options A and B only.

## 10. Decisions taken and what remains open

**Settled by the owner on 2026-09-24:**

| # | Question | Decision |
|---|---|---|
| 1 | The shared key | The **PSP payment reference**. The payment is seen on the PSP ledger first, and the product ledger later books a transaction carrying the same reference, which letters a business hold (an invoice…). Authorization/capture, where both sides share the authorization number, is a special case (§2.1, §6). |
| 2 | The state vocabulary | **Parameterised per side** in the rule (§6), because it depends on how external payment states are modelled on the PSP ledger. |
| 3 | Grace and ageing | No prior: the proposed defaults are 3 days of grace and buckets of 0–1, 2–7, 8–30 and > 30 days, all per rule and to be calibrated (§6). |
| 4 | Results storage | The **backup object storage**, under a recon prefix outside `backups/`. **90 days** of retention by default. The monthly reconciliation points at each day's diffs (§7). |
| 5 | Scope | Transaction-level reconciliation is **in the reconciliation project's scope**. The PRD is amended accordingly. |
| 6 | Tolerance per payment (fees, FX) | **None.** The comparison is exact, and any difference is a break (§6). |
| 7 | Refunds and chargebacks | **Each is its own 1-to-1 pair**: a refund hold on the product ledger and a payment with its own reference on the PSP ledger. They are never a reversal of the original payment (§6). |
| 8 | Schedule, period and alert | A **daily schedule** by default and the existing `periodType` (`daily`, `weekly`, `monthly`); no accounting-period model. The alert carries the aggregate comparison, and the per-payment detail sits in the backup storage (§6, §7). |
| 9 | First run | A bounded **backfill** from `backfillFrom` (default: cut-off − grace − 1 day), announced explicitly in the first statement (§7). |
| 10 | Read path of the flow | **`ListTransactions` filtered on `payment_ref EXISTS`**, over parallel id ranges: 5–7× faster than logs and O(payments). Logs stay for the rewind window and for exact re-derivation (§5). |

**Nothing blocks the tickets.** An accounting-period model (fiscal calendars) can come later as a
new `periodType` without changing this design.

## 11. Consequences

- **Recon gains object storage** (the backup destination, under its own prefix) as its first durable
  dependency outside the ledger. It stays stateless in process. The artifacts are outputs, plus the
  previous run's pending set and ageing. Every one of them can be recomputed from the permanent logs.
- **The result store is shared, not ADR-005-specific.** Its first other consumer is `stale_holds`,
  which should keep its flagged holds as an artifact instead of only a query to re-run
  ([stale-holds.md §9](../technical/stale-holds.md#9-revisit-after-adr-005--keep-the-flagged-holds-as-a-result-artifact-),
  [EN-2324](https://formance-team.atlassian.net/browse/EN-2324)). The layout, the manifest, the hash
  in the capture and the retention are generic by construction.
- **ADR-003 stands.** Evaluations never take query checkpoints; one is used only as a test oracle
  and for an optional proof run.
- **A new template kind, with its own async execution path** and resumable jobs. The scheduler's
  10 s drain grace does not apply to it ([scheduler.md](../technical/scheduler.md)).
- **Recon starts reading `ListTransactions` in bulk, and `ListLogs`.** For the logs, it has to handle
  every payload that moves a balance (created and reverted transactions), and `SavedMetadata` and
  `DeletedMetadata` on transactions for the `key_metadata_mutated` monitoring. It ignores the rest.
