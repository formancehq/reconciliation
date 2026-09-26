# Transaction-level reconciliation — reading the result files

> ⏳ **Planned — design under evaluation.** Nothing on this page is implemented yet.
>
> **This page is the source of truth for the result files** of a transaction-level (lettering)
> rule, `schemaVersion` `lettering/1`: where they are, what every field means, and how to read
> them. The [design doc](./transaction-level-reconciliation.md) explains how the engine computes
> them, and [ADR-005](../prd/adr-005-transaction-level-reconciliation.md) records the decisions.
> When either disagrees with this page about a file or a field, this page wins and the other is
> fixed.

---

## 1. Reading a day in four steps

1. **Find the day's current run** (§2) and open its `manifest.json`.
2. **Read `verdict`** (§4). `incomplete` means no conclusion can be drawn: the manifest says why in
   `incomplete.reason`, and the run wrote no other file.
3. **Read the statement** (§5), which is the manifest's `statement`, `books` and `triage` blocks,
   and the text the alert carries. The bridge explains the day's net difference, the open items
   give the running total still unmatched, and the triage says what to do first.
4. **Open the detail** only for what the statement points at: `breaks.ndjson.gz` for the rows to
   act on, `flow.ndjson.gz` and `stock.ndjson.gz` for the complete picture, and
   `unclassified.ndjson.gz` for the transactions the rule could not classify.

| Question | Where to look |
|---|---|
| Is the day reconciled? | `manifest.json` → `verdict` |
| What must be done today? | `triage.breaks`, then `breaks.ndjson.gz` with `outcome = 'break'` and no `acceptedOn`, by `priority` |
| What turns into a break soon? | `triage.pending`, with each item's `breakOn` |
| Why is the day's net not zero? | `statement.{asset}.lines` |
| How much is still unmatched, all days together? | `statement.{asset}.suspense.open`, which is the sum of `drift` in `carried.ndjson.gz` |
| What is still open on each ledger? | `books`, then `stock.ndjson.gz` |
| Was anything lettered outside matching (credit note, write-off)? | `books[].letteredOther`; in the stock, a `cleared` row with no `clearedBy` |
| Everything about invoice INV-12 | a filter on `businessId`, `holdId` or `merchantRef` in every file (§9) |
| Does the connector mapping follow the rule? | `counts.unclassified`, then `unclassified.ndjson.gz` |

## 2. Where the files are, and which run counts

```text
{backup bucket}/{bucketID}/reconciliation/rule={ruleId}/day={YYYY-MM-DD}/run={runId}/
  manifest.json           the run: rule, cuts, verdict, counts, statement, books, triage, files
  flow.ndjson.gz          one row per payment reference and asset: the window's, plus the ones carried in or looked up
  carried.ndjson.gz       the flow rows whose drift is not 0, handed to the next run
  stock.ndjson.gz         one row per open hold at the cut, plus the holds cleared since the previous run
  breaks.ndjson.gz        every break of both legs, open or resolved since the previous run
  unclassified.ndjson.gz  the transactions whose state is in none of the rule's sets
  period.json             weekly and monthly rules only, on the period's last run: the period summary
```

- **The bucket** is the backup destination of the rule's **product ledger**, under a prefix next to
  `backups/`. `{bucketID}` is that ledger's bucket.
- **Access.** Recon's API lists a run's files with a pre-signed URL for each. A deployment that owns
  the storage can also read the prefix directly. The `key=value` path segments let DuckDB, Spark or
  Athena read `rule`, `day` and `run` as columns.
- **An `incomplete` run writes its manifest only.** It writes no data file and is not a link in the
  chain, so a run that has data files is always complete.
- **The day's current run is its latest complete run.** A `runId` is `r-` followed by the run's
  start instant in UTC (`r-20260925T000004Z`), so run ids sort in time order. Replaying or retrying
  a day writes a new run, which replaces the earlier ones once it completes. A reader that globs the
  data files and keeps the latest run of each day gets exactly the current runs (§9).
- **Runs are chained.** `previousRun` names the current run of the most recent earlier day that has
  one, with its day and its manifest's SHA-256. When that day is not the day before (a day missed or
  incomplete), this run's window starts at that run's cut and covers every day since. The statement
  then says "window since …".
- **Expiry.** Each file has its own `expiresAt` in the manifest's `files`. The daily files are kept
  for the rule's `retention`, 90 days by default.
  - The last run of each month keeps its manifest, stock file and carried file for
    `anchorRetention` (13 months by default).
  - The last run of a period keeps its manifest and `period.json` for as long.
  - An expired day can be recomputed from the ledgers' permanent logs, by the same engine version
    (`engine` in the manifest), replaying forward from the nearest anchor, whose stock and carried
    files seed the chain.

## 3. Conventions

- **Format.** Gzipped NDJSON, one JSON object per line. Each file has a JSON Schema under the
  manifest's `schemaVersion`.
- **Amounts** are integers in minor units, written as strings, always next to their `asset`:
  `"100000"` in `EUR/2` is 1,000.00.
- **Signs.** Three conventions, each attached to a fixed set of fields:
  - **`drift`**, and every flow figure derived from it, reads **`psp − product`**. Positive means
    the PSP holds more than the product applied, which is cash not applied yet. Negative means the
    product applied more than the PSP finalised.
  - **`balance`** on a stock row is the ledger's balance, as the ledger shows it.
  - **The books and a stock break's `amount`** read in the hold's **open direction**. Positive
    means open as its prefix expects, such as an unpaid invoice. Negative means the wrong sign,
    such as an over-applied invoice. For a prefix whose `openSign` is `negative`, that is
    `−balance`.
- **Enumerations** (class, outcome, lifecycle, verdict) are lower snake_case. Only the statement
  prints the verdict in capitals.
- **Days** are `YYYY-MM-DD` in the rule's timezone. **Instants** are RFC 3339 in UTC, except the
  period's `cutoff`, which keeps the rule's offset.
- **Identifiers** have the same name in every file:
  - `ref`, the PSP payment reference;
  - `businessId`, the business id of the hold's kind (`invoice_no`, `refund_no`…);
  - `hold`, the hold's address;
  - `holdId`, the address after its prefix, which equals the business id with the recommended
    booking;
  - `merchantRef`, the merchant reference the PSP reports;
  - `tx`, a ledger transaction id.
- **Ledger ids** (`tx`, log ids) are JSON numbers. They are uint64 and stay below 2^53 in practice;
  a JavaScript reader that must be exact beyond that parses them as big integers.
- **Per side.** A figure split per side is keyed `psp` or `product`, never by the ledger's name.
- **Checksums** are SHA-256 in lowercase hex, in fields whose name ends in `sha256` or `Sha256`.
  `breakId` is an identifier, not a checksum: 16 hex characters of a hash.

**Four fields give a row's status**, and each answers one question:

| Field | Question | Values | On |
|---|---|---|---|
| `outcome` | Must someone act? | `ok` (nothing to do), `pending` (not a break yet), `break`, `warning` (an unclassified transaction) | every row |
| `class` | What happened? | per leg, §6 | flow, carried, stock and break rows |
| `priority` | How urgent is it? | 1 to 4, §6 | break rows |
| `lifecycle` | What changed since the previous run? | `new`, `persisting`, then `cleared` for a hold or `resolved` for a break | stock and break rows |

