# ADR-005 lettering reconciliation: feature inventory

Every feature of the ADR-005 design, with its interest (necessary or optional), to decide which to
cut from V1. The "Decisions taken" table records what was decided, and the ADR or the cited doc
records each decision.

Sources: the docs at `ee62545a` on `feat/reconciliation-ledger-v3`, with the fixes of the
2026-10-02 consistency review. The first read was at `3387d699`; features have changed since,
through the decisions below, and every section reference was checked again against the docs.

- ADR-005, decisions 1–26 and asks L2–L10: `docs/prd/adr-005-transaction-level-reconciliation.md`
- the design doc: `docs/technical/transaction-level-reconciliation.md`
- the results reference: `docs/technical/transaction-level-results.md`
- the workflows doc, §9: `docs/technical/workflows.md`
- the DuckDB tool doc: `docs/technical/lettering-duckdb.md`

## Decisions taken

| Feature | Decision | Recorded in |
|---|---|---|
| B12 + G8, incremental watch and slice retention (and the `--lettering-watch-interval` flag) | Deferred after V1, and the full read with it (B11, below) | ADR dec. 25 |
| G5 + G3, replay from the nearest stored stock and monthly anchors (`anchorRetention`, the anchor tag, per-file `expiresAt`) | Removed from V1: a replay rewinds the live listing from head, newest first; once the previous day's run has expired (a day before its `expiresAt`), the replay is a first run and its carried items are seeded | ADR dec. 4, 14 |
| I5, `period.json` | Removed from V1: the period's alert lists each day (I6), and any other period view is a query over the daily manifests | ADR §7 item 5 |
| D10 + E17 + D11, re-seed, `diagnostic.json`, `incomplete.kind` | Replaced (D10) by a restart as a first run (D12): its optional `backfillFrom` is rejected past the next run's cut and defaults to the earlier of the first-run default and the oldest `firstSeen` of the last complete run's carried items; a restart without it is refused when the carried file failed its check; breaks start `new` with the same `breakId`. Removed (E17, D11): `incomplete.detail` names the first 20 items at fault, with no `diagnostic.json` and no `kind` field | ADR dec. 26; RD §2 |
| D9, acceptance of breaks one by one | Removed from V1: the alert opens on any open break; the controller acknowledges or accepts the alert itself (existing alert model), and a known break is fixed by booking it | ADR §6 |
| B14, a synchronous aggregate capture at the tick | Removed from V1: the run is one asynchronous job that resolves the cut, reads, joins, writes the files and one signed capture, and computes the exact aggregates at the cut; the tick only enqueues it | ADR §7 items 1, 2 |
| E16, file parts | Removed from V1: one gzip per data file, whatever its size (~5 s on one thread for the 74–142 MB flow file of a 1M day); readers follow `files` or glob `flow*`, so parts can come back without breaking them | RD §8; DD §7.14 |
| B13, purge consistency check (`purge_check`) | Removed from V1: the rewind repairs a touched hold missing from the listing, and an untouched one breaks the books' continuity on a chained run, where `openPrev` comes from the previous run's stock file (on a first run the listing gives both ends); no `purge_check` reason and no `purged_accounts` read | DD §4; ADR §5 |
| B16, resumable job | Removed from V1: no progress is recorded; writes are ordered (B19: data files, capture, manifest last, alert), a run exists once its manifest is written, and a run that did not finish is started again from the beginning with a new `runId` | ADR §7 item 2 |
| C4, `firstSide` on flow rows | Removed from V1: analytics only; `firstSeen` and the row's `psp[]` and `product[]` events give which side came first | ADR dec. 19 |
| D8, `previousClass` on break rows | Removed from V1: a class change stays the same break (same `breakId`, `persisting`), and the previous run's breaks file, joined on `breakId`, gives the old class; `check-chain` still checks that an open break is never lost | ADR dec. 21; RD §7 |
| E10, `flowGross` + `offsetting` flag | Simplified: the `offsetting` flag is removed; `flowGross` stays, since the alert shows it next to each day's net | ADR dec. 21; RD §5 |
| E15, the manifest's `execution` and `timingsMs` blocks | Removed from V1: K, the per-step durations and the read counts are exported as OTel metrics and engine logs; `startedAt` and `finishedAt` stay | ADR §7 item 8 |
| F3, `logSha256` in `cuts` and in the capture | Removed from V1: `T` per ledger suffices to replay, the logs are immutable, and the hash is stable for one protocol version only | ADR §5, §7 item 7 |
| G2, recon's own `expiresAt` sweep, and the rule's `retention` | Removed from V1: one storage lifecycle rule on `{bucketID}/reconciliation/`, required at installation, deletes the files; the retention is the deployment setting `--lettering-retention` (default 90 d), and the manifest's `expiresAt` (the run's start plus the retention, since the rule counts from each file's creation) tells recon up to when a previous run is usable, a day before it | ADR §7 items 4, 8; dec. 4 |
| B18, G6, F10, doc-only options | Removed from V1: follower reads, the exact replay through the day's logs and the periodic proof against a checkpoint are no longer mentioned as options; the oracle test (R10, EN-2334) is the only checkpoint use | DD §3, §4; ADR §4, §5, §11 |
| G9, a generic result store | Deferred after V1: the store is built for lettering; `stale_holds` (EN-2324) generalises it when it starts, with the same aggregates-only manifest and alert | ADR §11; stale-holds.md §9 |
| F4 + F5, the stored-file check and the byte-identical replay | Kept: F4 checks the previous run's stock, carried and breaks files against its signed capture, a missing file counting as altered, with no fallback to a rewind; it is what makes the capture useful to the next run. F5 follows from the determinism the golden tests need | RD §2, §4, §8; DD §5 |
| B11, the metadata watch and `anomalies.key_metadata_mutated` | Deferred after V1, until L8: a run reads no range of logs, and its reads take ~20 s instead of ~2.5 min at 1M payments. A change on a transaction of a day already read moves no balance, and one before the run is read at its latest value. The write-once convention (A16) and replay condition 4 (F5) are no longer monitored; `reconciled_with_warnings` means unclassified transactions only | ADR dec. 25, §5 caveat 1; DD §3; RD §4, §8 |
| E8 + E9 + C10 + D5, the reconciliation statement and its self-checks | Kept: the statement is the deliverable a controller reads (a statement, never a bare drift), and the residual and the open-items identity are sums over rows already in memory that prove the join and the carry-over | ADR §6; RD §5 |
| E6 + D6 + C8, the breaks file, the lifecycle and the cleared holds | Kept: new, persisting and resolved breaks, and the holds cleared with the transaction that cleared them, answer the controller's daily "what changed since yesterday"; the previous run's files are read anyway, and the API pages breaks from one file | ADR §6, §7 item 9; RD §6, §7 |
| I3, the full rendered statement in the alert | Simplified: the alert's evidence is structured data, the period's day list (I6), whose latest entry is the headline, and the latest day's `statement`, `books` and `paymentAccounts` blocks as JSON, with no list of breaks; the UI renders the statement from it and pages the breaks from the API, the engine renders no text, and no golden test pins one | ADR §6, dec. 8; DD §5; RD §5, §10 |
| C6, `stuck` via `maxAge` | Removed from V1: no `stuck` stock class (P4) and no `maxAge` rule parameter; the `stale_holds` template covers holds held too long, and the age buckets still show every open hold's age. Stock breaks keep `wrong_sign` (P4) | ADR §6, dec. 21; RD §6 |
| C7 + A8, ageing and the `psp.merchantRef` pairing | Kept: the age of every open hold is the credit-management view, and a stock break's row carries its `ageDays`; the pairing turns an unapplied payment into "apply it to invoice X" on its flow and stock rows (`pairedHold`, `pairedRef`) for a few fields | ADR §6; RD §5, §6 |
| The log-id cut `S`, part of B1 + B2 | Removed: the cut is `T` per ledger, and one mandatory index fewer (the log date). A run reads no range of logs, so `S` was only a label; its `ListLogs` read, `cuts[].logFrom` and `logTo`, its place in the signed capture and its `check-chain` comparison go with it. The flow reads `(T_prev, T]`, the rewind `(T, head_tx]`, and a replay needs only `T` per ledger | ADR §5, dec. 12, 24; DD §3; RD §6 |
| E14, the triage in the manifest, and the rule's `topK` | Removed from V1: the manifest carries aggregates only (the verdict, `incomplete`, the counts, the statement's figures, `books`, `paymentAccounts`, `files`); the statement's figures render from it alone, and lists of items come from the files, through the API. Open breaks, pending items and resolved breaks are in the breaks and flow files; the API pages breaks with filters on class, priority and lifecycle; the alert carries the day list (each day's verdict, counts and link, the latest as headline) and the latest day's statement figures; the bridge lines lose their `top` references. `incomplete.detail` keeps its first 20 items at fault, since an incomplete run writes no data file | ADR §6, §7 items 3, 9, dec. 8, 21; DD §5; RD §5, §6, §8 |
| E21, API paging of breaks and the UI | Made necessary: with no list in the manifest or the alert, the API and the UI are how a controller sees the breaks | ADR §7 item 9; RD §5 |
| B17, the first run's backfill | Changed: a first run (a new rule's, a restart's, a replay's past the retention, catch-up day X's) compares one day, `(T_prev, T]`, like every run; its starting stock and payment account are rewound to `T_prev`, and the backfill only seeds its open items, with none of the seed's transactions in the day's figures ("open items seeded since …"). A hold opened before the seed has a null `openedAt` and a lower-bound age | ADR §7 item 6, dec. 9; RD §2 |
| G10, catch-up from a past day X | Kept in V1 (OPT), decided by the owner and specified in the MVP tickets: an API action writes a normal run for every day from X to its last day `to` (X ≤ `to` ≤ yesterday, `to` yesterday by default), from one backward pass (a single rewind that records each day's open book and payment account) and the days forward, each chained on the one before; day X is a first run unless X−1 has a current run, with an optional `backfillFrom`; one job per rule (B21); it resumes nothing, stops on an `incomplete` day, raises no alert for a closed period and has no depth limit (about 40 min for 90 days, 2.5 h for a year, at 1M transactions a day) | ADR §7 item 7, dec. 14; DD §4; RD §2 |
| F2, the files' SHA-256 in the manifest and the manifest's in the capture | Kept (OPT): F4 and F5, both kept, rest on it | ADR §7 item 3; RD §6 |
| F9, the rewind oracle test against a checkpoint (R10, EN-2334) | Kept (OPT): the only checkpoint use left in V1 | ADR §4, §5, §11 |
| B20, the engine constants | Constants, not settings: key lookups go by groups of 100 references, key first; a failed read is tried 5 times (1, 2, 4, 8 s), then the job stops without its manifest and the run restarts at the next tick; no job budget | ADR §7 item 8 |
| B21, one job per rule | Added (NEC): the scheduled run waits while a catch-up runs, then chains on the last day that has a current run; a catch-up asked while a job runs is refused | ADR §7 item 7; DD §4 |
| G4, how a replay is requested | Decided: a replay is a catch-up of one day, the catch-up action (G10) with its optional last day `to` equal to X; there is no separate replay action | ADR §7 item 7, dec. 14; DD §4; RD §2 |
| D12, restart a stuck chain | Added (NEC), replacing D10 with the rules of the D10 row above; the next run's manifest, a first run with no `previousRun`, is the restart's only trace | ADR dec. 26; RD §2 |
| I6, the period alert rebuilt every tick | Added (NEC): every tick rebuilds the open period's alert from the current run of each of its days, idempotent; a period closes once the next period's first day has a current run, and a closed alert is never rebuilt; the day list carries each day's verdict, counts, net, gross and link, and its latest entry is the headline; no gap state | ADR §7 items 2, 5; RD §5 |
| C7, the rule's `buckets` | Simplified (owner, 2026-10-01 review of the optional features): the age buckets are fixed by the engine at 0–1, 2–7, 8–30 and > 30 days, and the rule has no `buckets` parameter. Each open hold's `bucket` and the bucket counts per book stay; a reader who wants other buckets computes them from `ageDays` | ADR §6, §7 item 8, dec. 3; RD §6 |
| E7, `unclassified.ndjson.gz` | Removed from V1 (owner, 2026-10-01 review of the optional features): the manifest's `counts.unclassified` and `statement.{asset}.unclassified` give the side, state value, count and amount, which name the fix; the ledger lists the transactions on the key, the state value and the day's id range | ADR §6; RD §1, §6 |
| A13, the booking conventions | Kept (NEC), split (owner, 2026-10-02 review of the necessary features): ADR §8 and the connector checklist mark each convention *required* or *recommended*, and the booking guide keeps the two apart. Recommended, since the engine reads none of them: the clearing account, `kind`, the `reference` format and the `merchant_ref` index | ADR §8; DD §2 |
| A5, `business_ref` on applications | Clarified (owner, 2026-10-02 consistency review): the PSP payment reference is the key. `business_ref` is required on every product transaction that touches a business hold *without* it (the opening, a credit note, a write-off), so that the flow read returns them; on an application it is recommended, and `product[].businessId` is absent when the application carries none | ADR §5, §8 rule 3; DD §2; RD §5, §6 |
| I3 + I6, the alert's evidence | Simplified further (owner, 2026-10-02 consistency review): no separate headline and no top-level `counts`, since the day list's latest entry carries them and is the headline; the evidence adds the latest day's `books` and `paymentAccounts`, so the statement renders from it alone | ADR §6, §7 item 5, dec. 8; DD §5; RD §5 |
| F7, the engine-error alert | Specified (owner, 2026-10-02 consistency review): the existing `engine.error` meta-alert, in the continuous scope, derived every tick from the rule's latest run: open while that run is `incomplete`, with its day, `runId`, reason and detail, and resolved by the tick that finds a later complete run | ADR §6; RD §4 |
| A3, the product's state sets | Simplified (owner, 2026-10-02 consistency review): the product side declares only its `final` set, the values that mark an application. No class reads a product `pending` or `failed` state, so those sets had no defined effect; validation now rejects them, and a product transaction whose state is in no set stays unclassified. Removing `product.state` altogether would not be equivalent (a correction marked with another state would become an application) and is not done | ADR §6, dec. 2; RD §6 |
| F1, the capture's content | Simplified (owner, 2026-10-02 consistency review): the capture keeps the verdict, `incomplete.reason`, `T` per ledger, the artifact URI and the manifest's SHA-256; the counts are in the manifest, which that hash signs, and "drifts" was never defined. The existing capture model carries a template's own evidence, with no drift field (`internal/models/capture.go:20-40`) | ADR §7 item 2; DD §3; RD §2 |
| B8, colors in the rewind | Fixed (owner, 2026-10-02 consistency review): the fold keys each value by (hold, asset, color), since the empty color is the uncolored bucket and not a sum, `ListAccounts` has no `collapse_colors`, and a transaction's `post_commit_volumes` covers only the tuples it touches (ledger `c56aeb98`); the colors are summed per asset once the fold is done | ADR §5, §6; DD §4; RD §6 |
| E2, which run counts | Fixed (owner, 2026-10-02 consistency review): a catch-up whose `to` is earlier than the latest day that has a current run, a replay of an earlier day included, writes **verification runs**, flagged `verification` in the manifest, which never become current; otherwise the next day's current run would stay chained on a run that is no longer its day's current one | ADR §7 items 5, 7, dec. 14; DD §4; RD §2, §6 |
| C7, a hold's age | Fixed (owner, 2026-10-02 consistency review): a hold opened before `backfillFrom` has a null `openedAt` on both ledgers, even when the product seed, `psp.grace` earlier, read its opening, so a hold opened earlier never shows a lower age than one opened later | ADR §7 item 6, dec. 9; RD §2, §6 |
| G1, when a previous run expires | Specified (owner, 2026-10-02 consistency review): a previous run is used while its `expiresAt` is more than one day away when the job starts; past that margin it counts as expired even if files remain, and the run that would chain on it is a first run; before it, a missing file is `stored_file_mismatch` | ADR §7 items 4, 7; DD §4, §5; RD §2 |

Abbreviations: **ADR** = ADR-005, **dec. *n*** = decision *n* of ADR §10 (not to be confused with
rows D1–D12), **DD** = design doc, **RD** = results doc.

The strict test for **NEC** (necessary): if the feature goes, the V1 result is wrong, is incomplete,
or no longer tells which payments are lettered, which are breaks, and that the day is complete.
Everything else is **OPT** (optional).

Cost is implementation plus doc complexity: **S** small, **M** medium, **L** large.

---

## 1. Inputs, booking and rule contract

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| A1 | Join key = PSP payment reference in indexed tx metadata (`psp.key`, `product.key`) | ADR §2.2, §6, dec. 1 | Each side names the metadata field that carries the PSP ref; the join is on transactions | Hold ids differ across ledgers. A purged hold was reachable by exact address on `v3.0.0-beta.6` only (`38c6eef55`; reverted at `v3.0.0-beta.7` by `23ea97c7f`), and an address prefix costs O(history) per page (47.8 s), while `payment_ref EXISTS` is O(window) | NEC | S | No join at all |
| A2 | Mandatory key and `businessId` indexes, rejected at validation, `INDEX_BUILDING` wait | ADR §5 caveat 2, §7 item 8, §8 rule 3 | The rule is refused without the indexes. An index still building is waited for through 5 read tries; then the job stops and reruns at the next tick (B20) | The flow read filters on the key's presence | NEC | S | Flow read impossible or unbounded |
| A3 | Per-side state sets: `pending`/`final`/`failed` on the PSP side, `final` only on the product side; overlap rejected | ADR §6, dec. 2 | The PSP side maps its state values to 3 meanings, the product side to its applications | PSP state vocabulary varies per deployment | NEC | S | Hard-coded vocabulary, breaks on the first connector |
| A4 | `holds[]`: `prefix` + `openSign` per hold kind (mixed signs, overlap rejected) | ADR §6, dec. 11 | Declares every hold prefix and the sign of an open hold under it | The sign cannot be inferred; the stock and application amounts need it | NEC | S | Stock and `wrong_sign` are ambiguous |
| A5 | `businessId` per product hold kind (`business_ref` on every hold tx without the payment reference, the opening included; recommended on applications) | ADR §6, §8 rule 3, dec. 18 | The product side names the business-id field per hold kind | Continuity needs hold openings in the flow read; on an application that carries it, it shows the payment → invoice link next to the hold | NEC | S | Continuity cannot be computed (no `opened(W)`) |
| A6 | `psp.paymentAccount`: PSP amount = net posting on an account pattern (`*` = one segment) | ADR §6, dec. 17 | The PSP amount is read on the account a final credits, not on the hold | The hold shows 0 or the wrong amount when no pending came first, or when the amounts differ | NEC | S | Wrong PSP amounts, so false or missed breaks |
| A7 | Application amount = net posting on the side's hold prefixes, in the settling direction | ADR §6 | Revenue recognition in the same batch counts for 0 | Defines "applied" without a `kind` tag | NEC | S | No definition of an application |
| A8 | `psp.merchantRef` pairing (`merchantRef`, `pairedHold`, `pairedRef`, "apply invoice X") | ADR §6, §8 rule 3; DD §2 | An unapplied payment is paired with the open hold its merchant ref names | Turns "money arrived, PAY-42" into an action item | OPT, **kept** | S/M | Saves a pairing pass and 3 fields. Loses the most actionable hint the design has (DD: "the most useful single field"). `formancepayments` does not carry it today |
| A9 | `psp.movementKeys` (payout/fee keys added to the PSP flow membership) | ADR §6, §8 rule 10, dec. 23 | One extra `EXISTS` term per declared field; those txs feed the payment-account book only | Lets the book close at 0 on movements with a key of their own, on both sides: payouts and fees, and with `formancepayments` its conversions and order fills | OPT, **on hold** (Connectivity review) | S | See E13. Saves a contract field and about +31 % on the PSP flow read, but `formancepayments` conversions and order fills then leave a P1 residual on both sides of the book (DD §2) |
| A10 | Refunds and chargebacks as their own 1-to-1 pairs | ADR §6, dec. 7 | A refund is its own reference and a refund hold, never a reversal | Keeps one generic model | NEC | S | Nothing to save: already the simplest option |
| A11 | Exact comparison, no tolerance | ADR §6, dec. 6 | Any fee or FX difference is a break | Fees must be booked explicitly | NEC | S | Nothing to save: already the simplest option |
| A12 | Per-asset arithmetic: exact minor units, colors summed per asset after a rewind that folds per color, `asset: "*"` fan-out | ADR §6 | Every figure and row is keyed by asset | Multi-currency correctness | NEC | S | Mixed-asset sums are wrong |
| A13 | Booking conventions, connector checklist (12 rows), customer booking guide (R11, EN-2335) | ADR §8; DD §2 | Tells the implementer how to book both ledgers so the rule can read them | The engine's efficiency and completeness rest on the booking | NEC; each convention marked required or recommended | M (doc) | The rule cannot be onboarded. The advice-only parts, the clearing account (rule 5), `kind`, the `reference` format and the `merchant_ref` index, are marked *recommended* rather than removed (2026-10-02): the engine uses none of them |
| A14 | Payment-to-apply (suspense) hold option: `holds[].role`, `state.received` | DD §2 "When application is deferred" | An alternative booking for manual lettering, with the rule additions it would need | B2B unapplied cash | OPT (already outside V1) | S (doc) | Saves a DD section. Nothing lost in V1: a longer `product.grace` covers the case |
| A15 | "Movement states" (option B of the open Connectivity question) | ADR §10 open; DD §2 | Proposed: a rule set of states whose keyed txs feed the book, not matching nor `unclassified` | `formancepayments` credits the payment account from payouts, transfers, reversed refunds… | OPT (proposed, not decided) | M | Not adding it keeps the contract smaller. Option A (connector keys) or C (warning) avoid it |
| A16 | Write-once convention on key, state, business-id and merchant-ref metadata | ADR §5 caveat 1, §8 rule 3 | A correction is a new transaction, never a `SavedMetadata`. Not monitored in V1 (B11); L8 would enforce it | Transaction metadata is mutable; a filtered re-read of a past window could change | NEC (convention) | S (doc) | Past days are no longer reproducible |

## 2. Run and cut

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| B1 | Cut = tx id `T` per ledger at the business cut-off (`inserted_at`, not `timestamp`); no log-id cut `S` | ADR §4 option C, §5 | Converts the cut-off into id ranges `(T_prev, T]` | A day is frozen once cut; no checkpoint; both ledgers cut at the same business time | NEC | M | No deterministic window |
| B2 | Cut resolved on the `inserted_at` index, mandatory; no log-date index; bisection dropped | ADR §5, dec. 12; DD §3 | One page per ledger: the first transaction after the cut-off, minus 1 | One read, one code path | NEC | S | Needs bisection instead (a 2nd path) |
| B3 | Bounded date filter, widened while empty (δ) | ADR §5, dec. 24; DD §7.12 | `cut-off < inserted_at ≤ cut-off + δ` instead of an open filter | The ledger materializes a date range before paging it | OPT | S | Nothing for the daily run (ms). Open, a cut costs ~85 ns per entry since the cut-off (~30 s a year later), so the bound is needed by old replays (G4), by G10 (one cut per day from X−1) and by long `backfillFrom` seeds |
| B4 | Flow read: `ListTransactions` `And(membership, id ∈ (T_prev, T])`, membership first, `reverse = true` | ADR §5, dec. 10; DD §3, §7.11 | Reads only the keyed txs of the day | O(payments), whatever the other traffic | NEC | M | O(all traffic) through the logs, 5–9× slower |
| B5 | Product membership `Or(payment_ref, business_ref per kind)` | ADR §5; DD §7.11 | The product flow also returns hold openings | Continuity needs `opened(W)` | NEC | S | Continuity breaks (see D4) |
| B6 | Parallel id ranges K (`--lettering-read-ranges`, default 8); the flow's facts combined in id order, the rewind's chunks applied newest first, each touch overwriting | ADR §7 item 8, dec. 13; DD §3, §7.7 | Every window is split into K ranges read concurrently | About ×4 on reads | OPT (keep) | M | One stream: a run's reads ~4× slower (over a minute instead of ~20 s at 1M payments). Correctness unchanged |
| B7 | Process-wide reader cap (`--lettering-max-concurrent-reads`, default 16) | ADR §7 item 8, dec. 13; DD §3, §7.15 | Readers wait for a slot across every run | Several rules must not multiply the load on one ledger | OPT | S | Saves a semaphore. Many concurrent rules could slow ledger writes |
| B8 | Stock = live `ListAccounts` of open holds, rewound with the unfiltered txs `(T, head_tx]` | ADR §5, dec. 22; DD §4 | Newest-first rewind of `post_commit_volumes`, each touch overwriting the value, so the balance before the first touch after `T` is left; purged holds added back, holds created later dropped | Exact stock at the cut with no checkpoint (0 of 1M rows wrong) | NEC | M | The torn live listing (2,233 wrong rows in 1M); continuity is meaningless |
| B9 | Count check `hi − lo` on unfiltered windows → `incomplete` (`short_range`) | ADR §5; DD §3 | An unfiltered window must return exactly `hi − lo` rows | Detects a lagging replica or a failed read | NEC | S | A short read shrinks the window silently |
| B10 | Key lookups by reference (unknown applied refs on the PSP side; the history of refs found final; failed refs of the window on both ledgers), in groups of 100, an `Or` of equalities, key first | ADR §6, §7 item 8, dec. 16, 18; DD §5, §7.13 | References missing from the window and from the carried items are read by key, up to `T` | Otherwise a 2nd application reads as an orphan, and a payment matched earlier then failed today is missed | NEC | M | False P1 orphans, and a missed `reversed_after_application`. The group size is an engine constant (B20) |
| B11 | Metadata watch, full read at run time (`key_metadata_mutated`) | ADR §5 caveat 1, dec. 25; DD §3 | Reads every log since the previous run for metadata changes on txs | Monitors the write-once convention | **Deferred after V1 (until L8)** | M | Saves the only range of logs a run read, **~95 % of the run** (134–158 s at 1M payments): the reads now take ~20 s. Loses the `key_metadata_mutated` warning and the check behind replay condition 4 (F5). A change on a day already read moves no balance, and one before the run is read at its latest value. L8 (immutable labels) removes the need by construction |
| B12 | Incremental watch job with slices (the former default, `--lettering-watch-interval=1h`) | ADR dec. 25; DD §3 | A job per watched ledger writes sealed slices; the run assembles, verifies and re-reads missing ranges | Takes the watch off the run's critical path | **Deferred after V1** | **L** | Saves a job type, slice and seal files, one chain per ledger in *another* ledger's bucket, the manifest `watch` block (`slices`, `reread` with 5 reasons), slice tagging and the 7-day sweep. The slices are not measured yet. Depends on B11, deferred too: with no watch, the reads take ~20 s anyway |
| B13 | Purge consistency check from `purged_accounts` (`purge_check`) | ADR §5; DD §4 | A hold open at the cut, touched, missing from the listing, must be named in some `purged_accounts` | Detects a listing that missed a live account | **Removed from V1** | S/M | The rewind repairs a touched hold missing from the listing, and continuity catches an untouched one on a chained run (DD §4). Saves a reason and a batch-boundary subtlety. It read B11's logs. `purged_accounts` is gone at ledger `v3.0.0-beta.7` (`23ea97c7f`) anyway |
| B14 | Synchronous aggregate capture at the tick (`AggregateVolumes` per prefix, signed by `openSign`, labelled with the run instant) | ADR §7 item 1 | A capture of the live exposure, in seconds, before the detail | Early figure | **Removed from V1** | M | Saves a 2nd capture kind, a code path and one of L7's two reasons (the rewind skip stays). Loses an inexact "now" figure available ~20 s earlier. The job computes the exact aggregates at the cut anyway |
| B15 | The run's async job, idempotent per (rule, period, cut), own execution path, no 10 s drain grace | ADR §7 item 2, §11 | The per-key computation runs off the scheduler tick | ~20 s of reads and 1M rows at 1M payments, more on a first run's seed | NEC | M | Cannot fit in the scheduler's synchronous path |
| B16 | Resumable job | ADR §7 item 2 | A run resumes after a crash | Avoid redoing work | **Removed from V1** | M | The run restarts from the beginning with a new `runId` (~20 s of reads at 1M payments): no loss |
| B17 | First run: one day, like every run, with its open items seeded from `backfillFrom` | ADR §7 item 6, dec. 9, 19; RD §2 | A first run is a new rule's, a restart's (D12), a replay's past the retention, or catch-up day X's. Its window is `(T_prev, T]`; `openPrev`, `input(T_prev)` and `output(T_prev)` are rewound one day further. A seed read of the flow from `backfillFrom` up to `T_prev` (product side `psp.grace` earlier) gives the references still open there, and counts in none of the day's figures ("open items seeded since …"). `backfillFrom` is a date in the rule's timezone, default cut-off − max(grace) − 1 d; on or after the compared day, there is no seed. The same `backfillFrom` and cut give the same result; an earlier one can find more open items | Otherwise a payment finalised before the rule and never applied is never seen. Started from zero, the payment-account book would give a false P1 and the hold books would fail continuity | NEC | M | Old unapplied payments are invisible forever. The product-side offset is a refinement: without it, the seed carries false `unapplied_payment` rows |
| B18 | Follower reads (`x-consistency: stale`), enabled by the count check | DD §3 (mention dropped) | Could offload the leader | Load | **Removed from V1** | S | Nothing lost |
| B19 | Write order: data files, then the signed capture (verdict, `T` per ledger, manifest SHA-256), then the manifest last, then the alert | ADR §7 item 2; RD §2 | A run exists once its manifest is written. A job that stops earlier leaves files no reader counts, and the unfinished run restarts with a new `runId` | Readers pick runs from the manifests; the capture signs the manifest's hash, computed before it is written | NEC | S | A reader could count a half-written run, or a manifest no capture signs |
| B20 | Engine constants: key lookups in groups of 100 refs (`Or` of equalities, key first); a failed read tried 5 times (1/2/4/8 s), then the job stops and the run restarts at the next tick | ADR §7 item 8; DD §5 | Not configurable: no flag, no rule parameter, no job budget | Lookups at about a hundredth of their cost one by one; a transient failure or an index still building (`INDEX_BUILDING`) is waited for, tick to tick | OPT | S | Lookups one by one (1.3 ms each, DD §7.13), and a transient read failure costs the whole run, which restarts at the next tick |
| B21 | One job per rule at a time | ADR §7 item 7; DD §4 | The rule's scheduled run waits while a catch-up runs, then chains on the last day that has a current run; a catch-up asked while a job runs is refused | Each run chains on the previous day's current run | NEC | S | A scheduled run could chain on a day the catch-up has not written yet, or two runs race for one day's current run |

## 3. Matching and classification

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| C1 | Join per reference, applications summed per reference (split, partial) | ADR §6 "Cardinality" | One payment may be applied by several txs | Split payments are common | NEC | S | False under- or over-applications |
| C2 | 9 flow classes (`matched`, `under_applied`, `over_applied`, `unapplied_payment`, `in_progress`, `failed`, `applied_before_final`, `orphan_application`, `reversed_after_application`), read on net amounts | ADR §6, dec. 18, 21; RD §6 | Classifies every reference | The core output | NEC | M | No result. Minor merges are possible (`reversed_after_application` into `orphan_application`, both P1; `in_progress` and `failed`, both `ok`), for little saving |
| C3 | Per-side grace (`product.grace` 1 d, `psp.grace` 7 d), `pending` outcome, `firstSeen`, `breakOn`, promotion to a break | ADR §6, dec. 16, 20 | Lets each side lag the other before it is a break | Cross-midnight lag; SEPA/ACH apply at pending | NEC | S/M | Every cross-cut lag is a break. One shared grace would be simpler, but loses the 1 d vs 7 d asymmetry |
| C4 | `firstSide` on flow rows | ADR dec. 19 | Which side came first (PSP terminal state or first application) | Analytics only | **Removed from V1** | S | Nothing functional lost: `firstSeen` and the row's events give it |
| C5 | Stock classes `open` + `wrong_sign` (P4) | ADR §6, dec. 11; RD §6 | Open hold, or balance of the wrong sign | `wrong_sign` is the only signal for an invoice paid twice by two separately matched payments | NEC | S | A double payment of one invoice goes unseen (both flows are `matched`) |
| C6 | `stuck` via `maxAge` (no default, per side) | ADR §6, dec. 21 | Open past `maxAge` → P4 break | Per-key `stale_holds` signal | **Removed from V1** | S | Saves a rule parameter per side and a stock class. `stale_holds` covers held-too-long holds, and the buckets (C7) still show the age. Without a `maxAge` it never fired anyway |
| C7 | Ageing: `openedAt`, `ageDays`, `bucket`, fixed buckets (0–1, 2–7, 8–30, > 30 d) | ADR §6, §7 item 6; RD §6 | Age of every open hold and bucket counts per book. `openedAt` is the opening transaction's `timestamp`, seen in a read window and kept in the stock file; it is null for a hold opened before `backfillFrom`, on both ledgers, even when the product seed read its opening, and the age is then a lower bound from `backfillFrom` | Credit-management view | **Simplified** (OPT): fixed buckets, no `buckets` rule parameter | S | Saves the bucket counts in `books`. `ageDays` stays on the stock rows, where a stock break shows it |
| C8 | `cleared` stock rows (`previousBalance`, `clearedAt`, `clearedBy`) | ADR §6; RD §6 | A hold open at the previous cut and lettered since is listed once | Shows what the day lettered; resolves stock breaks | OPT, **kept** | S | The stock file shows only open holds. `letteredOther` is still visible in `books`. Depends on D6 |
| C9 | Unclassified txs: counted per side, state and asset, warning, never dropped | ADR §6; RD §5 | A tx whose state is in no set takes no part in matching but is reported | Catches connector mappings that break the conventions | NEC | S | Silent loss of money movements |
| C10 | `letteredOther` in the books | ADR dec. 21; RD §5 | Letterings by txs outside matching (credit notes, write-offs, unclassified) | Makes the bridge's B equal the applications; shows manual letterings | OPT, **kept** | S | Needed only by E8 (the residual). Manual write-offs become invisible inside `lettered` |
| C11 | `outcome` on every row (`ok`/`pending`/`break`) | RD §3 | One field answers "must someone act?" | Readers filter on it | NEC | S | Readers re-derive it from the class plus the grace |
| C12 | `priority` 1–4 per class | ADR §6; RD §6 | A fixed class → urgency mapping | Triage order | OPT | S | Low saving; the order falls back to the class. Loses the alert headline's P1 count, `counts.breaks.openByPriority`, the API's priority filter (E21) and the breaks file's order (RD §8) |
| C13 | Verdict: `incomplete` > `breaks` > `reconciled_with_warnings` > `reconciled_with_pending` > `reconciled` | ADR §6; RD §4 | One ordered status per run | "Is the day reconciled?" | NEC | S | No status. It could shrink to 3 values plus flags, a small saving |

## 4. Carry-over and chain

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| D1 | Carried items: every reference with drift ≠ 0 goes to the next run | ADR §2.3, dec. 15 | Reconciliation's own open-items book, day to day | Unapplied payments and pending applications span days | NEC | S/M | Every cross-day item is lost or misclassified |
| D2 | Separate `carried.ndjson.gz` (instead of filtering the flow file) | RD §6; DD §7.14 | A small file with the carried rows, without `impact` | The next run reads 5–9 MB, not the 74–142 MB flow | OPT | S | The next run filters the flow file (a few seconds) |
| D3 | Chain: `previousRun` (runId, day, `manifestSha256`); a window over missed or incomplete days ("window since …") | RD §2 | Each run names its predecessor and starts at its cut | Continuity and carry across gaps | NEC | S | No way to find the carried items and `openPrev` |
| D4 | Books continuity per side, prefix and asset: `open = openPrev + opened − lettered` | ADR §2.3; RD §4, §5 | Ties the window's hold movements to the rewound stock. `openPrev` comes from the previous stock file, checked against its signed capture (missing or altered: `stored_file_mismatch`, with no fallback, F4); a first run rewinds it to `T_prev` | The only completeness proof of the filtered flow read | NEC | M | A dropped event silently shrinks the universe |
| D5 | Open-items (suspense) identity: `open = openPrev + net + fromLookups` → `incomplete` (`continuity`) | ADR dec. 21; RD §5 | Ties carried(T_prev) + the window's net to carried(T) | Detects a carried item lost or counted twice | OPT, **kept** | S | An engine self-check on the engine's own file. `suspense.open` (the sum of carried drift) can stay as a figure. Needs E8's `impact` |
| D6 | Lifecycle: `new`/`persisting`/`resolved` (breaks), `new`/`persisting`/`cleared` (holds) | ADR §6; RD §7 | Compares with the previous run's artifacts | "What changed since yesterday" | OPT, **kept** | M | Saves the diff against the previous breaks and stock files, the `resolved` rows and part of `check-chain`. Loses new-vs-persisting in the breaks file and the alert's counts |
| D7 | Stable `breakId` (hash of rule, leg, key, asset; not the class) | ADR §6, dec. 21 | A break keeps its id when its class changes | Comments, lifecycle | OPT | S | Cheap. Required by D6; with D8 gone, it is also how a reader finds a break's earlier class |
| D8 | `previousClass` on the day the class changes | ADR dec. 21; RD §7 | Records the old class | Audit trail | **Removed from V1** | S | Nothing functional lost: the previous run's breaks file, joined on `breakId` (D7), has the old class |
| D9 | Acceptance (`acceptedOn`; lapses when the class or amount changes; excluded from the alert trigger) | ADR §6; RD §4 | A known break stays in the files but stops opening the alert | Systematic known gaps (e.g. an unbooked fee) until they are booked | **Removed from V1** | M | Saves an acceptance store and API, the lapse rules and `counts.breaks.accepted`. Loses the means to silence known breaks: the alert stays open until booked. Depends on D7 |
| D10 | Re-seed run (`reseed`: stock from head, carried rebuilt by key lookups on both ledgers, `reseed.adjustment`) | ADR dec. 26; RD §2 | Restarts the chain after a structural `incomplete` is fixed | A structural cause repeats every day and the window grows | **Replaced** by D12 | M/L | The restart D12 is a first run (B17). Breaks start `new`, and `openedAt` is null for the holds opened before the new seed. `backfillFrom` is required only when the carried file failed its check |
| D11 | `incomplete.kind` transient/structural | ADR dec. 26; RD §4 | Classes the 5 reasons in 2 kinds | Tells the operator whether to wait or act | **Removed from V1** | S | The engine-error alert says from the reason whether to wait (`missing_index`, `short_range`) or act; the engine renders no text. Needed by D10 and E17 |
| D12 | Restart a stuck chain (operator action) | ADR dec. 26; RD §2 | The next run is a first run (B17) with no `previousRun`. The optional `backfillFrom` is rejected past the next run's cut; its default is the earlier of the first-run default and the oldest carried `firstSeen`. The restart is refused without `backfillFrom` when the carried file failed its check. Breaks start `new` with the same `breakId`; the next manifest is the only trace | A cause whose fix cannot enter the window (a booking corrected by a new transaction, an altered stored file) repeats on every run | NEC | M | A chain stuck on `continuity`, `residual` or `stored_file_mismatch` stays `incomplete` for good, short of a new rule |

## 5. Outputs and files

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| E1 | Storage in the product ledger's backup destination, under a sibling prefix outside `backups/`; own credentials; `s3`/`azure`/`file` drivers | ADR §7 item 3, dec. 4 | Recon's first durable dependency | 1M-row detail cannot sit in a capture | NEC | M | No per-key detail |
| E2 | Layout `rule=/day=/run=`; sortable `runId`; current run = latest complete run that is not a verification run; a retry writes a new run | RD §2; ADR dec. 15 | Hive-style paths, one current run per day | Query engines read the path as columns; retries are safe | NEC | S | Ambiguous "which run counts" |
| E3 | `manifest.json` core, written last: `schemaVersion`, `engine`, rule snapshot + sha256, `runId`, `previousRun`, `period`, `startedAt`/`finishedAt`, `cuts`, `verdict`, `counts`, `files` (rows, sha256), `expiresAt`, and `verification` on a verification run | ADR §7 item 2; RD §6 | The run's summary and index | Entry point for every reader | NEC | M | No index of the run |
| E4 | `flow.ndjson.gz`, self-contained rows (`psp[]`, `product[]` tx lists, `holdAmount` on pending/failed) | RD §6 | One row per reference and asset | "Which payments are lettered" | NEC | M | No per-payment answer |
| E5 | `stock.ndjson.gz` | RD §6 | One row per open hold (plus cleared) | Open book, `openPrev` for continuity | NEC | S | Continuity loses its `openPrev` |
| E6 | `breaks.ndjson.gz`, self-contained, resolved rows included | RD §6 | Duplicates the break rows of flow/stock plus the book breaks | A reader never joins files | OPT, **kept** | S/M | Breaks are `outcome = 'break'` in flow + stock + `paymentAccounts`. Loses the `resolved` list (see D6), one-file convenience, and the file E21 pages the breaks from |
| E7 | `unclassified.ndjson.gz` | RD §6 | One row per unclassified tx | Fixing the mapping | **Removed from V1** | S | `statement.unclassified` in the manifest already gives side, state, amount and count. Low saving |
| E8 | Statement bridge: A (payment account) − B (from books) = net, lines by class/outcome/`earlierDay`, `residual` = 0 else `incomplete`, `impact` field | ADR §6; RD §5 | The classic *état de rapprochement* for the window | Explains the net; the residual ties the join to the books | OPT, **kept** | M | The lines are a `GROUP BY` over the flow file (DuckDB `bridge` query). Loses the residual self-check (the join attributed every lettering), `impact`, and C10/D5/E9 with it. Continuity (D4) still guards completeness |
| E9 | `carriedOutside` lines | RD §5 | Carried items with no movement today, per class | Complete statement | OPT, **kept** | S | Derivable from the carried file |
| E10 | `flowGross` + `offsetting` flag | ADR §6; RD §5 | Σ\|drift\| of the open flow breaks, and whether both signs exist | A net of 0 can hide breaks | **Simplified** (OPT): `offsetting` removed, `flowGross` kept | S | The alert already opens on breaks, never on the net: nothing lost. `flowGross` stays, since the alert shows it next to each day's net |
| E11 | `books` block (`openSign`, `openPrev`, `opened`, `lettered`, `letteredOther`, `open`, `count`, `buckets`, `continuityOk`) | RD §5, §6 | One entry per side, prefix and asset | Carries continuity | NEC | S | D4 has no carrier. `buckets` goes with C7, `letteredOther` with C10 |
| E12 | Payment-account **credit** book → P1 `unkeyed_payment_movement` on leg `book` (`paymentAccounts` block) | ADR §8 rule 10, dec. 23; DD §7.9; RD §5 | `input(T) − input(T_prev)` must equal the flow's credits | The only check that sees a final with no pending and no key (100 silent finals left every other check green) | NEC | M | A silent hole in completeness. With `formancepayments`, blocked by conversions and order fills, which need A9 (ADR §10 open question, DD §2) |
| E13 | Payment-account **debit** book (`output`, `flowDebits`, `debitResidual`) | ADR dec. 23; DD §2, §7.9 | Debits must be keyed payouts, fees, refunds | Unkeyed payouts and fees are a residual | OPT, **on hold** (Connectivity review) | M | Payouts and fees are outside payment lettering. Saves the debit direction; A9 stays needed for `formancepayments` conversions and order fills, and the Connectivity question is unchanged (DD §2) |
| E14 | Triage in the manifest: top-K open breaks + top-K `pending` and `resolved` lists, the bridge lines' `top` references, and the rule's `topK` | ADR §7 item 3, dec. 21; RD §5 | The UI would render the statement and its items from the manifest alone | The alert and a dashboard would need no other file | **Removed from V1** | S | The manifest carries aggregates only. The items are in the breaks and flow files; the API pages breaks with filters on class, priority and lifecycle (E21), and the alert carries the counts and the link |
| E15 | `execution` + `timingsMs` blocks (`readRanges`, `stockFrom`, `rewindTxs`, `lookups`, `watchLogs`) | ADR §7 item 8; RD §6 | Run telemetry in the manifest | Comparable run durations | **Removed from V1** | S | Moved to OTel metrics and engine logs. No reader loss |
| E16 | File parts beyond 250,000 rows (`flow-00000…`, `part` in `files`) | RD §8; DD §7.14 | Splits a data file into parts | Parallel compression and upload | **Removed from V1** | S/M | One gzip of 74–142 MB: ~5 s on one thread; DuckDB reads it as fast. Saves the part logic in the writer, reader, checks and API |
| E17 | `diagnostic.json` for structural `incomplete` (≤ 1,000 items per reason) | ADR dec. 26 | Lists the books, holds, applications or files at fault | Debug a run that writes no data file | **Removed from V1** | M | Saves a file format with 4 item shapes. The operator diagnoses from `incomplete.detail` (first 20) + engine logs. Depends on D11 |
| E18 | `schemaVersion` `lettering/1`, a JSON Schema per file, compatibility rules | ADR §7 item 3; RD §8 | Versioned, documented format | The customer reads the files directly | NEC | S | The format cannot evolve safely |
| E19 | Every data file written on every complete run, even empty | RD §8 | No missing file on a quiet day | Globs never break | NEC | S | Readers special-case missing files |
| E20 | Result API: lists a run's files with pre-signed URLs; run status from the capture | ADR §7 item 9 | Customers read without bucket access | Access control | NEC | M | Customers need raw bucket credentials |
| E21 | API paging of breaks from the artifact, with filters on class, priority and lifecycle, + UI | ADR §7 items 3, 9; ticket EN-2322 | Recon's API and UI read the files | The UI shows the breaks this way, since the manifest and the alert list none | NEC | M | Without it no break is visible outside the files: the manifest and the alert carry aggregates only |
| E22 | Customer query examples (RD §9) + `tools/lettering-duckdb` (check, check-chain, 11 queries, Python reference engine) | RD §9; lettering-duckdb.md | Internal validation pack, outside recon CI | An independent reading of the format for QA and support | OPT (already built) | M (maintenance) | Saves updating ~50 SQL rules, the generator and expected CSVs on every format change. Loses independent validation. Worth freezing, not growing |

## 6. Integrity and guarantees

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| F1 | The run's capture on `_recon`, Ed25519-signed (verdict, `T` per ledger, artifact URI + manifest SHA-256; the counts are in the manifest) | ADR §7 item 2 (EN-1930) | Reuses the signed capture | The run's status and anchor | NEC | S | No status record (reuses existing infra) |
| F2 | File SHA-256 in the manifest; manifest hash in the capture (transitive signature) | ADR §7 item 3; RD §6 | Tamper evidence over every file | Audit | OPT, **kept** | S | Loses tamper evidence. Underpins F4, F5 and `check`'s `file_sha256` |
| F3 | `logSha256` of the log at the cut, in `cuts` | ADR §5 | Hash of the protobuf `Log` at the cut | Re-identify the cut exactly | **Removed from V1** | S | `T` suffices to replay; logs are immutable. The hash is stable for one protocol version only |
| F4 | `stored_file_mismatch`: the previous run's stock, carried and breaks files checked against its signed capture before use, while that run is usable (until a day before its `expiresAt`) | RD §2, §4; DD §5 | A missing file counts as altered, with no fallback to a rewind. The operator restarts (D12); a restart without `backfillFrom` is refused when the carried file failed | Chain integrity | OPT, **kept** | S/M | Saves a verification step and an `incomplete` reason. A corrupted file would propagate, but the next continuity check would likely fail anyway |
| F5 | Byte-identical data files for the same cut, rule, engine and previous run (fixed key and row order, gzip level 6, no name, no timestamp) | ADR §7 item 3; RD §8 | A replay proves itself by its SHA-256 | Audit reproducibility | OPT, **kept** | M | Deterministic row order is cheap and still useful for tests. The "proves itself" guarantee and its 4 conditions can go. Condition 4 is no longer watched (B11): a replay is compared with the original by the SHA-256s in the manifests' `files` |
| F6 | `missing_index` → `incomplete`, never a silent fallback | DD §3 | An index missing at run time is an engine error | No wrong window | NEC | S | Silent wrong result |
| F7 | `incomplete` = no conclusion: signed capture and reduced manifest, no data file, engine-error alert, not a chain link | ADR §7 item 2; RD §2, §4, §6 | A failed run never feeds the next. Its capture holds the verdict, `incomplete.reason`, `T` per resolved ledger and the manifest SHA-256. Its manifest has a fixed field list: `schemaVersion`, `engine`, `rule`, `runId`, `previousRun`, `period`, `startedAt`, `finishedAt`, `verdict`, `incomplete`, `expiresAt`; `cuts` only for resolved sides; no `counts`, `statement`, `books`, `paymentAccounts` or `files`. `incomplete.detail` names the first 20 items at fault | Bad data never carries forward | NEC | S | Wrong carried items and stock propagate |
| F8 | Recon it-tests pinning purged-hold reachability through metadata and `reference` (L5; EN-2318, EN-2319) | ADR §9 L5 | Pins the Ledger contract the design relies on | The Ledger closed EN-2331 without that test | NEC | S | A Ledger change could break the flow silently |
| F9 | Rewind oracle regression test against a checkpoint (R10, EN-2334) | ADR §4, §5, §11; DD §7.4, §7.8 | Compares the rewound stock with a checkpoint listing under writes | Guards the rewind against Ledger changes | OPT, **kept** | M | A synthetic unit test covers the fold; loses the end-to-end guard |
| F10 | Optional periodic proof run against a checkpoint (ADR-003) | ADR §4, §5 | Rewound stock at the checkpoint's position must equal the checkpoint | Extra assurance | **Removed from V1** | M | Nothing in V1 depends on it |

## 7. Operations and recovery

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| G1 | Retention: a deployment setting (`--lettering-retention`, default 90 d), applied through one storage lifecycle rule on `{bucketID}/reconciliation/` | ADR §7 items 4, 8, dec. 4; RD §2 | The operator sets the S3/Azure lifecycle rule at installation; recon deletes nothing. The manifest's `expiresAt` = the run's creation (its `runId` instant) + the retention: replayed or caught-up files expire one retention after that run. Recon uses a previous run until a day before it, which also tells which replays are byte-identical | Bounded storage (7–14 GB per rule over 90 d) | NEC | S | Unbounded storage |
| G2 | Recon's own `expiresAt` sweep (fallback) | ADR §7 item 4; RD §2 | Recon deletes expired files itself | Deployments without a lifecycle rule | **Removed from V1** | M | Saves a deletion job (and its risk). The lifecycle rule is required at installation: without it the files are never deleted |
| G3 | Monthly stock anchors (`anchor`, object tag, `anchorRetention` 13 mo; keep manifest + stock + carried) + per-file `expiresAt` | ADR dec. 4, 14 | The last run of each month outlives the 90 d | Keeps an old replay cheap | **Removed from V1** | M | Saves tagging, two retentions per run, and per-file expiry. An old replay then rewinds from head (G4). Depended on by G5 and I5's retention |
| G4 | Replay of a past day from head (the rewind generalised) | ADR §7 item 7, dec. 14; DD §4, §7.16 | Any day recomputed from the ledgers' transactions; the rewind grows with age (~6 min at 90 d, ~26 min a year later at 1M tx/day). Requested as a catch-up of one day (G10, `to` = X) | Audit, recovery | OPT (keep: nearly free) | S | No past day can be recomputed: neither byte-identical within the retention, nor as a first run with a seed beyond it |
| G5 | Replay from the nearest stored stock (forward or backward; `stockFrom` `daily`/`anchor`/`head`) | ADR §7 item 7, dec. 14; DD §4 | Starts a replay from a stored stock instead of head | Bounds an old replay to ~½ month of txs | **Removed from V1** | M/L | Saves two fold directions, a start-point selection and the stored-stock verification. Old replays fall back to G4 (minutes, rare) |
| G6 | Exact replay variant through the day's logs | DD §3, §4 | Re-derives a day from the logs, not the mutable metadata | Exactness if the metadata changed | **Removed from V1** | S (doc) | Drop the mention; nothing implemented |
| G7 | Forward stock as a daily mode (open book > ~14 % of daily traffic) | DD §7.10 | Previous stock + the day's txs instead of list + rewind | Faster for huge open books | OPT (already "not needed in V1") | S (doc) | Keep as a measurement only |
| G8 | Watch slice retention (kept while a manifest lists it, unlisted swept after 7 d) | ADR dec. 25 | Lifecycle of the slices | Storage | **Deferred after V1** | S | Goes with B12 |
| G9 | Result store generic for `stale_holds` (EN-2324) | ADR §11; stale-holds.md §9 | Layout, manifest, hash in the capture and lifecycle rule made template-agnostic; `stale_holds`' manifest and alert carry aggregates only too | Reuse | **Deferred after V1** | S/M | Build it for lettering first and generalise when `stale_holds` needs it |
| G10 | Catch-up from a past day X (API action) | ADR §7 item 7, dec. 14; DD §4; RD §2 | One backward pass records each day's open book and payment account; then days X to `to` (yesterday by default) run forward as normal chained runs; `to` = X is a replay of day X (G4). X ≤ `to` ≤ yesterday; day X is a first run unless X−1 has a current run, with an optional `backfillFrom`. One job per rule (B21): the scheduled run waits, and a catch-up asked while a job runs is refused. A stop resumes nothing, an `incomplete` day stops it, no alert for a closed period, no depth limit | Gives a new rule its past days, or fills days no run covered, in one job | OPT, **kept** (V1) | M | Past days only come back one replay at a time (G4), which then needs its own action, each rewinding from head: about 4.5 h of rewinds for 90 days at 1M tx/day instead of ~6 min, estimated. A new rule starts at its first run |

## 8. Configuration knobs

Every knob a customer or an operator can set, with the feature it belongs to. It inherits that
feature's interest unless the row says otherwise. The lookup group size and the read retries are
engine constants (B20), not knobs, and so are the age buckets (C7).

| Knob | Level | Where | Feature | Interest | If removed |
|---|---|---|---|---|---|
| `psp.ledger`, `product.ledger` | rule | ADR §6 | A1, E1 | NEC | — |
| `psp.key`, `product.key` | rule | ADR §6 | A1 | NEC | — |
| `psp.state` (field + `pending`, `final`, `failed`), `product.state` (field + `final`) | rule | ADR §6 | A3 | NEC | — |
| `holds[].prefix`, `holds[].openSign` | rule | ADR §6, dec. 11 | A4 | NEC | — |
| `holds[].businessId` | rule | ADR §6, dec. 18 | A5 | NEC | — |
| `psp.paymentAccount` | rule | ADR dec. 17 | A6 | NEC | — |
| `psp.merchantRef` | rule | ADR §6 | A8 | OPT, **kept** | Pairing gone |
| `psp.movementKeys` | rule | ADR dec. 23 | A9/E13 | OPT, **on hold** | Conversions and order fills leave a P1 residual on both sides of the book (`formancepayments`) |
| `product.grace`, `psp.grace` | rule | ADR dec. 16, 20 | C3 | NEC | One grace would do, but loses the asymmetry |
| `psp.maxAge`, `product.maxAge` | rule | ADR §6, dec. 21 | C6 | **Removed with C6** | `stale_holds` covers holds held too long |
| `buckets` | rule | ADR §6 | C7 | **Removed with C7's simplification** | The buckets are an engine constant; other buckets come from `ageDays` |
| `backfillFrom` | rule, and the restart and catch-up actions (and a replay past the retention) | ADR §7 items 6, 7, dec. 9, 26; RD §2 | B17, D12, G10 | NEC (a default exists) | A date in the rule's timezone; on or after the compared day, no seed. A restart's value past the next run's cut is rejected. A restart without it is refused only when the carried file failed its check |
| `retention` | rule | ADR §7 item 4 | G2 | **Removed with G2** | `--lettering-retention` applies to every rule |
| `periodType` (`daily`/`weekly`/`monthly`), timezone, cut-off | rule | ADR §6, dec. 8 | I1/I4 | NEC (daily, tz, cut-off); OPT (weekly/monthly) | — |
| `asset` (`"*"` fan-out) | rule | ADR §6 | A12 | NEC | — |
| `severity` | rule | RD §3 | I1 (reused) | NEC (existing) | — |
| `topK` | rule | ADR dec. 21 | E14 | **Removed with E14** | The manifest lists no items; the files hold them |
| Restart (optional `backfillFrom`) | operator action | ADR dec. 26; RD §2 | D12 | NEC | — |
| Catch-up (X, optional `to` and `backfillFrom`) | API action | ADR §7 item 7 | G10, G4 | OPT, **kept** (V1) | Past days come back one replay at a time (G4), through an action of its own |
| Storage: the product ledger's backup destination, recon's own credentials, the driver (`s3`/`azure`/`file`) | operator | ADR §7 item 3 | E1 | NEC | — |
| `--lettering-retention` | operator | ADR §7 items 4, 8, dec. 4 | G1 | NEC (default 90 d) | Fixed at 90 d; the lifecycle rule must match it |
| `anchorRetention` | rule | ADR dec. 4, 14 | G3 | **Removed with G3** | Goes with the anchors |
| `--lettering-read-ranges` | operator | ADR §7 item 8, dec. 13 | B6 | OPT | K fixed at 8 |
| `--lettering-max-concurrent-reads` | operator | ADR §7 item 8, dec. 13 | B7 | OPT | No cap |
| `--lettering-watch-interval` | operator | ADR dec. 25 | B11/B12 | **Dropped** (B12 deferred) | No watch in V1: B11 is deferred too |

## 9. Periods, aggregation and alerting

| # | Feature | Where | Description | Why | Interest | Cost | If removed |
|---|---|---|---|---|---|---|---|
| I1 | One aggregate alert per (rule, fingerprint, period), existing alert-period model, daily schedule by default | ADR §6, dec. 8 | Reuses the existing alerting | Never one alert per payment | NEC | S | No notification (reuses existing infra) |
| I2 | Alert trigger: an open break; never the net; `incomplete` → engine-error alert | ADR §6, dec. 16; RD §4 | Opening rule | Pending items and offsetting breaks move the net | NEC | S | Noisy or missed alerts |
| I3 | Alert evidence = the full rendered statement (bridge, open items, books, the breaks to act on, lookup hints) | ADR §6; RD §5, §10 "The statement" | A text *état de rapprochement* in the alert | Controller-readable | **Simplified** (OPT): structured evidence, no text rendering | M | The evidence is the day list (I6), whose latest entry is the headline, and the latest day's `statement`, `books` and `paymentAccounts` blocks as JSON, with no list of breaks; the UI renders the statement and pages the breaks from the API. Saves a server-side renderer and its golden test |
| I4 | Weekly/monthly periods for lettering rules (the monthly alert opened by the first failing daily run) | ADR §6, §7 item 5, dec. 8 | Reuses `periodType` | Monthly close | OPT | S | Daily-only lettering rules in V1 |
| I5 | `period.json` summary (one entry per day from the daily manifests, gaps, expired links, never rewritten, kept like an anchor) | ADR §7 item 5 | A file of the period's last run | A period view that outlives the daily files | **Removed from V1** | M | Saves a file format, "last run of the period" detection, the anchor-like retention and the self-reference rule (no `manifestSha256` for the last day). The same view is a query over the daily manifests. Depends on G3 for its retention |
| I6 | Alert rebuild: every tick rebuilds the open period's alert from the current run of each of its days (idempotent) | ADR §7 items 2, 5; RD §5 | A period closes once the next period's first day has a current run; a closed alert is never rebuilt. The day list carries each day's verdict, counts, net, gross and link; its latest entry is the headline. No gap state: a day with no complete run is not listed | The alert is derived from the manifests, never stored beside them, so a job that stops after its manifest loses nothing | NEC | S | An alert a stopped job never updated stays stale, and a replay in the open period never shows |

---

## Counts

- **Features:** 110 numbered rows in §1–§7 and §9. §8 has 25 knob rows: 19 live, 6 removed; they
  map to those rows and are not counted again.
- **NEC:** 53, E21 included.
- **OPT:** 31. Counted here: the "OPT, kept" rows, the "OPT, on hold" rows (A9, E13) and the
  "Simplified" rows (C7, E10, I3). Kept by decision: A8, C8, C10, D5, D6, E6, E8, E9, F2, F4, F5,
  F9, and G10 (kept in V1). Of the 31, 3 are doc-only, proposed, or already outside V1: A14, A15,
  G7.
- **The owner reviewed the 27 optional features in V1 one by one on 2026-10-01:** 25 stay as
  they are (E10 and I3 in their simplified form), C7 is simplified to fixed buckets, and E7 is
  removed. The 26 that remain in V1 are A8, B3, B6, B7, B20, C7, C8, C10, C12, D2, D5, D6, D7, E6,
  E8, E9, E10, E22 (frozen), F2, F4, F5, F9, G4, G10, I3 and I4.
- **The owner reviewed the 53 necessary features on 2026-10-01 and 2026-10-02, in 14 groups:**
  all stay. A13 now marks each booking convention required or recommended; E12 stays necessary,
  pending the Connectivity review of the payment account (DD §2).
- **Removed, deferred or replaced:** 26, B11, B12, B13, B14, B16, B18, C4, C6, D8, D9, D10, D11, E7,
  E14, E15, E16, E17, F3, F10, G2, G3, G5, G6, G8, G9 and I5 (see "Decisions taken").

## Candidates to remove

Ranked by complexity saved × low loss. Tiers are coarse, and the order within a tier is a
judgement.

**Tier 1: large saving, low loss.**

1. **B12 + G8, incremental watch job and slices.** L saved: a new job type, slices in another
   ledger's bucket, the manifest `watch` block, slice retention. Decided (deferred, dec. 25): the
   flag is dropped, and the full read is deferred too (item 16).
2. **G5 + G3 (+ per-file `expiresAt`), replay from stored stock and monthly anchors.** M/L saved.
   An old-day replay falls back to the rewind from head (G4), which takes minutes and is rare.
   `anchorRetention` goes too. Decided (removed, dec. 4, 14).
3. **I5, `period.json`.** M saved. A query over the daily manifests gives the same view. With G3
   gone, it would need its own retention anyway. Decided (removed, ADR §7 item 5).
4. **D10 + E17 + D11, re-seed, `diagnostic.json`, transient/structural kinds.** M/L saved. Recovery
   becomes "fix, then restart the rule as a first run" (D12, B17). Diagnose from
   `incomplete.detail` and the engine logs. Decided (D10 replaced by D12, E17 and D11 removed;
   dec. 26).
5. **D9, acceptance.** M saved, including an acceptance store and API that are not designed. A
   known break keeps the alert open until it is booked, which matches "fix by booking, no
   write-off". Decided (removed, ADR §6).
6. **B14, the synchronous aggregate capture at the tick.** M saved, plus a capture kind and one of
   L7's two reasons. It only yields an inexact "now" figure about 20 s early. Decided (removed,
   ADR §7 items 1–2).
