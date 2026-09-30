# ADR-005 lettering reconciliation: feature inventory

Every feature of the ADR-005 design, with its interest (necessary or optional), to decide which to
cut from V1. It is a working list: nothing here is decided, and a cut is recorded in the ADR when
the owner takes it.

Sources, read in full on `feat/reconciliation-ledger-v3` @ `3387d699`. The docs were de-duplicated
afterwards (`6f5c7f76`, `1be11650`, `50f3f9a8`) with no feature changed; the section references
below still hold.

- ADR-005, decisions 1–26 and asks L2–L10: `docs/prd/adr-005-transaction-level-reconciliation.md`
- the design doc: `docs/technical/transaction-level-reconciliation.md`
- the results reference: `docs/technical/transaction-level-results.md`
- the workflows doc, §9: `docs/technical/workflows.md`
- the DuckDB tool doc: `docs/technical/lettering-duckdb.md`

## Decisions taken

| Feature | Decision | Recorded in |
|---|---|---|
| B12 + G8, incremental watch and slice retention (and the `--lettering-watch-interval` flag) | Deferred after V1, and the full read with it (B11, below) | ADR-005 decision 25 |
| G5 + G3, replay from the nearest stored stock and monthly anchors (`anchorRetention`, the anchor tag, per-file `expiresAt`) | Removed from V1: a replay rewinds the live listing from head, newest first; beyond the retention its carried items are rebuilt from a backfill window | ADR-005 decisions 4 and 14 |
| I5, `period.json` | Removed from V1: the period's alert lists each day, and any other period view is a query over the daily manifests | ADR-005 §7 item 5 |
| D10 + E17 + D11, re-seed, `diagnostic.json`, `incomplete.kind` | Simplified: a stuck chain restarts as a first run, backfilled from the oldest open item of the last complete run; `incomplete.detail` names the first 20 items at fault; no `kind` field | ADR-005 decision 26 |
| D9, acceptance of breaks one by one | Removed from V1: the alert opens on any open break; the controller acknowledges or accepts the alert itself (existing alert model), and a known break is fixed by booking it | ADR-005 §6 |
| B14, phase-1 synchronous aggregate capture | Removed from V1: the run is one asynchronous job that resolves the cut, reads, joins, writes the files and one signed capture; the tick only enqueues it | ADR-005 §7 items 1 and 2 |
| E16, file parts | Removed from V1: one gzip per data file, whatever its size (~4 s of one thread for the 75–142 MB flow file of a 1M day); readers follow `files` or glob `flow*`, so parts can come back without breaking them | RD §8; DD §7.14 |
| B13, purge consistency check (`purge_check`) | Removed from V1: the rewind adds a purged hold back from its first touch anyway, and an untouched hold missing from the listing breaks the books' continuity; no `purge_check` reason, no `purged_accounts` read, and L10 no longer needs a filter on it | DD §4; ADR §9 (L10) |
| B16, resumable job | Removed from V1: no progress is recorded; writes are ordered (data files, capture, manifest last, alert), a run exists once its manifest is written, and a run that did not finish is started again from the beginning with a new `runId` | ADR-005 §7 item 2 |
| C4, `firstSide` on flow rows | Removed from V1: analytics only; `firstSeen` and the row's `psp[]` and `product[]` events give which side came first | ADR-005 decision 19 |
| D8, `previousClass` on break rows | Removed from V1: a class change stays the same break (same `breakId`, `persisting`), and the previous run's breaks file, joined on `breakId`, gives the old class; `check-chain` still checks that an open break is never lost | ADR-005 decision 21; RD §7 |
| E10, `flowGross` + `offsetting` flag | Simplified: the `offsetting` flag is removed; `flowGross` stays, since the alert's headline shows it first | ADR-005 decision 21; RD §5 |
| E15, the manifest's `execution` and `timingsMs` blocks | Removed from V1: K, the per-step durations and the read counts are exported as OTel metrics and engine logs; `startedAt` and `finishedAt` stay | ADR-005 §7 item 8 |
| F3, `logSha256` in `cuts` and in the capture | Removed from V1: `S` and `T` per ledger suffice to replay, the logs are immutable, and the hash is stable for one protocol version only | ADR-005 §5 and §7 item 7 |
| G2, recon's own `expiresAt` sweep, and the rule's `retention` | Removed from V1: one storage lifecycle rule on `{bucketID}/reconciliation/`, required at installation, deletes the files; the retention is the deployment setting `--lettering-retention` (default 90 d), and the manifest's `expiresAt` is for information only | ADR-005 §7 items 4 and 8, decision 4 |
| B18, G6, F10, doc-only options | Removed from V1: follower reads, the exact replay through the logs `(S_prev, S]` and the periodic proof against a checkpoint are no longer mentioned as options; ADR-003 keeps the proof run, and the oracle test (R10, EN-2334) is the only checkpoint use | DD §3, §4; ADR §4, §5, §11 |
| G9, a generic result store | Deferred after V1: the store is built for lettering; `stale_holds` (EN-2324) generalises it when it starts | ADR §11 |
| F4 + F5, the stored-file check and the byte-identical replay | Kept: F4 is what makes the signed capture useful to the next run, and F5 follows from the determinism the golden tests need | RD §2, §4, §8 |
| B11, the metadata watch and `anomalies.key_metadata_mutated` | Deferred after V1, until L8: a run reads no range of logs, and its reads take ~20 s instead of ~2.5 min at 1M payments. A change on a transaction of a day already read moves no balance, and one before the run is read at its latest value. The write-once convention (A16) and replay condition 4 (F5) are no longer monitored; `reconciled_with_warnings` means unclassified transactions only | ADR-005 decision 25 and §5 caveat 1; DD §3; RD §4, §8 |
| E8 + E9 + C10 + D5, the reconciliation statement and its self-checks | Kept: the statement is the deliverable a controller reads (a statement, never a bare drift), and the residual and the open-items identity are sums over rows already in memory that prove the join and the carry-over | ADR §6; RD §5 |