The rule's `severity` (`low` to `critical`) grades the alert only, and is unrelated to `outcome`
and `priority`.

## 4. The verdict

The verdict is evaluated in this order, and the first condition that holds wins:

| `verdict` | Condition | What it tells the controller |
|---|---|---|
| `incomplete` | A required index is missing, a log range came back shorter than `hi − lo`, a continuity identity fails, the bridge's residual is not 0, a hold open at the cut is missing from the listing without having been purged, or a stored file differs from the SHA-256 in its signed capture. `incomplete.reason` says which: `missing_index`, `short_log_range`, `continuity`, `residual`, `purge_check`, `stored_file_mismatch` | No conclusion can be drawn. The read is incomplete, or a hold moved in a transaction that carries neither the key nor a business id. The run writes no data file and opens the engine-error alert, never a green one |
| `breaks` | At least one open break | The breaks, by priority, new or persisting |
| `reconciled_with_warnings` | No break, but at least one unclassified transaction, or a key, state, business-id or merchant-reference metadata changed after insertion (`anomalies`) | Money moved that the rule does not classify, or a transaction's identity changed after the fact: the rule, the connector mapping or the booking needs attention |
| `reconciled_with_pending` | No break and no warning, but unapplied payments within `product.grace` or applications within `psp.grace` | "OK for now". Each pending item comes with the day it becomes a break |
| `reconciled` | None of the above | The only green state |

**What opens the alert:** at least one open break that is not accepted. An accepted break stays a
break, in the files and in the verdict, but no longer opens the alert. A resolved break does not
open it, and neither does the net alone, since pending items move the net without being breaks and
offsetting breaks net to zero. An `incomplete` run opens the engine-error alert instead.

## 5. The statement

A net difference of 0 proves nothing: breaks of +1,000 € and −1,000 € net to zero. Every run
therefore answers with a reconciliation statement, the classic *état de rapprochement*. It has
three parts per asset, each closed by an identity that the engine checks.

### The bridge: why the window's net is what it is

```text
  PSP — finalised payments in the window                              A     (count)
− Product — applications in the window                                B     (count)
= Net difference                                                      A − B
  explained by:
    + unapplied payments of the window (within product.grace)         …     (n)   pending
    − applications awaiting a final PSP state (within psp.grace)      …     (n)   pending
    − orphan applications                                       P1    …     (n)
    − reversed after application                                P1    …     (n)
    ± under / over applications                                 P2    …     (n)
    ± matched today, entered the join on an earlier day               …     (n)
  = unexplained residual                                  must be 0, else incomplete
Carried from earlier days, outside the window's net (one line per class still open):
    unapplied payments, applications awaiting a final state, under / over, orphan, reversed
Gross open flow breaks: Σ|drift| = …    Offsetting: yes/no
```

- **A** is the change in finalised PSP amounts within the window: payments finalised in it, minus
  payments that failed in it after being finalised. It is read on `psp.paymentAccount`.
- **B** comes from the **product books**, not from the flow rows. It is what the window's
  transactions taking part in matching lettered on the business holds, which is `lettered −
  letteredOther`, summed over the product prefixes.
- **Each line** is `SUM(impact)` over the flow rows with a non-zero `impact`, grouped by `class`,
  `outcome` and `firstSeen < day`, so the lines add up to the rows' own `A − B`.
  - With `product.grace: 0`, an unapplied payment of the window is a break and gets its own line,
    with P3.
  - The earlier-day line holds `matched` rows only. Its sign says which side caught up: negative
    for applications of earlier payments, positive for finalisations of earlier applications. An
    earlier-day under- or over-application goes on the under / over line.
- **The residual** is B from the rows minus B from the books. A residual other than 0 means a
  lettering the join did not attribute to a reference, and the run is `incomplete`.
- **The carried lines** are `SUM(drift)` of the rows still open from earlier days. Their `impact`
  is 0, so they sit outside the window's net.
- **The gross** covers every open flow break, from the window or carried in, so it is not on the
  same scope as the net. `offsetting` says only that open flow breaks of both signs exist, which is
  when a net can hide them.

### The open items: what is still unmatched, all days together

```text
  Open items at the previous cut (Σ drift of its carried file)        …     (n)
+ Net difference of the window                                        …
± Entered through a lookup                                            …
= Open items at this cut (Σ drift of carried.ndjson.gz)               …     (n)   ✓
```

- **`fromLookups`** is the drift a reference already had when neither the carried items nor the
  window held it. An example is a payment finalised before `backfillFrom` whose application arrives
  now. It is 0 in steady state.
- **`openPrev`** and `countPrev` are the previous current run's `open` and `count`.
- **The identity** `open = openPrev + net + fromLookups` fails when a carried item is lost or
  counted twice between two runs. The run is then `incomplete` (`continuity`).
- **`open` is the running balance of the reconciliation**: payments not applied yet, applications
  not finalised yet, and amounts that disagree, all days together.

### The open books: what is open on each ledger

For each side, hold prefix and asset, in the open direction:

```text
open = openPrev + opened − lettered
```

- `openPrev` is the previous run's stored stock. `opened` and `lettered` are the window's hold
  movements, and `open` is the stock rewound to the cut.
- A lost event, or a hold moved in a transaction that carries neither the key nor a business id,
  breaks the identity. The run is then `incomplete` (`continuity`).
- **`letteredOther`** is the part of `lettered` done by transactions that take no part in
  matching. Most carry no PSP reference: a credit note, a write-off, a manual lettering. The rest
  carry one but have a state in none of the rule's sets, and are also in the unclassified file. It
  is not an error, but it is money that cleared a hold with no matched payment behind it, so it
  stays visible. A transaction without a PSP reference must carry the hold's business id; otherwise
  the flow read misses it and continuity fails.
- On the PSP side, `letteredOther` holds only the letterings of unclassified events.
- The age buckets count the open holds by age.

### Around the three parts

- **Triage**, in priority order, then by amount: each open break new or persisting, with its
  acceptance, then the pending items with their `breakOn`, then the breaks resolved since the
  previous run.
- **A merchant reference.** When the rule names `psp.merchantRef`, every unapplied payment is
  paired with the open business hold it names.
- **Warnings**: unclassified transactions per side and state value, and the transactions whose
  identifying metadata changed after insertion. They make the verdict `reconciled_with_warnings` at
  best.

## 6. File reference

### `manifest.json`