7. **E13 + A9, the debit book and `psp.movementKeys`.** M saved, plus +31 % on the PSP flow read.
   Payouts and fees are outside payment lettering. Keep the credit book (E12). Undecided, on hold
   for the Connectivity review: `formancepayments` conversions and order fills need A9 on both
   sides of the book (DD §2).
8. **B16, the resumable job.** M saved. A run's reads take about 20 s; restart it from the
   beginning with a new `runId`. Decided (removed, ADR §7 item 2).

**Tier 2: small or medium saving, near-zero loss.**

9. **E16, file parts.** A single gzip per file is fine at 74–142 MB. Decided (removed, RD §8).
10. **B13, the purge consistency check.** The rewind repairs a touched hold, and continuity catches
    an untouched one on a chained run. Decided (removed, DD §4).
11. **C4 `firstSide`, D8 `previousClass`, E10 `flowGross`/`offsetting`, E15 `execution`/`timingsMs`,
    F3 `logSha256`.** Each is S, and none is read by any check or trigger. Decided (removed, E10
    simplified; dec. 19, 21, ADR §5, §7 items 7–8): all go but `flowGross`, which the alert shows
    next to each day's net.
12. **G2, recon's own expiry sweep.** Require the storage lifecycle rule instead. Decided (removed;
    dec. 4, ADR §7 items 4, 8): the retention becomes the deployment setting
    `--lettering-retention`, and the rule loses its `retention`.