Abbreviations: **ADR** = ADR-005, **D*n*** = decision *n* of ADR §10, **DD** = design doc, **RD** = results doc.

The strict test for **NEC** (necessary): if the feature goes, the V1 result is wrong, is incomplete,
or no longer tells which payments are lettered, which are breaks, and that the day is complete.
Everything else is **OPT** (optional).

Cost is implementation plus doc complexity: **S** small, **M** medium, **L** large.

---

## 1. Inputs, booking and rule contract

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| A1 | Join key = PSP payment reference in indexed tx metadata (`psp.key`, `product.key`) | ADR §6, D1 | Each side names the metadata field that carries the PSP ref; the join is on transactions | Hold ids differ across ledgers; purged holds are reachable only through tx metadata | NEC | S | No join at all |
| A2 | Mandatory key and `businessId` indexes, rejected at validation, `INDEX_BUILDING` wait | ADR §5 caveat 2, §8 rule 3 | The rule is refused without the indexes; the run waits while they build | The flow read filters on the key's presence | NEC | S | Flow read impossible or unbounded |
| A3 | Per-side state sets (`pending`/`final`/`failed`), overlap rejected | ADR §6, D2 | Each side maps its state values to 3 meanings | PSP state vocabulary varies per deployment | NEC | S | Hard-coded vocabulary, breaks on the first connector |
| A4 | `holds[]`: `prefix` + `openSign` per hold kind (mixed signs, overlap rejected) | ADR §6, D11 | Declares every hold prefix and the sign of an open hold under it | The sign cannot be inferred; the stock and application amounts need it | NEC | S | Stock and `wrong_sign` are ambiguous |
| A5 | `businessId` per product hold kind (`business_ref` on every hold tx, the opening included) | ADR §6, §8 rule 3, D18 | The product side names the business-id field per hold kind | Continuity needs hold openings in the flow read; gives the payment → invoice link | NEC | S | Continuity cannot be computed (no `opened(W)`) |
| A6 | `psp.paymentAccount`: PSP amount = net posting on an account pattern (`*` = one segment) | ADR §6, D17 | The PSP amount is read on the account a final credits, not on the hold | The hold shows 0 or the wrong amount when no pending came first, or when the amounts differ | NEC | S | Wrong PSP amounts, so false or missed breaks |
| A7 | Application amount = net posting on the side's hold prefixes, in the settling direction | ADR §6 | Revenue recognition in the same batch counts for 0 | Defines "applied" without a `kind` tag | NEC | S | No definition of an application |
| A8 | `psp.merchantRef` pairing (`merchantRef`, `pairedHold`, `pairedRef`, "apply invoice X") | ADR §6, §8 rule 3; DD §2 | An unapplied payment is paired with the open hold its merchant ref names | Turns "money arrived, PAY-42" into an action item | OPT | S/M | Saves a pairing pass, 3 fields and triage text. Loses the most actionable hint the design has (DD: "the most useful single field"). `formancepayments` does not carry it today |
| A9 | `psp.movementKeys` (payout/fee keys added to the PSP flow membership) | ADR §6, D23, §8 rule 10 | One extra `EXISTS` term per declared field; those txs feed the payment-account book only | Lets the book close at 0 on movements with a key of their own, on both sides: payouts and fees, and with `formancepayments` its conversions and order fills | OPT | S | See E13. Saves a contract field and about +31 % on the PSP flow read, but `formancepayments` conversions then leave a residual (DD §2) |
| A10 | Refunds and chargebacks as their own 1-to-1 pairs | ADR §6, D7 | A refund is its own reference and a refund hold, never a reversal | Keeps one generic model | NEC | S | Nothing to save: already the simplest option |
| A11 | Exact comparison, no tolerance | ADR §6, D6 | Any fee or FX difference is a break | Fees must be booked explicitly | NEC | S | Nothing to save: already the simplest option |
| A12 | Per-asset arithmetic: exact minor units, colors collapsed, `asset: "*"` fan-out | ADR §6 | Every figure and row is keyed by asset | Multi-currency correctness | NEC | S | Mixed-asset sums are wrong |
| A13 | Booking conventions, connector checklist (12 rows), customer booking guide (R11, EN-2335) | ADR §8; DD §2 | Tells the implementer how to book both ledgers so the rule can read them | The engine's efficiency and completeness rest on the booking | NEC (trimmed) | M (doc) | The rule cannot be onboarded. The advice-only parts can go: the clearing account (rule 5), `kind`, the `reference` format and `merchant_ref` indexing; the engine uses none of them |
| A14 | Payment-to-apply (suspense) hold option: `holds[].role`, `state.received` | DD §2 "When application is deferred" | An alternative booking for manual lettering, with the rule additions it would need | B2B unapplied cash | OPT (already outside V1) | S (doc) | Saves a DD section. Nothing lost in V1: a longer `product.grace` covers the case |
| A15 | "Movement states" (option B of the open Connectivity question) | ADR §10 open; DD §2 | Proposed: a rule set of states whose keyed txs feed the book, not matching nor `unclassified` | `formancepayments` credits the payment account from payouts, transfers, reversed refunds… | OPT (proposed, not decided) | M | Not adding it keeps the contract smaller. Option A (connector keys) or C (warning) avoid it |
| A16 | Write-once convention on key, state, business-id and merchant-ref metadata | ADR §5 caveat 1, §8 rule 3 | A correction is a new transaction, never a `SavedMetadata`. Not monitored in V1 (B11); L8 would enforce it | Transaction metadata is mutable; a filtered re-read of a past window could change | NEC (convention) | S (doc) | Past days are no longer reproducible |