| Field | Meaning |
|---|---|
| `schemaVersion` | `lettering/1` |
| `engine` | The version of recon that produced the run. A replay reproduces the files only with the same one |
| `rule` | The whole rule as evaluated: `id`, `version`, `sha256` and every parameter, including each side's `key`, `state` sets, `grace`, `maxAge`, `holds` (`prefix`, `openSign`, `businessId` on the product side), `psp.paymentAccount` and `psp.merchantRef`. `buckets` are the age buckets' upper bounds |
| `runId` | `r-{UTC start instant}`; run ids sort in time order |
| `previousRun` | `runId`, `day` and `manifestSha256` of the current run of the most recent earlier day that has one. Absent on the first run |
| `period` | `type`, `day`, `cutoff` (with the rule's offset) and `tz` |
| `startedAt`, `finishedAt`, `timingsMs` | When the run ran and how long each phase took: `cut`, `flow`, `lookup`, `stock`, `watch`, `join`, `write` |
| `cuts` | One entry per side: `ledger`, the log window `(logFrom, logTo]` and the transaction window `(txFrom, txTo]`. `logTo` is the cut `S` and `txTo` is `T`. Also `logHead`, the head the run read up to, and `logSha256`, which identifies the log at `S` |
| `execution` | `readRanges`, `maxConcurrentReads`, `stockFrom` (`live` for a daily run; `daily`, `anchor` or `head` for a replay), and per side `rewindLogs`, `lookups` (references read by key) and `watchLogs` (logs read in `(head_prev, S]` for the metadata check) |
| `verdict` | §4 |
| `incomplete` | Only when `verdict` is `incomplete`: `reason` and a human-readable `detail` |
| `counts.flow` | Flow rows per class; adds up to the flow file's row count |
| `counts.flowOutcome` | Flow rows per outcome |
| `counts.stock` | Stock rows per side and class |
| `counts.breaks` | `new`, `persisting`, `resolved`, `accepted`, and open breaks `openByLeg` and `openByPriority` |
| `counts.unclassified` | Unclassified transactions per side |
| `anomalies.key_metadata_mutated` | The transactions (`side`, `tx`) whose key, state, business-id or merchant-reference metadata was changed or deleted after insertion, seen since the previous run's head. Empty in a sound booking |
| `statement.{asset}` | The bridge: `psp` and `product` (`amount`, `count`), `net`, `lines` (`class`, `outcome`, `earlierDay`, `amount`, `count`, `top` references), `residual`, `carriedOutside` (`class`, `outcome`, `amount` as `SUM(drift)`, `count`, `top`), `flowGross`, `offsetting`. The open items: `suspense` (`openPrev`, `countPrev`, `fromLookups`, `open`, `count`, `continuityOk`). And `unclassified` per side and state |
| `books` | One entry per side, prefix and asset: `openSign`, `openPrev`, `opened`, `lettered`, `letteredOther`, `open`, `count`, `buckets`, `continuityOk` |
| `triage` | What the statement names, so it is rendered from the manifest alone. `topK`; `breaks`, the top-K open breaks in priority order, each with `breakId`, `priority`, `class`, `lifecycle`, its key (`ref`, or `side` and `hold`), `asset`, `amount`, and its context (`holdIds`, the holds its applications lettered; `firstSeen`; `ageDays`; `acceptedOn`); `pending`, each with `ref`, `class`, `asset`, `amount`, `breakOn` and `pairedHold` or `holdIds`; `resolved`, the breaks resolved since the previous run, each with `breakId`, `class`, its key, `asset`, `amount` and `clearedBy` |
| `files` | One entry per file: `name`, `rows`, `sha256`, `expiresAt`, and `part` when the file comes in parts |
| `anchor` | `true` on the last run of a month |
| `expiresAt` | The manifest's own expiry |

### `flow.ndjson.gz`

One row per payment reference and asset. That covers the window's references, the ones carried in
from the previous run and the ones read by key.

| Field | Meaning |
|---|---|
| `ref`, `asset` | The reference and its asset (the unique key) |
| `class`, `outcome` | Below |
| `pspAmount` | The finalised amount. It is 0 while the reference is not finalised, and 0 again once the PSP reports `failed` after `final` |
| `productAmount` | The sum of the reference's applications |
| `drift` | `pspAmount − productAmount` |
| `impact` | The change in `drift` within the window: finalised amount gained, or lost to a failure, minus applications booked in the window. The bridge is `SUM(impact)`. A reference carried in with nothing new today has `impact` 0 |
| `firstSeen` | The day the reference entered the join: its first final PSP state or its first application. Absent on `in_progress` |
| `breakOn` | On a `pending` row, the day it becomes a break: `firstSeen` plus the lagging side's `grace`. It stays on the row once the break is open |
| `firstSide` | `psp` or `product`: which came first, the PSP's terminal state or the first application. For analysis only |
| `merchantRef`, `pairedHold` | When the PSP reports a merchant reference, and the open hold it names |
| `psp` | The reference's PSP transactions: `tx`, `state`, `insertedAt`, plus `amount` on a `final` event, which is its net posting on `psp.paymentAccount` and what `pspAmount` sums. A `pending` or `failed` event carries `holdAmount` instead: its hold movement, in absolute value |
| `product` | The reference's applications: `tx`, `businessId`, `holdId`, `amount` (the net posting on the hold, in the settling direction), `insertedAt`. A transaction that letters two holds is listed once per hold |

A reference carried in or read by key keeps the transactions of its earlier days, so a row always
shows its whole history.

**Flow classes:**

| `class` | Meaning | `outcome` (`priority`) |
|---|---|---|
| `matched` | PSP `final`, and applications summing to the same amount | `ok` |
| `under_applied` / `over_applied` | PSP `final`, and applications summing to less or to more. There is no tolerance: an unbooked fee is a break | `break` (2) |
| `unapplied_payment` | PSP `final`, no application yet | `pending` within `product.grace`, then `break` (3) |
| `in_progress` | PSP `pending` only, no application. Its hold is in the PSP stock | `ok` |
| `failed` | PSP `failed`, never applied | `ok` |
| `applied_before_final` | An application whose reference the PSP has not finalised yet (`pending`, or not seen at all) | `pending` within `psp.grace`, then becomes `orphan_application` |
| `orphan_application` | An application whose reference is still not final past `psp.grace`, or was already `failed` | `break` (**1**) |
| `reversed_after_application` | The PSP reports `failed` after the product applied the reference. A refund or chargeback is not this: it has its own reference | `break` (**1**) |

### `carried.ndjson.gz`

The flow rows whose `drift` is not 0, without `impact`, which describes only their own day's
window. The next run joins them with its window. The sum of their `drift` is
`statement.{asset}.suspense.open`, and their count is `suspense.count`.

### `stock.ndjson.gz`

One row per hold open at the cut, plus one row per hold cleared since the previous run.

| Field | Meaning |
|---|---|
| `side`, `hold`, `asset` | The unique key |
| `prefix`, `holdId`, `openSign` | The rule's hold kind and the id after its prefix |
| `balance` | The ledger's balance at the cut, signed as the ledger shows it |
| `class`, `outcome` | Below |
| `lifecycle` | `new`, `persisting` or `cleared`, against the previous run |
| `openedAt`, `ageDays`, `bucket` | When the hold opened, its age at the cut, and its age bucket |
| `previousBalance`, `clearedAt`, `clearedBy` | On a cleared hold: its balance at the previous cut, when it was lettered, and the `ref` that lettered it. No `clearedBy` means it was lettered without a PSP reference |
| `pairedRef` | The unapplied payment whose `merchantRef` names this hold |

**Stock classes:**

| `class` | Meaning | `outcome` (`priority`) |
|---|---|---|
| `open` | Open in the direction its prefix expects | `ok` |
| `wrong_sign` | The balance has the sign opposite `openSign`: an over-application, or a skipped state | `break` (4) |
| `stuck` | Open past the side's `maxAge`, a rule parameter; without it, no hold is ever `stuck` | `break` (4) |
| `cleared` | Open at the previous cut, lettered since: listed once, with a balance of 0 | `ok` |

The two stock books are aged, never joined to each other: an unpaid invoice has no PSP counterpart
by design.

### `breaks.ndjson.gz`

Every open break of both legs, plus the breaks resolved since the previous run. A break row is the
complete row of its flow or stock file, so a reader never joins files to show a break, plus these
fields:

| Field | Meaning |
|---|---|
| `breakId` | A hash of rule, leg, key and asset, **not the class**. The key is `ref` for a flow break, and `side` + `hold` for a stock break. It stays the same from day to day, and comments, assignments and acceptances attach to it |
| `leg` | `flow` or `stock` |
| `priority` | 1 to 4, below |
| `lifecycle` | `new`, `persisting` or `resolved` |
| `openedOn`, `resolvedOn` | The day the break opened, and the day it was resolved |
| `amount` | Signed. On a flow break it is the `drift` (`psp − product`); on a stock break, the balance in the open direction |
| `previousClass` | On the day the class changes only |
| `acceptedOn` | The day of the acceptance, while it holds |

| `priority` | Classes | Why |
|---|---|---|
| 1 | `orphan_application`, `reversed_after_application` | The product booked money with no cash behind it |
| 2 | `under_applied`, `over_applied` | The amounts disagree |
| 3 | `unapplied_payment` past `product.grace` | Cash received and still not applied |
| 4 | `stuck`, `wrong_sign` | An open hold to investigate |

`class`, `outcome`, `priority`, `lifecycle` and `amount` are the break's. A resolved break keeps
the class, priority and amount it had when it was last open, next to the row as it stands now, and
its `outcome` is `ok`. So `outcome = 'break'` counts the open breaks in every file.

### `unclassified.ndjson.gz`

One row per transaction and asset whose state is in none of the rule's sets: `side`, `tx`, `ref`,
`asset`, `outcome` (`warning`), `state`, `amount`, `insertedAt`. `amount` is its net posting, in
absolute value, on the accounts the rule reads for that side: `psp.paymentAccount` and the hold
prefixes on the PSP side, the hold prefixes on the product side. Such a transaction takes no part in
matching.
A typical cause is a connector booking refunds on the original payment's id ([connector
checklist](./transaction-level-reconciliation.md#mapping-a-connector-for-reconciliation), row 6).

### `period.json`

Written once, by the last run of a weekly or monthly period. It is built from the period's daily
manifests and never rewritten afterwards.

| Field | Meaning |
|---|---|
| `schemaVersion`, `rule` (`id`, `version`) | As in the manifest |
| `period` | `type`, `from`, `to`, `tz` |
| `days[]` | One entry per day. For a completed day: `day`, `runId`, `manifestSha256`, `verdict`, `counts` (as in the manifest: `flow`, `flowOutcome`, `stock`, `breaks`), `statement` per asset (`net`, `flowGross`, `suspense`), the run's `path` and its `expiresAt`. For a day with no complete run: `day` and `gap` (`no_run` or `incomplete`) |

A break still open at the period's end keeps the day it first appeared. A break resolved after the
period closed shows up in the next period. A day replayed later changes its own files, not a
closed summary.

## 7. How rows move from day to day

- **A pending flow row becomes a break on its `breakOn` day.** `unapplied_payment` goes from
  `pending` to `break` (P3). `applied_before_final` becomes `orphan_application` (P1).
- **A reference stays carried while its `drift` is not 0.** It leaves the carried file only when a
  booking brings the drift to 0: the PSP finalises it, or the product books the difference or
  reverses the application. There is no write-off state:
  a systematic difference, such as a fee never booked on every payment, is fixed by booking it.
  Until then, accepting the breaks keeps them out of the alert.
- **A hold** becomes `stuck` at `maxAge`, and is listed once as `cleared` by the run whose window
  lettered it.
- **A break keeps its `breakId` for life.**
  - When its class changes (an orphan later reported `failed`, an unapplied payment later applied
    short), it stays the same break, `persisting`, with `previousClass` on that day.
  - An acceptance records the class and amount it accepted, and lapses when either changes.
  - A resolved break that opens again is `new` again, with a new `openedOn`, and keeps its history.

## 8. Rules a reader can rely on

- **Every data file is written on every complete run**, even with no row, so a glob never breaks
  on a quiet day. The manifest gives each file's row count.
- **A file may come in parts.** Past a row threshold, `flow.ndjson.gz` becomes
  `flow-00000.ndjson.gz`, `flow-00001.ndjson.gz` and so on, each listed in `files` with its `part`.
  A reader that follows the manifest, or globs `flow*.ndjson.gz`, needs no change.
- **The same cut gives the same bytes.** Keys follow the order of the file's JSON Schema, rows the
  order below, and gzip uses a fixed level with no name and no timestamp. A replay therefore
  reproduces every data file's SHA-256, provided four things hold:
  - the same engine version;
  - the same rule version;
  - the same previous run, whose carried and stock files seed the day;
  - no key or state metadata changed since the original run (`key_metadata_mutated`), because the
    ledger serves the current metadata.

  Only the manifest differs, through its instants and timings.
- **Compatibility.** A new optional field may appear within `lettering/1`, so a reader ignores
  fields it does not know. Removing or renaming a field, changing its meaning, or adding a value to
  an enumeration changes `schemaVersion`.

| File | Unique key | Order |
|---|---|---|
| `flow` | `ref`, `asset` | `ref`, `asset` |
| `carried` | `ref`, `asset` | `ref`, `asset` |
| `stock` | `side`, `hold`, `asset` | `side`, `hold`, `asset` |
| `breaks` | `breakId` | open before resolved, then `priority`, then `\|amount\|` descending |
| `unclassified` | `side`, `tx`, `asset` | `side`, `tx`, `asset` |

## 9. Queries

**The query pack.** [`tools/lettering-duckdb`](../../tools/lettering-duckdb/README.md) holds
ready-made queries, one file per question:
- the daily flow and the day's reconciled payments;
- the bridge;
- the breaks to act on;
- the pending items;
- everything about one invoice;
- the open items and books day after day;
- stock ageing;
- letterings outside matching;
- one row per application or PSP event.

It also holds `check`, which verifies a run's files against the rules of this page that the files
themselves can show, and `check-chain`, which verifies that one run chains onto the previous one.
They are plain SQL run by the DuckDB CLI, usable from any DuckDB client, and tested on the worked
example of §10. Its README lists them, with their variables.

A few standalone examples follow, on the worked example; the paths are relative to the rule's
prefix.

**The current run of each day.** Keep the latest run per day before anything else, with DuckDB. An
incomplete run has no data file, so it never shows up here; the pack decides from the manifests
instead, which also covers a day whose latest complete run has an empty flow file.

```sql
CREATE VIEW flow AS
SELECT * FROM read_json_auto('rule=psp-vs-billing/day=*/run=*/flow.ndjson.gz', hive_partitioning = true)
QUALIFY run = max(run) OVER (PARTITION BY day);
```

**Rebuild the bridge** from the flow file alone:

```sql
SELECT class, outcome, firstSeen < day AS earlierDay,
       sum(CAST(impact AS BIGINT)) AS amount, count(*) AS n
FROM flow
WHERE day = DATE '2026-09-24' AND impact <> '0'
GROUP BY ALL
ORDER BY amount DESC;
```

It returns `unapplied_payment` +80000, `under_applied` +5000, `applied_before_final` −30000 and
`matched` (earlier day) −70000. They add up to the net of −15000.

**Everything about invoice INV-12**, with jq, on one run's files:

```sh
gzip -dc flow.ndjson.gz stock.ndjson.gz breaks.ndjson.gz | jq -c 'select(.holdId == "INV-12"
  or .merchantRef == "INV-12" or any(.product[]?; .businessId == "INV-12"))'
```

## 10. Worked example: one day of output

A rule `psp-vs-billing` reconciles the PSP ledger `psp` with the product ledger `main`, for **24
September 2026**. The PSP ledger is fed by Connectivity's `formancepayments`.

- The cut-off is 23:59:59 Europe/Paris, and the run starts at 02:00.
- `product.grace` is 1 day and `psp.grace` 7 days.
- `maxAge` is 30 days on the product side, where invoices are paid by card at checkout, and 10 days
  on the PSP side.
- All figures are EUR, one asset `EUR/2`. The files carry minor units as strings (`"100000"` =
  1,000.00), and the statement shows major units.
- Ids and hashes are illustrative; the figures agree with each other across the files.

**What happened in the window.**

| Ref | PSP side | Product side | Class |
|---|---|---|---|
| PAY-42 | `payin.succeeded` 1,000.00 | applied to INV-7, 1,000.00 | `matched` |
| PAY-43 | `payin.succeeded` 1,000.00 | split: INV-8 600.00 + INV-10 400.00 | `matched` |
| PAY-40 | finalised on 23 Sep, carried in | applied to INV-5 today, 700.00 | `matched`, earlier day |
| PAY-44 | `payin.succeeded` 1,200.00 | applied to INV-11, 1,150.00 (a 50.00 fee never booked) | `under_applied`, break |
| PAY-45 | `payin.succeeded` 800.00 | not applied yet; its `merchant_ref` names INV-12 | `unapplied_payment`, pending until 25 Sep |
| PAY-39 | finalised on 20 Sep, still unapplied | — | `unapplied_payment`, past `product.grace`: break |
| PAY-46 | `payin.pending` 250.00 | — | `in_progress`: not a break, its hold is in the stock |
| PAY-99 | `payin.pending` only, a debit final in a few days | applied to INV-13, 300.00, before the PSP's event | `applied_before_final`, pending until 1 Oct |
| PAY-31 | `payin.refunded` 200.00, on the original payment id | — | `unclassified` (state in no set) |

Every payment finalised in the window went through `payin.pending` first, so its PSP hold opened
and was lettered on the same day.

**`manifest.json`**

```json
{
  "schemaVersion": "lettering/1",
  "engine": "reconciliation v1.4.0",
  "rule": {
    "id": "psp-vs-billing", "version": 7, "sha256": "4c1d…",
    "buckets": ["1d", "7d", "30d"], "retention": "90d", "anchorRetention": "13mo",
    "backfillFrom": "2026-08-01",
    "psp":     {"ledger": "psp",  "key": "payments.formance.com/payment-id",
                "state": {"field": "formance.com/observation.event-type", "pending": ["payin.pending"],
                          "final": ["payin.succeeded"], "failed": ["payin.compensate"]},
                "grace": "7d", "maxAge": "10d", "paymentAccount": "fpay:stripe:account:*:main",
                "merchantRef": "merchant_ref",
                "holds": [{"prefix": "fpay:stripe:payment:hold:pending:", "openSign": "positive"}]},
    "product": {"ledger": "main", "key": "psp_payment_ref",
                "state": {"field": "transition_kind", "final": ["to_final"]},
                "grace": "1d", "maxAge": "30d",
                "holds": [{"prefix": "main:hold:invoice:", "openSign": "negative", "businessId": "invoice_no"},
                          {"prefix": "main:hold:refund:",  "openSign": "positive", "businessId": "refund_no"}]}
  },
  "runId": "r-20260925T000004Z",
  "previousRun": {"runId": "r-20260924T000003Z", "day": "2026-09-23", "manifestSha256": "a3e0…"},
  "period": {"type": "daily", "day": "2026-09-24", "cutoff": "2026-09-24T23:59:59+02:00", "tz": "Europe/Paris"},
  "startedAt": "2026-09-25T00:00:04Z",
  "finishedAt": "2026-09-25T00:00:31Z",
  "timingsMs": {"cut": 420, "flow": 11240, "lookup": 0, "stock": 6810, "watch": 6500, "join": 900, "write": 1080},
  "cuts": [
    {"side": "psp",     "ledger": "psp",  "logFrom": 2411902, "logTo": 2640118, "txFrom": 1204000, "txTo": 1318500, "logHead": 2644328, "logSha256": "3f9a…"},
    {"side": "product", "ledger": "main", "logFrom": 1530010, "logTo": 1574300, "txFrom": 880400,  "txTo": 902750,  "logHead": 1576173, "logSha256": "b07c…"}
  ],
  "execution": {"readRanges": 8, "maxConcurrentReads": 16, "stockFrom": "live",
                "rewindLogs": {"psp": 4210, "product": 1873}, "lookups": {"psp": 0, "product": 0},
                "watchLogs": {"psp": 224111, "product": 42500}},
  "verdict": "breaks",
  "counts": {
    "flow": {"matched": 3, "under_applied": 1, "over_applied": 0, "unapplied_payment": 2,
             "applied_before_final": 1, "orphan_application": 0, "reversed_after_application": 0,
             "in_progress": 1, "failed": 0},
    "flowOutcome": {"ok": 4, "pending": 2, "break": 2},
    "stock": {"psp":     {"open": 2, "wrong_sign": 0, "stuck": 0, "cleared": 0},
              "product": {"open": 4, "wrong_sign": 1, "stuck": 1, "cleared": 1}},
    "breaks": {"new": 1, "persisting": 3, "resolved": 1, "accepted": 0,
               "openByLeg": {"flow": 2, "stock": 2}, "openByPriority": {"1": 0, "2": 1, "3": 1, "4": 2}},
    "unclassified": {"psp": 1, "product": 0}
  },
  "anomalies": {"key_metadata_mutated": []},
  "statement": {
    "EUR/2": {
      "psp":     {"amount": "400000", "count": 4},
      "product": {"amount": "415000", "count": 6},
      "net": "-15000",
      "lines": [
        {"class": "unapplied_payment",    "outcome": "pending", "earlierDay": false, "amount": "80000",  "count": 1, "top": ["PAY-45"]},
        {"class": "applied_before_final", "outcome": "pending", "earlierDay": false, "amount": "-30000", "count": 1, "top": ["PAY-99"]},
        {"class": "under_applied",        "outcome": "break",   "earlierDay": false, "amount": "5000",   "count": 1, "top": ["PAY-44"]},
        {"class": "matched",              "outcome": "ok",      "earlierDay": true,  "amount": "-70000", "count": 1, "top": ["PAY-40"]}
      ],
      "residual": "0",
      "carriedOutside": [{"class": "unapplied_payment", "outcome": "break", "amount": "50000", "count": 1, "top": ["PAY-39"]}],
      "flowGross": "55000",
      "offsetting": false,
      "suspense": {"openPrev": "120000", "countPrev": 2, "fromLookups": "0", "open": "105000", "count": 4, "continuityOk": true},
      "unclassified": [{"side": "psp", "state": "payin.refunded", "amount": "20000", "count": 1}]
    }
  },
  "books": [
    {"side": "psp",     "prefix": "fpay:stripe:payment:hold:pending:", "asset": "EUR/2", "openSign": "positive",
     "openPrev": "0",      "opened": "455000", "lettered": "400000", "letteredOther": "0", "open": "55000",  "count": 2,
     "buckets": {"0-1d": 2, "2-7d": 0, "8-30d": 0, ">30d": 0}, "continuityOk": true},
    {"side": "product", "prefix": "main:hold:invoice:",                "asset": "EUR/2", "openSign": "negative",
     "openPrev": "430000", "opened": "230000", "lettered": "415000", "letteredOther": "0", "open": "245000", "count": 5,
     "buckets": {"0-1d": 0, "2-7d": 2, "8-30d": 2, ">30d": 1}, "continuityOk": true},
    {"side": "product", "prefix": "main:hold:refund:",                 "asset": "EUR/2", "openSign": "positive",
     "openPrev": "0",      "opened": "20000",  "lettered": "0",      "letteredOther": "0", "open": "20000",  "count": 1,
     "buckets": {"0-1d": 1, "2-7d": 0, "8-30d": 0, ">30d": 0}, "continuityOk": true}
  ],
  "triage": {
    "topK": 10,
    "breaks": [
      {"breakId": "a93d02e6b7f1c448", "priority": 2, "class": "under_applied",     "lifecycle": "new",        "ref": "PAY-44", "asset": "EUR/2", "amount": "5000",   "holdIds": ["INV-11"]},
      {"breakId": "1c7f3a90d2e84b55", "priority": 3, "class": "unapplied_payment", "lifecycle": "persisting", "ref": "PAY-39", "asset": "EUR/2", "amount": "50000",  "firstSeen": "2026-09-20"},
      {"breakId": "d4f8a1c3e5b70926", "priority": 4, "class": "stuck",             "lifecycle": "persisting", "side": "product", "hold": "main:hold:invoice:INV-3",  "asset": "EUR/2", "amount": "120000", "ageDays": 41},
      {"breakId": "7b24e1f09c3d6a12", "priority": 4, "class": "wrong_sign",        "lifecycle": "persisting", "side": "product", "hold": "main:hold:invoice:INV-14", "asset": "EUR/2", "amount": "-10000", "ageDays": 6}
    ],
    "pending": [
      {"ref": "PAY-45", "class": "unapplied_payment",    "asset": "EUR/2", "amount": "80000",  "breakOn": "2026-09-25", "pairedHold": "main:hold:invoice:INV-12"},
      {"ref": "PAY-99", "class": "applied_before_final", "asset": "EUR/2", "amount": "-30000", "breakOn": "2026-10-01", "holdIds": ["INV-13"]}
    ],
    "resolved": [
      {"breakId": "3a6e9d0b2c8f4171", "class": "stuck", "side": "product", "hold": "main:hold:invoice:INV-5", "asset": "EUR/2", "amount": "70000", "clearedBy": "PAY-40"}
    ]
  },
  "files": [
    {"name": "flow.ndjson.gz",         "rows": 8, "sha256": "e41d…", "expiresAt": "2026-12-23"},
    {"name": "carried.ndjson.gz",      "rows": 4, "sha256": "7a02…", "expiresAt": "2026-12-23"},
    {"name": "stock.ndjson.gz",        "rows": 9, "sha256": "c9b8…", "expiresAt": "2026-12-23"},
    {"name": "breaks.ndjson.gz",       "rows": 5, "sha256": "15fe…", "expiresAt": "2026-12-23"},
    {"name": "unclassified.ndjson.gz", "rows": 1, "sha256": "90b3…", "expiresAt": "2026-12-23"}
  ],
  "anchor": false,
  "expiresAt": "2026-12-23"
}
```

- **The bridge closes.** `product.amount` is taken from the books: the invoice book lettered
  415,000 with nothing lettered outside matching. The flow rows' applications of the window also sum
  to 415,000 (PAY-40 70,000, PAY-42 100,000, PAY-43 100,000, PAY-44 115,000, PAY-99 30,000), hence
  a residual of 0.
- **The open items close.** The previous run handed over PAY-39 (500.00) and PAY-40 (700.00),
  1,200.00 in all. 1,200.00 − 150.00 = 1,050.00, which is the sum of the four carried rows.
- **The books read in the open direction.** For invoice holds, which open negative on the ledger:
  430,000 + 230,000 − 415,000 = 245,000. INV-14 (−10,000, wrong sign) counts negatively in it.

**`flow.ndjson.gz`**, 8 rows:

```text
{"ref":"PAY-39","asset":"EUR/2","class":"unapplied_payment","outcome":"break","pspAmount":"50000","productAmount":"0","drift":"50000","impact":"0","firstSeen":"2026-09-20","firstSide":"psp","breakOn":"2026-09-21","psp":[{"tx":1071229,"state":"payin.succeeded","amount":"50000","insertedAt":"2026-09-20T09:14:55Z"}],"product":[]}
{"ref":"PAY-40","asset":"EUR/2","class":"matched","outcome":"ok","pspAmount":"70000","productAmount":"70000","drift":"0","impact":"-70000","firstSeen":"2026-09-23","firstSide":"psp","psp":[{"tx":1160874,"state":"payin.succeeded","amount":"70000","insertedAt":"2026-09-23T15:03:12Z"}],"product":[{"tx":884517,"businessId":"INV-5","holdId":"INV-5","amount":"70000","insertedAt":"2026-09-24T06:12:44Z"}]}
{"ref":"PAY-42","asset":"EUR/2","class":"matched","outcome":"ok","pspAmount":"100000","productAmount":"100000","drift":"0","impact":"0","firstSeen":"2026-09-24","firstSide":"psp","psp":[{"tx":1249870,"state":"payin.pending","holdAmount":"100000","insertedAt":"2026-09-24T07:58:40Z"},{"tx":1250981,"state":"payin.succeeded","amount":"100000","insertedAt":"2026-09-24T08:01:17Z"}],"product":[{"tx":891204,"businessId":"INV-7","holdId":"INV-7","amount":"100000","insertedAt":"2026-09-24T08:05:10Z"}]}
{"ref":"PAY-43","asset":"EUR/2","class":"matched","outcome":"ok","pspAmount":"100000","productAmount":"100000","drift":"0","impact":"0","firstSeen":"2026-09-24","firstSide":"psp","psp":[{"tx":1261022,"state":"payin.pending","holdAmount":"100000","insertedAt":"2026-09-24T09:20:05Z"},{"tx":1262410,"state":"payin.succeeded","amount":"100000","insertedAt":"2026-09-24T09:22:48Z"}],"product":[{"tx":893118,"businessId":"INV-8","holdId":"INV-8","amount":"60000","insertedAt":"2026-09-24T09:25:31Z"},{"tx":893119,"businessId":"INV-10","holdId":"INV-10","amount":"40000","insertedAt":"2026-09-24T09:25:31Z"}]}
{"ref":"PAY-44","asset":"EUR/2","class":"under_applied","outcome":"break","pspAmount":"120000","productAmount":"115000","drift":"5000","impact":"5000","firstSeen":"2026-09-24","firstSide":"psp","psp":[{"tx":1287004,"state":"payin.pending","holdAmount":"120000","insertedAt":"2026-09-24T11:47:31Z"},{"tx":1288115,"state":"payin.succeeded","amount":"120000","insertedAt":"2026-09-24T11:50:02Z"}],"product":[{"tx":895660,"businessId":"INV-11","holdId":"INV-11","amount":"115000","insertedAt":"2026-09-24T11:55:48Z"}]}
{"ref":"PAY-45","asset":"EUR/2","class":"unapplied_payment","outcome":"pending","pspAmount":"80000","productAmount":"0","drift":"80000","impact":"80000","firstSeen":"2026-09-24","firstSide":"psp","breakOn":"2026-09-25","merchantRef":"INV-12","pairedHold":"main:hold:invoice:INV-12","psp":[{"tx":1300312,"state":"payin.pending","holdAmount":"80000","insertedAt":"2026-09-24T13:10:26Z"},{"tx":1301876,"state":"payin.succeeded","amount":"80000","insertedAt":"2026-09-24T13:12:59Z"}],"product":[]}
{"ref":"PAY-46","asset":"EUR/2","class":"in_progress","outcome":"ok","pspAmount":"0","productAmount":"0","drift":"0","impact":"0","psp":[{"tx":1312455,"state":"payin.pending","holdAmount":"25000","insertedAt":"2026-09-24T20:41:09Z"}],"product":[]}
{"ref":"PAY-99","asset":"EUR/2","class":"applied_before_final","outcome":"pending","pspAmount":"0","productAmount":"30000","drift":"-30000","impact":"-30000","firstSeen":"2026-09-24","firstSide":"product","breakOn":"2026-10-01","psp":[{"tx":1309640,"state":"payin.pending","holdAmount":"30000","insertedAt":"2026-09-24T19:55:37Z"}],"product":[{"tx":899031,"businessId":"INV-13","holdId":"INV-13","amount":"30000","insertedAt":"2026-09-24T17:02:20Z"}]}
```

- An application's `amount` is its net posting on the hold, in the settling direction, so INV-7's
  application counts 1,000.00 even though the same batch recognised revenue.
- A `pending` event shows its `holdAmount`, and only the `final` event's `amount` counts in
  `pspAmount`.
- PAY-39 is carried in: its `impact` is 0, because it was finalised on an earlier day and nothing
  was applied today.

**`carried.ndjson.gz`**, handed to 25 September: the 4 flow rows whose drift is not 0, without
`impact`.

```text
{"ref":"PAY-39","asset":"EUR/2","class":"unapplied_payment","outcome":"break","pspAmount":"50000","productAmount":"0","drift":"50000","firstSeen":"2026-09-20","firstSide":"psp","breakOn":"2026-09-21","psp":[{"tx":1071229,"state":"payin.succeeded","amount":"50000","insertedAt":"2026-09-20T09:14:55Z"}],"product":[]}
{"ref":"PAY-44","asset":"EUR/2","class":"under_applied","outcome":"break","pspAmount":"120000","productAmount":"115000","drift":"5000","firstSeen":"2026-09-24","firstSide":"psp","psp":[{"tx":1287004,"state":"payin.pending","holdAmount":"120000","insertedAt":"2026-09-24T11:47:31Z"},{"tx":1288115,"state":"payin.succeeded","amount":"120000","insertedAt":"2026-09-24T11:50:02Z"}],"product":[{"tx":895660,"businessId":"INV-11","holdId":"INV-11","amount":"115000","insertedAt":"2026-09-24T11:55:48Z"}]}
{"ref":"PAY-45","asset":"EUR/2","class":"unapplied_payment","outcome":"pending","pspAmount":"80000","productAmount":"0","drift":"80000","firstSeen":"2026-09-24","firstSide":"psp","breakOn":"2026-09-25","merchantRef":"INV-12","pairedHold":"main:hold:invoice:INV-12","psp":[{"tx":1300312,"state":"payin.pending","holdAmount":"80000","insertedAt":"2026-09-24T13:10:26Z"},{"tx":1301876,"state":"payin.succeeded","amount":"80000","insertedAt":"2026-09-24T13:12:59Z"}],"product":[]}
{"ref":"PAY-99","asset":"EUR/2","class":"applied_before_final","outcome":"pending","pspAmount":"0","productAmount":"30000","drift":"-30000","firstSeen":"2026-09-24","firstSide":"product","breakOn":"2026-10-01","psp":[{"tx":1309640,"state":"payin.pending","holdAmount":"30000","insertedAt":"2026-09-24T19:55:37Z"}],"product":[{"tx":899031,"businessId":"INV-13","holdId":"INV-13","amount":"30000","insertedAt":"2026-09-24T17:02:20Z"}]}
```

If PAY-44's missing 50.00 is booked tomorrow with the reference, tomorrow's run finds PAY-44 here
and classes it `matched`; without the carried row, it would see an application with no payment. If
the PSP finalises PAY-99 for 300.00 before 1 October, that day's run classes it `matched`, with a
`firstSeen` of 24 September and an `impact` of +300.00. Otherwise, on 1 October it becomes an
`orphan_application` of priority 1.

**`stock.ndjson.gz`**, 8 holds open at the cut and 1 cleared, 9 rows:

```text
{"side":"product","hold":"main:hold:invoice:INV-11","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-11","openSign":"negative","balance":"-5000","class":"open","outcome":"ok","lifecycle":"persisting","openedAt":"2026-09-04T08:30:00Z","ageDays":20,"bucket":"8-30d"}
{"side":"product","hold":"main:hold:invoice:INV-12","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-12","openSign":"negative","balance":"-80000","class":"open","outcome":"ok","lifecycle":"persisting","openedAt":"2026-09-19T12:05:00Z","ageDays":5,"bucket":"2-7d","pairedRef":"PAY-45"}
{"side":"product","hold":"main:hold:invoice:INV-14","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-14","openSign":"negative","balance":"10000","class":"wrong_sign","outcome":"break","lifecycle":"persisting","openedAt":"2026-09-18T16:20:00Z","ageDays":6,"bucket":"2-7d"}
{"side":"product","hold":"main:hold:invoice:INV-3","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-3","openSign":"negative","balance":"-120000","class":"stuck","outcome":"break","lifecycle":"persisting","openedAt":"2026-08-14T10:02:00Z","ageDays":41,"bucket":">30d"}
{"side":"product","hold":"main:hold:invoice:INV-5","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-5","openSign":"negative","balance":"0","class":"cleared","outcome":"ok","lifecycle":"cleared","openedAt":"2026-08-21T09:40:00Z","ageDays":34,"bucket":">30d","previousBalance":"-70000","clearedAt":"2026-09-24T06:12:44Z","clearedBy":"PAY-40"}
{"side":"product","hold":"main:hold:invoice:INV-9","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-9","openSign":"negative","balance":"-50000","class":"open","outcome":"ok","lifecycle":"persisting","openedAt":"2026-09-12T07:45:00Z","ageDays":12,"bucket":"8-30d"}
{"side":"product","hold":"main:hold:refund:RF-2","asset":"EUR/2","prefix":"main:hold:refund:","holdId":"RF-2","openSign":"positive","balance":"20000","class":"open","outcome":"ok","lifecycle":"new","openedAt":"2026-09-24T14:30:00Z","ageDays":0,"bucket":"0-1d"}
{"side":"psp","hold":"fpay:stripe:payment:hold:pending:PAY-46","asset":"EUR/2","prefix":"fpay:stripe:payment:hold:pending:","holdId":"PAY-46","openSign":"positive","balance":"25000","class":"open","outcome":"ok","lifecycle":"new","openedAt":"2026-09-24T20:41:09Z","ageDays":0,"bucket":"0-1d"}
{"side":"psp","hold":"fpay:stripe:payment:hold:pending:PAY-99","asset":"EUR/2","prefix":"fpay:stripe:payment:hold:pending:","holdId":"PAY-99","openSign":"positive","balance":"30000","class":"open","outcome":"ok","lifecycle":"new","openedAt":"2026-09-24T19:55:37Z","ageDays":0,"bucket":"0-1d"}
```

INV-14 is positive while invoice holds open negative, hence `wrong_sign`. INV-5, open yesterday at
−700.00 on the ledger, was lettered by PAY-40 today: it stays in the file once, as `cleared`.

**`breaks.ndjson.gz`**, 4 open and 1 resolved, 5 rows:

```text
{"breakId":"a93d02e6b7f1c448","leg":"flow","priority":2,"lifecycle":"new","openedOn":"2026-09-24","amount":"5000","ref":"PAY-44","asset":"EUR/2","class":"under_applied","outcome":"break","pspAmount":"120000","productAmount":"115000","drift":"5000","impact":"5000","firstSeen":"2026-09-24","firstSide":"psp","psp":[{"tx":1287004,"state":"payin.pending","holdAmount":"120000","insertedAt":"2026-09-24T11:47:31Z"},{"tx":1288115,"state":"payin.succeeded","amount":"120000","insertedAt":"2026-09-24T11:50:02Z"}],"product":[{"tx":895660,"businessId":"INV-11","holdId":"INV-11","amount":"115000","insertedAt":"2026-09-24T11:55:48Z"}]}
{"breakId":"1c7f3a90d2e84b55","leg":"flow","priority":3,"lifecycle":"persisting","openedOn":"2026-09-21","amount":"50000","ref":"PAY-39","asset":"EUR/2","class":"unapplied_payment","outcome":"break","pspAmount":"50000","productAmount":"0","drift":"50000","impact":"0","firstSeen":"2026-09-20","firstSide":"psp","breakOn":"2026-09-21","psp":[{"tx":1071229,"state":"payin.succeeded","amount":"50000","insertedAt":"2026-09-20T09:14:55Z"}],"product":[]}
{"breakId":"d4f8a1c3e5b70926","leg":"stock","priority":4,"lifecycle":"persisting","openedOn":"2026-09-14","amount":"120000","side":"product","hold":"main:hold:invoice:INV-3","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-3","openSign":"negative","balance":"-120000","class":"stuck","outcome":"break","openedAt":"2026-08-14T10:02:00Z","ageDays":41,"bucket":">30d"}
{"breakId":"7b24e1f09c3d6a12","leg":"stock","priority":4,"lifecycle":"persisting","openedOn":"2026-09-21","amount":"-10000","side":"product","hold":"main:hold:invoice:INV-14","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-14","openSign":"negative","balance":"10000","class":"wrong_sign","outcome":"break","openedAt":"2026-09-18T16:20:00Z","ageDays":6,"bucket":"2-7d"}
{"breakId":"3a6e9d0b2c8f4171","leg":"stock","priority":4,"lifecycle":"resolved","openedOn":"2026-09-21","resolvedOn":"2026-09-24","amount":"70000","side":"product","hold":"main:hold:invoice:INV-5","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-5","openSign":"negative","balance":"0","class":"stuck","outcome":"ok","openedAt":"2026-08-21T09:40:00Z","ageDays":34,"bucket":">30d","previousBalance":"-70000","clearedAt":"2026-09-24T06:12:44Z","clearedBy":"PAY-40"}
```

A stock break's `amount` reads in the open direction: INV-3 is 1,200.00 still open, and INV-14 is
100.00 on the wrong side. INV-5 had been `stuck` since 21 September, and its clearing resolves that
break, which keeps its last open class and amount.

**`unclassified.ndjson.gz`**, 1 row:

```text
{"side":"psp","tx":1276330,"ref":"PAY-31","asset":"EUR/2","outcome":"warning","state":"payin.refunded","amount":"20000","insertedAt":"2026-09-24T10:36:14Z"}
```

**The statement** the alert carries:

```text
psp-vs-billing — 24 Sep 2026 (cut-off 23:59:59 Europe/Paris) — BREAKS
4 open breaks (2 flow, 2 stock), 0 at P1, 0 accepted, 1 resolved · open flow breaks 550.00 EUR gross, net −150.00 EUR

EUR
  PSP — finalised payments in the window                           4,000.00  (4)
− Product — applications in the window                             4,150.00  (6)
= Net difference                                                     −150.00
  explained by:
    + unapplied payments of the window (until 25 Sep)                +800.00  (1)  PAY-45 → INV-12
    − applications awaiting a final PSP state (until 1 Oct)          −300.00  (1)  PAY-99 → INV-13
    ± under / over applications                             P2        +50.00  (1)  PAY-44
    ± matched, entered the join on an earlier day                    −700.00  (1)  PAY-40
  = unexplained residual                                                0.00  ✓
Carried from earlier days, outside the window's net:
    unapplied payments past product.grace                   P3        500.00  (1)  PAY-39
Gross open flow breaks: Σ|drift| = 550.00    Offsetting: no

Open items (psp − product)
  at the previous cut (23 Sep)                                     1,200.00  (2)
+ net difference of the window                                       −150.00
± entered through a lookup                                              0.00
= at this cut, handed to the next run                              1,050.00  (4)  ✓

Open books at S                     total      count   0–1d  2–7d  8–30d  >30d   continuity
  PSP pending holds                  550.00       2       2     —     —      —      ✓
  Product invoices                 2,450.00       5       —     2     2      1      ✓   1 stuck, 1 wrong_sign
  Product refunds                    200.00       1       1     —     —      —      ✓
  Lettered outside matching: none

Triage
  P2  under_applied       PAY-44 → INV-11, short by 50.00   new
  P3  unapplied_payment   PAY-39, 500.00, since 20 Sep      persisting
  P4  stuck               INV-3, 1,200.00 open, 41 days     persisting
  P4  wrong_sign          INV-14, 100.00 over-applied       persisting
Resolved: stuck INV-5, lettered by PAY-40.
Pending (not breaks): PAY-45, 800.00, a break on 25 Sep — its merchant reference names INV-12: apply it.
                      PAY-99 → INV-13, 300.00, applied before the PSP finalised it — an orphan
                      application (P1) on 1 Oct unless the PSP finalises it.
⚠ Unclassified: 1 PSP transaction with state "payin.refunded", 200.00 — the rule's state sets or the
  connector mapping need attention.
Detail: {bucketID}/reconciliation/rule=psp-vs-billing/day=2026-09-24/run=r-20260925T000004Z/
```

- **The PSP total** counts the four payments finalised in the window: PAY-42, 43, 44 and 45.
- **The product total** counts the six applications booked in it. That includes PAY-40's, whose
  payment was finalised the day before, and PAY-99's, whose payment the PSP has not finalised yet.
- **Why `BREAKS` and not `INCOMPLETE`.** Every difference is explained, so the residual is zero,
  and the verdict is `BREAKS`. Without the breaks, the unclassified refund would make it
  `RECONCILED_WITH_WARNINGS`, not green.