13. **B18, G6, G7, A14, F10.** Doc-only mentions or options already outside V1: prune them from the
    docs. Decided (removed: B18, G6, F10; A14 and G7 kept).
14. **G9, a generic result store for `stale_holds`.** Defer the generalisation. Decided (deferred).
15. **F4, `stored_file_mismatch`, and F5, the byte-identical replay guarantee.** Keep deterministic
    row order, and drop the "replay proves itself" contract and its 4 conditions. Decided (both kept).

**Tier 3: medium saving, a real but acceptable loss (owner's call).**

16. **B11, the metadata watch altogether.** ~95 % of the run's time and the only log read. The
    convention loses its monitor, but continuity, the payment-account book and the residual still
    catch the mutations that change today's result. It becomes unnecessary with L8. This is the
    biggest runtime lever, and the one with the most design weight behind it. Decided (deferred
    until L8, dec. 25): a run reads no range of logs.
17. **E8 + E9 + C10 + D5, the bridge lines, residual, `carriedOutside`, `letteredOther` and the
    open-items identity.** A statement a DuckDB query can rebuild. The residual and D5 are engine
    self-checks; D4 remains the completeness proof. Decided (kept).
18. **E6, the duplicated breaks file**, together with D6, lifecycle, and C8, `cleared` rows. This
    only holds if the alert can live without new-vs-persisting. Decided (kept).
19. **I3, the full rendered statement in the alert.** Send a minimal alert instead. Decided
    (simplified): the evidence is structured data, and the UI renders the statement.
20. **C6 `stuck`/`maxAge` and C7 buckets.** They overlap `stale_holds`. Decided (C6 removed, C7
    kept, then simplified to fixed buckets on 2026-10-01).
21. **A8, merchantRef pairing.** It is cheap, and the docs call it the single most useful field. Keep
    it unless the Connectivity mapping cannot provide it. Decided (kept).

**Gaps found while reading:**

- **`topK`:** removed, with E14. The manifest carries aggregates only, and lists of items come from
  the files, through the API (ADR §7 items 3 and 9).
- **`openedAt`:** closed. It is the opening transaction's `timestamp`, the business date, seen in
  a window recon read (a first run's seed included) and kept in the stock file; a hold opened
  before `backfillFrom` has a null `openedAt` and an age counted from it, a lower bound, on both
  ledgers
  (ADR §6, §7 item 6; RD §6). A purged hold re-funded restarts its volumes from zero, and its
  opening is seen in the window; on ledger `v3.0.0-beta.7` it keeps its account metadata (ADR
  §2.2).
- **E12, the credit book:** marked NEC. With `formancepayments` it closes on the payment events,
  which carry the key, but not on conversions and order fills without A9 (ADR §10 open question,
  DD §2). V1 must settle that review before the book ships.