## 2. Run and cut

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| B1 | Cut = log id `S` + tx id `T` per ledger at the business cut-off (insertion date, not `timestamp`) | ADR §5 decision A, §4 option C | Converts the cut-off into id ranges `(T_prev, T]` | A day is frozen once cut; no checkpoint; both ledgers cut at the same business time | NEC | M | No deterministic window |
| B2 | Cut resolved on the log-date and `inserted_at` indexes, both mandatory; bisection dropped | ADR §5, D12; DD §3 | One page per ledger: the first entry after the cut-off, minus 1 | One read, one code path | NEC | S | Needs bisection instead (a 2nd path) |
| B3 | Bounded date filter, widened while empty (δ) | D24; DD §7.12 | `cut-off < date ≤ cut-off + δ` instead of an open filter | The ledger materializes a date range before paging it | OPT | S | Nothing for the daily run (ms). An old replay or a long backfill costs ~85 ns per entry since the cut-off (~30 s a year later). Needed only if old replays stay (G4) |
| B4 | Flow read: `ListTransactions` `And(membership, id ∈ (T_prev, T])`, membership first, `reverse = true` | ADR §5, D10; DD §3, §7.11 | Reads only the keyed txs of the day | O(payments), whatever the other traffic | NEC | M | O(all traffic) through the logs, 5–7× slower |
| B5 | Product membership `Or(payment_ref, business_ref per kind)` | ADR §5; DD §7.11 | The product flow also returns hold openings | Continuity needs `opened(W)` | NEC | S | Continuity breaks (see D4) |
| B6 | Parallel id ranges K (`--lettering-read-ranges`, default 8), merged in id order | ADR §7.8, D13; DD §7.7 | Every window is split into K ranges read concurrently | About ×4 on reads | OPT (keep) | M | One stream: a run's reads ~4× slower (over a minute instead of ~20 s at 1M payments). Correctness unchanged |
| B7 | Process-wide reader cap (`--lettering-max-concurrent-reads`, default 16) | ADR §7.8, D13; DD §7.15 | Readers wait for a slot across every run | Several rules must not multiply the load on one ledger | OPT | S | Saves a semaphore. Many concurrent rules could slow ledger writes |
| B8 | Stock = live `ListAccounts` of open holds, rewound with the unfiltered txs `(T, head_tx]` | ADR §5, D22; DD §4 | First-touch fold of `post_commit_volumes`; purged holds added back, holds created later dropped | Exact stock at the cut with no checkpoint (0 of 1M rows wrong) | NEC | M | The torn live listing (2,233 wrong rows in 1M); continuity is meaningless |
| B9 | Count check `hi − lo` on unfiltered windows → `incomplete` (`short_range`) | ADR §5; DD §3 | An unfiltered window must return exactly `hi − lo` rows | Detects a lagging replica or a failed read | NEC | S | A short read shrinks the window silently |
| B10 | Key lookups by reference (unknown applied refs on the PSP side; the history of refs found final; failed refs of the window on the product side), grouped `Or` of 100 | ADR §6, D16, D18; DD §5, §7.13 | References missing from the window and from the carried items are read by key, up to `T` | Otherwise a 2nd application reads as an orphan, and a payment matched earlier then failed today is missed | NEC | M | False P1 orphans, and a missed `reversed_after_application`. The grouping itself is optional (1.3 ms per lookup one by one) |
| B11 | Metadata watch, full read at run time (`key_metadata_mutated`) | ADR §5 caveat 1, D25; DD §3 | Reads every log since the previous run for metadata changes on txs | Monitors the write-once convention | **Deferred after V1 (until L8)** | M | Saves the only range of logs a run read, **~95 % of the run** (134–158 s at 1M payments): the reads now take ~20 s. Loses the `key_metadata_mutated` warning and the check behind replay condition 4 (F5). A change on a day already read moves no balance, and one before the run is read at its latest value. L8 (immutable labels) removes the need by construction |
| B12 | Incremental watch job with slices (the default, `--lettering-watch-interval=1h`) | D25; DD §3 | A job per watched ledger writes sealed slices; the run assembles, verifies and re-reads missing ranges | Takes the watch off the run's critical path | **Deferred after V1** | **L** | Saves a job type, slice and seal files, one chain per ledger in *another* ledger's bucket, the manifest `watch` block (`slices`, `reread` with 5 reasons), slice tagging and the 7-day sweep. The slices are not measured yet. Depends on B11, deferred too: with no watch, the reads take ~20 s anyway |
| B13 | Purge consistency check from `purged_accounts` (`purge_check`) | ADR §5; DD §4 | A hold open at `S`, touched, missing from the listing, must be named in some `purged_accounts` | Detects a listing that missed a live account | **Removed from V1** | S/M | DD: "the rewind is exact without it". Saves a reason and a batch-boundary subtlety. It read B11's logs |
| B14 | Phase 1: synchronous aggregate capture (`AggregateVolumes` per prefix, signed by `openSign`, labelled with the run instant) | ADR §7.1; DD §3 | A capture of the live exposure, in seconds, before the detail | Early figure | **Removed from V1** | M | Saves a 2nd capture kind, a code path and one of L7's two reasons (the rewind skip stays). Loses an inexact "now" figure available ~20 s earlier. Phase 2 gives the exact aggregates anyway |
| B15 | The run's async job, idempotent per (rule, period, cut), own execution path, no 10 s drain grace | ADR §7.2, §11 | The per-key computation runs off the scheduler tick | ~20 s of reads and 1M rows at 1M payments, more on a backfill | NEC | M | Cannot fit in the scheduler's synchronous path |
| B16 | Resumable phase-2 job | ADR §7.2 | A run resumes after a crash | Avoid redoing work | **Removed from V1** | M | A crashed run restarts from scratch (~20 s of reads at 1M payments): no loss |
| B17 | First-run bounded backfill (`backfillFrom`, default cut-off − max(grace) − 1 d; product window `psp.grace` earlier; "backfilled since …"; `open(S_prev)` rebuilt by the rewind) | ADR §7.6, D9, D19 | The first run reads a longer window to seed the carried items | Otherwise a payment finalised before the rule and never applied is never seen | NEC | M | Old unapplied payments are invisible forever. The product-side offset is a refinement: without it, the first day shows false `unapplied_payment` rows |
| B18 | Follower reads (`x-consistency: stale`), enabled by the count check | DD §3 "Parallel reads" | Could offload the leader | Load | **Removed from V1** | S | Drop the mention: nothing lost |

## 3. Matching and classification

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| C1 | Join per reference, applications summed per reference (split, partial) | ADR §6 "Cardinality" | One payment may be applied by several txs | Split payments are common | NEC | S | False under- or over-applications |
| C2 | 9 flow classes (`matched`, `under_applied`, `over_applied`, `unapplied_payment`, `in_progress`, `failed`, `applied_before_final`, `orphan_application`, `reversed_after_application`), read on net amounts | ADR §6, D18, D21; RD §6 | Classifies every reference | The core output | NEC | M | No result. Minor merges are possible (`reversed_after_application` into `orphan_application`, both P1; `in_progress`/`failed` rows kept only "so every ref has a row"), for little saving |
| C3 | Per-side grace (`product.grace` 1 d, `psp.grace` 7 d), `pending` outcome, `firstSeen`, `breakOn`, promotion to a break | ADR §6, D16, D20 | Lets each side lag the other before it is a break | Cross-midnight lag; SEPA/ACH apply at pending | NEC | S/M | Every cross-cut lag is a break. One shared grace would be simpler, but loses the 1 d vs 7 d asymmetry |
| C4 | `firstSide` on flow rows | ADR §6, D16, D19 | Which side came first (PSP terminal state or first application) | Analytics only; "changes no priority and no bridge line" | **Removed from V1** | S | Nothing functional lost: `firstSeen` and the row's events give it |
| C5 | Stock classes `open` + `wrong_sign` (P4) | ADR §6, D21 | Open hold, or balance of the wrong sign | `wrong_sign` is the only signal for an invoice paid twice by two separately matched payments | NEC | S | A double payment of one invoice goes unseen (both flows are `matched`) |
| C6 | `stuck` via `maxAge` (no default, per side) | ADR §6 | Open past `maxAge` → P4 break | Per-key `stale_holds` signal | OPT | S | Overlaps the `stale_holds` template. Without a `maxAge` it never fires anyway |
| C7 | Ageing: `openedAt`, `ageDays`, `bucket`, `buckets` param (0–1, 2–7, 8–30, > 30 d) | ADR §6; RD §6 stock | Age of every open hold and bucket counts per book | Credit-management view | OPT | S | Saves the bucket counts in `books` and a rule param. `ageDays` is still needed by C6 and triage. **Gap:** the source of `openedAt` for a hold opened before the first run, or re-listed after metadata purge, is not specified |
| C8 | `cleared` stock rows (`previousBalance`, `clearedAt`, `clearedBy`) | ADR §6; RD §6 | A hold open at the previous cut and lettered since is listed once | Shows what the day lettered; resolves stock breaks | OPT | S | The stock file shows only open holds. `letteredOther` is still visible in `books`. Depends on D6 |
| C9 | Unclassified txs: counted per side, state and asset, warning, never dropped | ADR §6; RD §5 | A tx whose state is in no set takes no part in matching but is reported | Catches connector mappings that break the conventions | NEC | S | Silent loss of money movements |
| C10 | `letteredOther` in the books | D21; RD §5 | Letterings by txs outside matching (credit notes, write-offs, unclassified) | Makes the bridge's B equal the applications; shows manual letterings | OPT, **kept** | S | Needed only by E8 (the residual). Manual write-offs become invisible inside `lettered` |
| C11 | `outcome` on every row (`ok`/`pending`/`break`/`warning`) | RD §3 | One field answers "must someone act?" | Readers filter on it | NEC | S | Readers re-derive it from the class plus the grace |
| C12 | `priority` 1–4 per class | ADR §6; RD §6 | A fixed class → urgency mapping | Triage order | OPT | S | Low saving; the order falls back to the class |
| C13 | Verdict: `incomplete` > `breaks` > `reconciled_with_warnings` > `reconciled_with_pending` > `reconciled` | ADR §6; RD §4 | One ordered status per run | "Is the day reconciled?" | NEC | S | No status. It could shrink to 3 values plus flags, a small saving |

## 4. Carry-over and chain

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| D1 | Carried items: every reference with drift ≠ 0 goes to the next run | ADR §2.3, D15 | Reconciliation's own open-items book, day to day | Unapplied payments and pending applications span days | NEC | S/M | Every cross-day item is lost or misclassified |
| D2 | Separate `carried.ndjson.gz` (instead of filtering the flow file) | DD §5; RD §6 | A small file with the carried rows, without `impact` | The next run reads ~5 MB, not the 75–140 MB flow | OPT | S | The next run filters the flow file (a few seconds) |
| D3 | Chain: `previousRun` (runId, day, `manifestSha256`); a window over missed or incomplete days ("window since …") | RD §2 | Each run names its predecessor and starts at its cut | Continuity and carry across gaps | NEC | S | No way to find the carried items and `openPrev` |
| D4 | Books continuity per side, prefix and asset: `open = openPrev + opened − lettered`, `openPrev` from the stored stock | ADR §2.3; RD §5 | Ties the window's hold movements to the rewound stock | The only completeness proof of the filtered flow read | NEC | M | A dropped event silently shrinks the universe |
| D5 | Open-items (suspense) identity: `open = openPrev + net + fromLookups` → `incomplete` (`continuity`) | D21; RD §5 | Ties carried(S_prev) + the window's net to carried(S) | Detects a carried item lost or counted twice | OPT, **kept** | S | An engine self-check on the engine's own file. `suspense.open` (the sum of carried drift) can stay as a figure. Needs E8's `impact` |
| D6 | Lifecycle: `new`/`persisting`/`resolved` (breaks), `new`/`persisting`/`cleared` (holds) | ADR §6; RD §7 | Compares with the previous run's artifacts | "What changed since yesterday" | OPT | M | Saves the diff against the previous breaks and stock files, the `resolved` rows and part of `check-chain`. Loses new-vs-persisting in triage and the alert |
| D7 | Stable `breakId` (hash of rule, leg, key, asset; not the class) | ADR §6, D16 | A break keeps its id when its class changes | Comments, lifecycle | OPT | S | Cheap. Required by D6; with D8 gone, it is also how a reader finds a break's earlier class |
| D8 | `previousClass` on the day the class changes | ADR §6; RD §6 | Records the old class | Audit trail | **Removed from V1** | S | Nothing functional lost: the previous run's breaks file, joined on `breakId` (D7), has the old class |
| D9 | Acceptance (`acceptedOn`; lapses when the class or amount changes; excluded from the alert trigger) | ADR §6, D21; RD §4, §7 | A known break stays in the files but stops opening the alert | Systematic known gaps (e.g. an unbooked fee) until they are booked | **Removed from V1** | M | Saves an acceptance store and API, the lapse rules and `counts.breaks.accepted`. Loses the means to silence known breaks: the alert stays open until booked. Depends on D7 |
| D10 | Re-seed run (`reseed`: stock from head, carried rebuilt by key lookups on both ledgers, `reseed.adjustment`) | D26; RD §2 | Restarts the chain after a structural `incomplete` is fixed | A structural cause repeats every day and the window grows | **Replaced by a restart as a first run** | M/L | Replace it with "restart the rule like a first run" (B17, new `backfillFrom`). Loses nothing: the backfill starts at the oldest open item, except after a `stored_file_mismatch`, when the operator gives `backfillFrom` |
| D11 | `incomplete.kind` transient/structural | D26; RD §4 | Classes the 6 reasons in 2 kinds | Tells the operator whether to wait or act | **Removed from V1** | S | The alert text maps the reason directly. Needed by D10 and E17 |

## 5. Outputs and files

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| E1 | Storage in the product ledger's backup destination, under a sibling prefix outside `backups/`; own credentials; `s3`/`azure`/`file` drivers | ADR §7.3, D4 | Recon's first durable dependency | 1M-row detail cannot sit in a capture | NEC | M | No per-key detail |
| E2 | Layout `rule=/day=/run=`; sortable `runId`; current run = latest complete run; a retry writes a new run | RD §2, D21 | Hive-style paths, one current run per day | Query engines read the path as columns; retries are safe | NEC | S | Ambiguous "which run counts" |
| E3 | `manifest.json` core: `schemaVersion`, `engine`, rule snapshot + sha256, `cuts`, `verdict`, `counts`, `files` (rows, sha256) | RD §6 | The run's summary and index | Entry point for every reader | NEC | M | No index of the run |
| E4 | `flow.ndjson.gz`, self-contained rows (`psp[]`, `product[]` tx lists, `holdAmount` on pending/failed) | RD §6 | One row per reference and asset | "Which payments are lettered" | NEC | M | No per-payment answer |
| E5 | `stock.ndjson.gz` | RD §6 | One row per open hold (plus cleared) | Open book, `openPrev` for continuity | NEC | S | Continuity loses its `openPrev` |
| E6 | `breaks.ndjson.gz`, self-contained, resolved rows included | RD §6 | Duplicates the break rows of flow/stock plus the book breaks | A reader never joins files | OPT | S/M | Breaks are `outcome = 'break'` in flow + stock + `paymentAccounts`. Loses the `resolved` list (see D6) and one-file convenience |
| E7 | `unclassified.ndjson.gz` | RD §6 | One row per unclassified tx | Fixing the mapping | OPT | S | `statement.unclassified` in the manifest already gives side, state, amount and count. Low saving |
| E8 | Statement bridge: A (payment account) − B (from books) = net, lines by class/outcome/`earlierDay`, `residual` = 0 else `incomplete`, `impact` field | ADR §6; RD §5 | The classic *état de rapprochement* for the window | Explains the net; the residual ties the join to the books | OPT, **kept** | M | The lines are a `GROUP BY` over the flow file (DuckDB `bridge` query). Loses the residual self-check (the join attributed every lettering), `impact`, and C10/D5/E9 with it. Continuity (D4) still guards completeness |
| E9 | `carriedOutside` lines | RD §5 | Carried items with no movement today, per class | Complete statement | OPT, **kept** | S | Derivable from the carried file |
| E10 | `flowGross` + `offsetting` flag | ADR §6; RD §5 | Σ\|drift\| of the open flow breaks, and whether both signs exist | A net of 0 can hide breaks | **Simplified** (OPT): `offsetting` removed, `flowGross` kept | S | The alert already opens on breaks, never on the net: nothing lost. `flowGross` stays, since the alert's headline shows it first |
| E11 | `books` block (`openPrev`, `opened`, `lettered`, `letteredOther`, `open`, `count`, `buckets`, `continuityOk`) | RD §5, §6 | One entry per side, prefix and asset | Carries continuity | NEC | S | D4 has no carrier. `buckets` goes with C7, `letteredOther` with C10 |
| E12 | Payment-account **credit** book → P1 `unkeyed_payment_movement` on leg `book` (`paymentAccounts` block) | D23; ADR §8 rule 10; DD §7.9; RD §5 | `input(S) − input(S_prev)` must equal the flow's credits | The only check that sees a final with no pending and no key (100 silent finals left every other check green) | NEC | M | A silent hole in completeness. With `formancepayments`, blocked by conversions and order fills, which need A9 (ADR §10 open question, DD §2) |
| E13 | Payment-account **debit** book (`output`, `flowDebits`, `debitResidual`) + `psp.movementKeys` | D23; DD §7.9 | Debits must be keyed payouts, fees, refunds | Unkeyed payouts and fees are a residual | OPT | M | Payouts and fees are outside payment lettering. Saves the debit direction; A9 stays needed for `formancepayments` conversions, and the Connectivity question is unchanged (DD §2) |
| E14 | Triage in the manifest: top-K open breaks (NEC for the alert) + top-K `pending` and `resolved` lists | ADR §7.3, D21; RD §5 | The manifest renders the statement alone | The alert and a dashboard need no other file | NEC (breaks) / OPT (pending, resolved) | S | The pending and resolved lists come from the files. **Gap:** where `topK` is configured is not specified |
| E15 | `execution` + `timingsMs` blocks (`readRanges`, `stockFrom`, `rewindTxs`, `lookups`, `watchLogs`) | ADR §7.8; RD §6 | Run telemetry in the manifest | Comparable run durations | **Removed from V1** | S | Moved to OTel metrics and engine logs. No reader loss |
| E16 | File parts beyond 250,000 rows (`flow-00000…`, `part` in `files`) | ADR §7.3; RD §8; DD §7.14 | Splits a data file into parts | Parallel compression and upload | **Removed from V1** | S/M | One gzip of 75–142 MB: ~5 s on one thread; DuckDB reads it as fast. Saves the part logic in the writer, reader, checks and API |
| E17 | `diagnostic.json` for structural `incomplete` (≤ 1,000 items per reason) | D26; RD §6 | Lists the books, holds, applications or files at fault | Debug a run that writes no data file | **Removed from V1** | M | Saves a file format with 4 item shapes. The operator debugs from logs or a debug run. Depends on D11 |
| E18 | `schemaVersion` `lettering/1`, a JSON Schema per file, compatibility rules | ADR §7.3; RD §8 | Versioned, documented format | The customer reads the files directly | NEC | S | The format cannot evolve safely |
| E19 | Every data file written on every complete run, even empty | RD §8 | No missing file on a quiet day | Globs never break | NEC | S | Readers special-case missing files |
| E20 | Result API: lists a run's files with pre-signed URLs; run status from the capture | ADR §7.9 | Customers read without bucket access | Access control | NEC | M | Customers need raw bucket credentials |
| E21 | API paging of breaks from the artifact + UI | ADR §7.3, §7.9 | Recon's API and UI read the files | In-product triage | OPT | M | Customers use the files and the alert only |
| E22 | Customer query examples (RD §9) + `tools/lettering-duckdb` (check, check-chain, 11 queries, Python reference engine) | RD §9; lettering-duckdb.md | Internal validation pack, outside recon CI | An independent reading of the format for QA and support | OPT (already built) | M (maintenance) | Saves updating ~50 SQL rules, the generator and expected CSVs on every format change. Loses independent validation. Worth freezing, not growing |

## 6. Integrity and guarantees

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| F1 | Detail capture on `_recon`, Ed25519-signed (counts, drifts, `S`/`T` per ledger, artifact URI + manifest SHA-256) | ADR §7.2.4 (EN-1930) | Reuses the signed capture | The run's status and anchor | NEC | S | No status record (reuses existing infra) |
| F2 | File SHA-256 in the manifest; manifest hash in the capture (transitive signature) | ADR §7.3; DD §5 | Tamper evidence over every file | Audit | OPT (keep: cheap) | S | Loses tamper evidence. Underpins F4, F5 and `check`'s `file_sha256` |
| F3 | `logSha256` of the log at `S` in `cuts` | ADR §5; RD §6 | Hash of the protobuf `Log` at the cut | Re-identify the cut exactly | **Removed from V1** | S | `S` and `T` suffice to replay; logs are immutable. The hash is stable for one protocol version only |
| F4 | `stored_file_mismatch`: stored stock and carried files checked against the signed capture before use | RD §2, §4 | Refuses a tampered or corrupted previous file | Chain integrity | OPT, **kept** | S/M | Saves a verification step and an `incomplete` reason. A corrupted file would propagate, but the next continuity check would likely fail anyway |
| F5 | Byte-identical data files for the same cut, rule, engine and previous run (fixed key and row order, gzip level 6, no name, no timestamp) | ADR §7.3; RD §8 | A replay proves itself by its SHA-256 | Audit reproducibility | OPT, **kept** | M | Deterministic row order is cheap and still useful for tests. The "proves itself" guarantee and its 4 conditions can go. Condition 4 is no longer watched (B11): a replay is compared with the original by the SHA-256s in the manifests' `files` |
| F6 | `missing_index` → `incomplete`, never a silent fallback | DD §3 | An index missing at run time is an engine error | No wrong window | NEC | S | Silent wrong result |
| F7 | `incomplete` = no conclusion: manifest only, engine-error alert, not a chain link | ADR §7.3; RD §2, §4 | A failed run never feeds the next | Bad data never carries forward | NEC | S | Wrong carried items and stock propagate |
| F8 | Recon it-tests pinning purged-hold reachability through metadata and `reference` (L5; EN-2318, EN-2319) | ADR §9 L5 | Pins the Ledger contract the design relies on | The Ledger closed EN-2331 without that test | NEC | S | A Ledger change could break the flow silently |
| F9 | Rewind oracle regression test against a checkpoint (R10, EN-2334) | ADR §5, §4; DD §7.4, §7.8 | Compares the rewound stock with a checkpoint listing under writes | Guards the rewind against Ledger changes | OPT (keep advised) | M | A synthetic unit test covers the fold; loses the end-to-end guard |
| F10 | Optional periodic proof run against a checkpoint (ADR-003) | ADR §5, §4 | Rewound stock at `S = checkpoint.max_sequence` must equal the checkpoint | Extra assurance | **Removed from V1** | M | Nothing in V1 depends on it |

## 7. Operations and recovery

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| G1 | Retention: a deployment setting (`--lettering-retention`, default 90 d), applied through one storage lifecycle rule on `{bucketID}/reconciliation/` | ADR §7.4, §7.8, D4 | The operator sets the S3/Azure lifecycle rule at installation; recon deletes nothing, and uses the retention to know which replays are byte-identical | Bounded storage (7–14 GB per rule over 90 d) | NEC | S | Unbounded storage |
| G2 | Recon's own `expiresAt` sweep (fallback) | ADR §7.4; RD §2 | Recon deletes expired files itself | Deployments without a lifecycle rule | **Removed from V1** | M | Saves a deletion job (and its risk). The lifecycle rule is required at installation: without it the files are never deleted |
| G3 | Monthly stock anchors (`anchor`, object tag, `anchorRetention` 13 mo; keep manifest + stock + carried) + per-file `expiresAt` | ADR §7.4, D4, D14 | The last run of each month outlives the 90 d | Keeps an old replay cheap | **Removed from V1** | M | Saves tagging, two retentions per run, and per-file expiry. An old replay then rewinds from head (G4). Depended on by G5 and I4's retention |
| G4 | Replay of a past day from head (the rewind generalised) | ADR §7.7; DD §4 | Any day recomputed from the permanent logs | Audit, recovery | OPT (keep: nearly free) | S | Cost grows with age (~6 min at 90 days, ~26 min a year later at 1M tx/day, DD §7.16) |
| G5 | Replay from the nearest stored stock (forward or backward; `stockFrom` `daily`/`anchor`/`head`) | ADR §7.7, D14; DD §4 | Starts a replay from a stored stock instead of head | Bounds an old replay to ~½ month of txs | **Removed from V1** | M/L | Saves two fold directions, a start-point selection and the stored-stock verification. Old replays fall back to G4 (minutes, rare) |
| G6 | Exact replay variant through the logs `(S_prev, S]` | ADR §7.7; DD §4 | Re-derives a day from the logs, not the mutable metadata | Exactness if the metadata changed | **Removed from V1** | S (doc) | Drop the mention; nothing implemented |
| G7 | Forward stock as a daily mode (open book > ~14 % of daily traffic) | DD §7.10 | Previous stock + the day's txs instead of list + rewind | Faster for huge open books | OPT (already "not needed in V1") | S (doc) | Keep as a measurement only |
| G8 | Watch slice retention (kept while a manifest lists it, unlisted swept after 7 d) | ADR §7.4; RD §2 | Lifecycle of the slices | Storage | **Deferred after V1** | S | Goes with B12 |
| G9 | Result store generic for `stale_holds` (EN-2324) | ADR §11 | Layout, manifest, hash and retention made template-agnostic | Reuse | **Deferred after V1** | S/M | Build it for lettering first and generalise when `stale_holds` needs it |

## 8. Configuration knobs

Every knob a customer or an operator can set, with the feature it belongs to. It inherits that
feature's interest unless the row says otherwise.

| Knob | Level | Where | Feature | Interest | If removed |
|---|---|---|---|---|---|
| `psp.key`, `product.key` | rule | ADR §6 | A1 | NEC | — |
| `psp.state`, `product.state` (field + sets) | rule | ADR §6 | A3 | NEC | — |
| `holds[].prefix`, `holds[].openSign` | rule | ADR §6, D11 | A4 | NEC | — |
| `holds[].businessId` | rule | ADR §6, D18 | A5 | NEC | — |
| `psp.paymentAccount` | rule | D17 | A6 | NEC | — |
| `psp.merchantRef` | rule | ADR §6 | A8 | OPT | Pairing gone |
| `psp.movementKeys` | rule | D23 | A9/E13 | OPT | Debit book gone |
| `product.grace`, `psp.grace` | rule | D16, D20 | C3 | NEC | One grace would do, but loses the asymmetry |
| `psp.maxAge`, `product.maxAge` | rule | ADR §6 | C6 | OPT | No `stuck` |
| `buckets` | rule | ADR §6 | C7 | OPT | Hard-code the 4 buckets, or drop them |
| `--lettering-retention` | operator | ADR §7.4, §7.8, D4 | G1 | NEC (default 90 d) | Fixed at 90 d; the lifecycle rule must match it |
| `anchorRetention` | rule | ADR §7.4, D14 | G3 | **Removed with G3** | Goes with the anchors |
| `backfillFrom` | rule | ADR §7.6, D9 | B17 | NEC (a default exists) | Keep the default, drop the knob? The knob is also the re-seed substitute (D10) |
| `periodType` (`daily`/`weekly`/`monthly`), timezone, cut-off | rule | ADR §6, D8 | I1/I4 | NEC (daily, tz, cut-off); OPT (weekly/monthly) | — |
| `asset` (`"*"` fan-out) | rule | ADR §6 | A12 | NEC | — |
| `severity` | rule | RD §3 | I1 (reused) | NEC (existing) | — |
| `topK` | unspecified (10 in the example) | RD §5, §6 | E14 | — | **Gap:** say whether it is a rule parameter or a constant (prefer a constant) |
| `--lettering-read-ranges` | operator | ADR §7.8, D13 | B6 | OPT | K fixed at 8 |
| `--lettering-max-concurrent-reads` | operator | ADR §7.8, D13 | B7 | OPT | No cap |
| `--lettering-watch-interval` | operator | ADR §7.8, D25 | B11/B12 | **Removed with B12** | No watch in V1: B11 is deferred too |

## 9. Periods, aggregation and alerting

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| I1 | One aggregate alert per (rule, fingerprint, period), existing alert-period model, daily schedule by default | ADR §6, D8 | Reuses the existing alerting | Never one alert per payment | NEC | S | No notification (reuses existing infra) |
| I2 | Alert trigger: an open break; never the net; `incomplete` → engine-error alert | ADR §6, D16; RD §4 | Opening rule | Pending items and offsetting breaks move the net | NEC | S | Noisy or missed alerts |
| I3 | Alert evidence = the full rendered statement (bridge, open items, books, triage text, lookup hints) | ADR §6; RD §10 "The statement" | A text *état de rapprochement* in the alert | Controller-readable | OPT | M | Minimal evidence (verdict, counts per class, top-K breaks, link) is enough for V1. Rendering follows E8–E10 |
| I4 | Weekly/monthly periods for lettering rules (the monthly alert opened by the first failing daily run) | ADR §6, §7.5, D8 | Reuses `periodType` | Monthly close | OPT | S | Daily-only lettering rules in V1 |
| I5 | `period.json` summary (one entry per day from the daily manifests, gaps, expired links, never rewritten, kept like an anchor) | ADR §7.5; RD §6 | A file of the period's last run | A period view that outlives the daily files | **Removed from V1** | M | Saves a file format, "last run of the period" detection, the anchor-like retention and the self-reference rule (no `manifestSha256` for the last day). The same view is a query over the daily manifests. Depends on G3 for its retention |

---

## Counts

- **Features:** 104 numbered rows in §1–§7 and §9. §8 lists 20 configuration knobs, which map to
  those rows and are not counted again.
- **NEC:** 49, E14 included (its top-K breaks list).
- **OPT:** 32, E14's pending and resolved lists not counted separately, and E10 counted here since
  it is simplified, not removed. C10, D5, E8, E9, F4 and F5 are kept by decision. Of these, 3 are doc-only, proposed,
  or already outside V1: A14, A15, G7.
- **Removed, deferred or replaced:** 23, B11, B12, B13, B14, B16, B18, C4, D8, D9, D10, D11, E15, E16,
  E17, F3, F10, G2, G3, G5, G6, G8, G9 and I5 (see "Decisions taken").

## Candidates to remove

Ranked by complexity saved × low loss. Tiers are coarse, and the order within a tier is a
judgement.

**Tier 1: large saving, low loss.**

1. **B12 + G8, incremental watch job and slices.** L saved: a new job type, slices in another
   ledger's bucket, the manifest `watch` block, slice retention. Decided (D25): deferred, and the
   flag is dropped; the full read is deferred too (item 16).
2. **G5 + G3 (+ per-file `expiresAt`), replay from stored stock and monthly anchors.** M/L saved.
   An old-day replay falls back to the rewind from head (G4), which takes minutes and is rare.
   `anchorRetention` goes too. Decided (D4, D14).
3. **I5, `period.json`.** M saved. A query over the daily manifests gives the same view. With G3
   gone, it would need its own retention anyway. Decided (ADR §7 item 5).
4. **D10 + E17 + D11, re-seed, `diagnostic.json`, transient/structural kinds.** M/L saved. Recovery
   becomes "fix, then restart the rule like a first run" (B17). Diagnose from engine logs.
   Decided (D26).
5. **D9, acceptance.** M saved, including an acceptance store and API that are not designed. A
   known break keeps the alert open until it is booked, which matches "fix by booking, no
   write-off". Decided (ADR §6).
6. **B14, phase-1 synchronous aggregate capture.** M saved, plus a capture kind and one of L7's
   two reasons. It only yields an inexact "now" figure about 20 s early. Decided (ADR §7 items
   1–2).
7. **E13 + A9, the debit book and `psp.movementKeys`.** M saved, plus +31 % on the PSP flow read.
   Payouts and fees are outside payment lettering. Keep the credit book (E12). On hold for the
   Connectivity review: `formancepayments` conversions and order fills need A9 on both sides of
   the book (DD §2).
8. **B16, the resumable phase-2 job.** M saved. A run's reads take about 20 s; restart it.
   Decided (ADR §7 item 2).

**Tier 2: small or medium saving, near-zero loss.**

9. **E16, file parts.** A single gzip per file is fine at 75–142 MB. Decided (RD §8).
10. **B13, the purge consistency check.** The rewind is exact without it, per the design doc.
    Decided (DD §4).
11. **C4 `firstSide`, D8 `previousClass`, E10 `flowGross`/`offsetting`, E15 `execution`/`timingsMs`,
    F3 `logSha256`.** Each is S, and none is read by any check or trigger. Decided (D19, D21,
    ADR §5 and §7 items 7–8): all go but `flowGross`, which the alert's headline shows first.
12. **G2, recon's own expiry sweep.** Require the storage lifecycle rule instead. Decided (D4,
    ADR §7 items 4 and 8): the retention becomes the deployment setting `--lettering-retention`,
    and the rule loses its `retention`.
13. **B18, G6, G7, A14, F10.** Doc-only mentions or options already outside V1: prune them from the
    docs. Decided (removed: B18, G6, F10; A14 and G7 kept).
14. **G9, a generic result store for `stale_holds`.** Defer the generalisation. Decided (deferred).
15. **F4, `stored_file_mismatch`, and F5, the byte-identical replay guarantee.** Keep deterministic
    row order, and drop the "replay proves itself" contract and its 4 conditions. Decided (both kept).

**Tier 3: medium saving, a real but acceptable loss (owner's call).**

16. **B11, the metadata watch altogether.** ~95 % of the run's time and the only log read. The
    convention loses its monitor, but continuity, the payment-account book and the residual still
    catch the mutations that change today's result. It becomes unnecessary with L8. This is the
    biggest runtime lever, and the one with the most design weight behind it. Decided (D25):
    deferred until L8, and a run reads no range of logs.
17. **E8 + E9 + C10 + D5, the bridge lines, residual, `carriedOutside`, `letteredOther` and the
    open-items identity.** A statement a DuckDB query can rebuild. The residual and D5 are engine
    self-checks; D4 remains the completeness proof. Decided (kept).
18. **E6, the duplicated breaks file**, together with D6, lifecycle, and C8, `cleared` rows. This
    only holds if the alert can live without new-vs-persisting.
19. **I3, the full rendered statement in the alert.** Send a minimal alert instead.
20. **C6 `stuck`/`maxAge` and C7 buckets.** They overlap `stale_holds`.
21. **A8, merchantRef pairing.** It is cheap, and the docs call it the single most useful field. Keep
    it unless the Connectivity mapping cannot provide it.

**Gaps found while reading:**

- **`topK`:** where it is configured is unspecified.
- **`openedAt`:** its source for holds opened before the first run, or after a purge restarted
  their metadata, is unspecified.
- **E12, the credit book:** marked NEC. With `formancepayments` it closes on the payment events,
  which carry the key, but not on conversions and order fills without A9 (ADR §10 open question,
  DD §2). V1 must settle that review before the book ships.
