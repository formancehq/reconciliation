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
   `incomplete.reason` and names the first items at fault in `incomplete.detail`. The run wrote no
   data file.
3. **Read the statement** (§5), which is the manifest's `statement`, `books` and `paymentAccounts`
   blocks. The alert carries the `statement` block and the `counts` as data, and the UI renders
   the statement. The bridge explains the day's net difference, the open items give the running
   total still unmatched, and `counts` says how many breaks and pending items there are.
4. **Open the files for the items.** The manifest carries aggregates only: `breaks.ndjson.gz` holds
   the rows to act on, `flow.ndjson.gz` the pending items and, with `stock.ndjson.gz`, the complete
   picture. The transactions the rule could not classify are counted in the manifest and listed
   from the ledger ([§6](#unclassified-transactions)).
   Recon's API pages the breaks, with filters on class, priority and lifecycle.

| Question | Where to look |
|---|---|
| Is the day reconciled? | `manifest.json` → `verdict` |
| What must be done today? | `counts.breaks.openByPriority`, then `breaks.ndjson.gz` with `outcome = 'break'`, by `priority` |
| What turns into a break soon? | `counts.flowOutcome.pending`, then `flow.ndjson.gz` with `outcome = 'pending'`, by `breakOn` |
| Why is the day's net not zero? | `statement.{asset}.lines` |
| How much is still unmatched, all days together? | `statement.{asset}.suspense.open`, which is the sum of `drift` in `carried.ndjson.gz` |
| What is still open on each ledger? | `books`, then `stock.ndjson.gz` |
| Was anything lettered outside matching (credit note, write-off)? | `books[].letteredOther`; in the stock, a `cleared` row with no `clearedBy` |
| Everything about invoice INV-12 | a filter on `businessId`, `holdId` or `merchantRef` in every file (§9) |
| Does the connector mapping follow the rule? | `counts.unclassified`, then `statement.{asset}.unclassified` per state value, then the ledger ([§6](#unclassified-transactions)) |

## 2. Where the files are, and which run counts

```text
{backup bucket}/{bucketID}/reconciliation/rule={ruleId}/day={YYYY-MM-DD}/run={runId}/
  manifest.json           the run's aggregates: rule, cuts, verdict, counts, statement, books, files
  flow.ndjson.gz          one row per payment reference and asset: the window's, plus the ones carried in or looked up
  carried.ndjson.gz       the flow rows whose drift is not 0, handed to the next run
  stock.ndjson.gz         one row per open hold at the cut, plus the holds cleared since the previous run
  breaks.ndjson.gz        every break of the three legs, open or resolved since the previous run
```

- **The bucket** is the backup destination of the rule's **product ledger**, under a prefix next to
  `backups/`. `{bucketID}` is that ledger's bucket.
- **Access.** Recon's API lists a run's files with a pre-signed URL for each. A deployment that owns
  the storage can also read the prefix directly. The `key=value` path segments let DuckDB, Spark or
  Athena read `rule`, `day` and `run` as columns.
- **A run exists once its manifest is written.** The manifest is the run's last file, written
  after the data files and the signed capture that holds its verdict and its SHA-256 (ADR-005 §7,
  item 2); every run's status comes from its capture. A run
  directory with no manifest is a run that stopped: no reader counts it, and its files expire under
  the prefix's lifecycle rule.
- **An `incomplete` run writes its capture and a reduced manifest, and no data file.** Like a
  complete run, it writes the capture first and the manifest last. Its signed capture holds the
  verdict, `incomplete.reason`, the cut `T` of each ledger where it was resolved and the
  manifest's SHA-256; its manifest carries no counts and no statement (§6). It raises the
  engine-error alert (§4), and it is not a link in the chain.
- **The day's current run is its latest complete run**: the latest run whose manifest exists and
  whose `verdict` is not `incomplete`. A `runId` is `r-` followed by the run's start instant in UTC
  (`r-20260925T000004Z`), so run ids sort in time order. Replaying or retrying a day writes a new
  run, which replaces the earlier ones once its manifest is written. A reader picks the current runs
  from the manifests, then reads their data files (§9).
- **Runs are chained.** `previousRun` names the current run of the most recent earlier day that has
  one, with its day and its manifest's SHA-256. When that day is not the day before (a day missed or
  incomplete), this run's window starts at that run's cut and covers every day since. The statement
  then says "window since …".
- **A first run compares one day too.** A first run, a new rule's, a restart's (below) or a
  replay's once the previous run's files have expired, has no `previousRun`, and its window is
  still `(T_prev, T]`, with `T_prev` the cut of the day before (ADR-005 §7, item 6).
  - **Its starting values are rewound to `T_prev`** by its own stock rewind, read one day further:
    the books' `openPrev`, the payment account's `inputPrev` and `outputPrev`, and the stock the
    lifecycle of its stock rows is read against (a hold open at `T_prev` is `persisting`, or
    `cleared` once lettered).
  - **Its open items are seeded.** A read of the flow over the days from `backfillFrom` up to
    `T_prev`, with the product side starting `psp.grace` earlier, is joined, and the references
    still open at `T_prev` with a drift are carried in as if a previous run had carried them: they
    give `openPrev` and `countPrev` (§5). None of the seed's transactions counts in the day's
    bridge, books or payment-account book. `backfillFrom` is a date in the rule's timezone, by
    default cut-off − max(`psp.grace`, `product.grace`) − 1 day; one on or after the compared day
    means no seed. The manifest's `rule.backfillFrom` records it (§6).
  - The statement says "open items seeded since …". A hold opened before the seed has a null
    `openedAt` and a lower-bound age (§6).
- **Restarting a chain.** Some causes of `incomplete` come back on every run until they are fixed,
  and each run's window grows (§4). When the fix cannot enter the window (a booking corrected by a
  new transaction leaves the faulty one in it, or a stored file was altered), an operator restarts
  the rule: its next run is a first run, with no `previousRun`, so nothing stored is trusted.
  - Its `backfillFrom` defaults to the earlier of the first-run default and the oldest `firstSeen`
    of the last complete run's carried items, so the seed finds the items still open, and falls back
    to a first run's default when nothing was carried. When the carried file failed its signed
    check (`stored_file_mismatch`), the default cannot be computed from it, so a restart without
    `backfillFrom` is refused; a mismatch on the stock or breaks file alone keeps the default.
  - Its breaks start `new`: their `breakId` is unchanged, their history is not carried over.
  - The restart takes an optional `backfillFrom`, rejected when it is later than the next run's
    cut. Nothing else is stored: the next run's manifest, a first run with no `previousRun` and
    "open items seeded since …", is the trace of the restart.
- **Catching up from a past day.** An API action on the rule writes a normal run for every day
  from a day X to its last day `to`, yesterday by default, as if the rule had run since X (ADR-005
  §7, item 7). A catch-up with `to` = X is how one past day is replayed. The caught-up
  runs are normal runs, chained day by day, with day X a first run unless day X−1 already has a
  current run. Nothing marks them but their `startedAt` and `runId`, later than their day, and they
  raise no alert for a closed period. A catch-up that stops leaves complete runs up to the
  interrupted day, which has no manifest.
- **Expiry.** A run's files are kept for the deployment's retention, the operator setting
  `--lettering-retention`, 90 days by default. A lifecycle rule of the storage on
  `{bucketID}/reconciliation/` deletes them; recon deletes nothing. The retention is not a rule
  parameter: it applies to every rule under the product ledger's prefix. The storage counts from
  each file's creation, so the manifest's `expiresAt` is the run's start (`startedAt`, the instant
  in its `runId`) plus the retention, for information only: a replayed or caught-up day's files
  expire a retention after that run, not after the day. A customer bound to a longer legal
  retention has the operator raise both ([design doc
  §5](./transaction-level-reconciliation.md#result-artifacts-and-retention)).
  - **A replay's stock at its cut is always rewound from head.** Within the retention, the previous
    day's carried, stock and breaks files give its carried items, `openPrev`, the `cleared` holds,
    `openedAt` and the lifecycle, and the replay reproduces the day's files byte for byte (§8).
  - An expired day can still be recomputed from the ledgers' transactions. With the previous day's
    files expired, the replay is a first run and its carried items are seeded (ADR-005 §7, item 7),
    so an item open for longer than the seed window is missed unless the replay is given an
    earlier `backfillFrom`.

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
- **Ledger ids** (`tx`, and the cuts' `txFrom`, `txTo` and `txHead`) are JSON numbers. They are
  uint64 and stay below 2^53 in practice; a JavaScript reader that must be exact beyond that parses
  them as big integers.
- **Per side.** A figure split per side is keyed `psp` or `product`, never by the ledger's name.
- **Checksums** are SHA-256 in lowercase hex, in fields whose name ends in `sha256` or `Sha256`.
  `breakId` is an identifier, not a checksum: 16 hex characters of a hash.

**Four fields give a row's status**, and each answers one question:

| Field | Question | Values | On |
|---|---|---|---|
| `outcome` | Must someone act? | `ok` (nothing to do), `pending` (not a break yet), `break` | every row |
| `class` | What happened? | per leg, §6 | flow, carried, stock and break rows |
| `priority` | How urgent is it? | 1 to 4, §6 | break rows |
| `lifecycle` | What changed since the previous run? | `new`, `persisting`, then `cleared` for a hold or `resolved` for a break | stock and break rows |

The rule's `severity` (`low` to `critical`) grades the alert only, and is unrelated to `outcome`
and `priority`.

## 4. The verdict

The verdict is evaluated in this order, and the first condition that holds wins:

| `verdict` | Condition | What it tells the controller |
|---|---|---|
| `incomplete` | A required index is missing, a transaction range came back shorter than `hi − lo`, a continuity identity fails, the bridge's residual is not 0, or a previous run's file this run reads (its stock, carried or breaks file) is missing or differs from the SHA-256 its signed capture holds. There is no silent fallback: a missing file is an altered one, and the operator restarts the rule, whose next run is a first run (§2). `incomplete.reason` says which: `missing_index`, `short_range`, `continuity`, `residual`, `stored_file_mismatch`. `missing_index` and `short_range` usually clear on the next scheduled run. The other three come back on every run until the cause is fixed: the engine-error alert then says that an operator must act, and when the fix cannot enter the window the operator restarts the rule (§2). `incomplete.detail` names the first 20 items at fault (the books and holds that do not close with the transactions that moved them, the applications no flow row attributes, or the altered files), and the engine's logs list them all | No conclusion can be drawn. The read is incomplete, or a hold moved in a transaction that carries neither the key nor a business id. The run writes its signed capture and a reduced manifest (§6), no data file, and opens the engine-error alert, never a green one |
| `breaks` | At least one open break | The breaks, by priority, new or persisting |
| `reconciled_with_warnings` | No break, but at least one unclassified transaction | Money moved that the rule does not classify: the rule's state sets or the connector mapping need attention |
| `reconciled_with_pending` | No break and no warning, but unapplied payments within `product.grace` or applications within `psp.grace` | "OK for now". Each pending item comes with the day it becomes a break |
| `reconciled` | None of the above | The only green state |

**What opens the alert:** at least one open break. A break is never accepted one by one: a known
break stays open until it is booked, and the controller acknowledges or accepts the alert itself,
as for any rule. A resolved break does not open it, and neither does the net alone, since pending items move the net without being breaks and
offsetting breaks net to zero. An `incomplete` run opens the engine-error alert instead.

## 5. The statement

A net difference of 0 proves nothing: breaks of +1,000 € and −1,000 € net to zero. Every run
therefore answers with a reconciliation statement, the classic *état de rapprochement*. It has
three parts per asset, each closed by an identity that the engine checks, and a fourth check on the
PSP payment account, whose residual is a break rather than an `incomplete` run.

The statement is data: the manifest's `statement`, `books` and `paymentAccounts` blocks (§6). Its
figures render from the manifest alone; lists of items come from the files, through the API. The
layouts below show what each part adds up, and §10 shows one way to present a whole day.

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
Gross open flow breaks: Σ|drift| = …
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
- **The carried lines** are `SUM(drift)` of the rows carried in from earlier days with no movement
  in the window: their `impact` is 0, so they sit outside the window's net. A carried row that
  moves today is on a window line instead, with its `impact`.
- **The gross** covers every open flow break, from the window or carried in, so it is not on the
  same scope as the net.

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
- **`openPrev`** and `countPrev` are the previous current run's `open` and `count`, or on a first
  run the sum and count of its seeded items (§2).
- **The identity** `open = openPrev + net + fromLookups` fails when a carried item is lost or
  counted twice between two runs. The run is then `incomplete` (`continuity`).
- **`open` is the running balance of the reconciliation**: payments not applied yet, applications
  not finalised yet, and amounts that disagree, all days together.

### The open books: what is open on each ledger

For each side, hold prefix and asset, in the open direction:

```text
open = openPrev + opened − lettered
```

- `openPrev` is the previous run's stored stock, or on a first run the stock rewound to `T_prev`
  (§2). `opened` and `lettered` are the window's hold movements, and `open` is the stock rewound to
  the cut.
- A transaction's movement counts in `opened` when it goes in the open direction and in
  `lettered` when it goes in the settling direction. The exception is a product transaction that
  takes part in matching: it always counts in `lettered`, with its signed amount, even when it
  moves the hold in the open direction (an application undone with its reference). That way,
  `lettered − letteredOther` on the product side is exactly the window's applications, the
  bridge's B. Movements are counted per hold: a transaction that moves money from one invoice to
  another counts on each.
- A lost event, or a hold moved in a transaction that carries neither the key nor a business id,
  breaks the identity. The run is then `incomplete` (`continuity`).
- **`letteredOther`** is the part of `lettered` done by transactions that take no part in
  matching. Most carry no PSP reference: a credit note, a write-off, a manual lettering. The rest
  carry one but have a state in none of the rule's sets, and are also counted as unclassified. It
  is not an error, but it is money that cleared a hold with no matched payment behind it, so it
  stays visible. A transaction without a PSP reference must carry the hold's business id; otherwise
  the flow read misses it and continuity fails.
- On the PSP side, `letteredOther` holds only the letterings of unclassified events.
- The age buckets count the open holds by age.

### The payment-account book: did the PSP payment account move only as the flow says?

For each account matching `psp.paymentAccount`, per asset, and separately for credits and debits:

```text
input(T)  − input(T_prev)  = credits on the account by the transactions the flow read returned
output(T) − output(T_prev) = debits on the account by the transactions the flow read returned
```

- The account is `NORMAL`, so its volumes are cumulative. `input(T)` and `output(T)` are its live
  volumes rewound to the cut with the stock's transaction window; the `T_prev` values are the
  previous run's, or on a first run the same rewind read one day further (§2).
- The flow read returns every transaction that carries the PSP key or one of the rule's
  `psp.movementKeys` (payouts, fees), whatever its class, unclassified ones included. The
  movement-key transactions count here and nowhere else.
- **This is the one check that sees a final with no `pending` before it and no reference**: such a
  final moves no hold, so the open books close, and the flow read cannot return it. A payout or a
  fee booked without its key shows up here too.
- A residual on either side opens a P1 break of the class `unkeyed_payment_movement` on the leg
  `book` (§6). It does not stop the run: the bridge, the open items and the books stand, and the
  verdict is `breaks`.

### Around the three parts

- **The items** are in the files, and `counts` gives their totals. The open breaks, new or
  persisting, and the breaks resolved since the previous run are the rows of `breaks.ndjson.gz`, in
  priority order, then by amount (§8). The pending items are the `pending` rows of
  `flow.ndjson.gz`, each with its `breakOn`. Recon's API pages the breaks, with filters on class,
  priority and lifecycle.
- **A merchant reference.** When the rule names `psp.merchantRef`, every unapplied payment is
  paired with the open business hold it names: `pairedHold` on its flow row, `pairedRef` on the
  hold's stock row.
- **Warnings**: unclassified transactions per side and state value. They make the verdict
  `reconciled_with_warnings` at best.

### The alert's evidence

There is one alert per rule, fingerprint and period, and it carries structured data, never
rendered text. Its headline, statement and counts are those of the **latest day of the period that
has a current run**; with a `daily` period, that is the period's one day:

- **the headline**: that day's `verdict`, open breaks per leg (`counts.breaks.openByLeg`), open P1
  breaks (`counts.breaks.openByPriority["1"]`), and per asset the gross (`flowGross`) and the net
  (`net`);
- **the statement**: that day's manifest `statement` block, as JSON;
- **the counts**: that day's manifest `counts`;
- **the day list**: one entry per day of the period that has a current run, with its `verdict`,
  its `counts`, per asset its `net` and gross (`flowGross`), and the link to its run's files, which
  recon's API lists with pre-signed URLs (§2). A day with no complete run is not listed: the next
  complete run's window covers it.

Every tick rebuilds the open period's alert from these manifests. A period closes once the next
period's first day has a current run, and a closed period's alert is never rebuilt (ADR-005 §7,
items 2 and 5).

It carries no list of breaks or pending items. The UI renders the reconciliation statement from
this data and the latest day's `books`, and shows the breaks by paging them from recon's API. The
engine renders no text.

## 6. File reference

### `manifest.json`

| Field | Meaning |
|---|---|
| `schemaVersion` | `lettering/1` |
| `engine` | The version of recon that produced the run. A replay reproduces the files only with the same one |
| `rule` | The whole rule as evaluated: `id`, `version`, `sha256` and every parameter, including each side's `key`, `state` sets, `grace`, `holds` (`prefix`, `openSign`, `businessId` on the product side), `psp.paymentAccount`, `psp.movementKeys` and `psp.merchantRef`. `backfillFrom` is the date, in the rule's timezone, where the chain's first run seeded its open items from (§2): the seed covers the days from it up to `T_prev`, and one on or after the first run's day means no seed. It is the rule's parameter or its default, or the restart's; every later run of the chain repeats it, since a null `openedAt` counts its age from it. The retention is not a rule parameter (§2) |
| `runId` | `r-{UTC start instant}`; run ids sort in time order |
| `previousRun` | `runId`, `day` and `manifestSha256` of the current run of the most recent earlier day that has one. Absent on the first run |
| `period` | `type`, `day`, `cutoff` (with the rule's offset) and `tz` |
| `startedAt`, `finishedAt` | When the run started and finished. Per-step durations and read counts go to the engine's metrics and logs, not to the manifest |
| `cuts` | One entry per side (on an `incomplete` run, per side whose cut was resolved): `ledger` and the transaction window `(txFrom, txTo]`. `txTo` is the cut `T`; `txFrom` is `T_prev` on every run: the previous run's `txTo`, or on a first run the cut of the day before. A cut with no transaction at or before its cut-off is 0, since ids start at 1. A first run's seed lies before it and counts in no figure of the day; `rule.backfillFrom` records where the seed started. Also `txHead`, the transaction head the run read up to: the rewind reads the transactions `(txTo, txHead]`, and on a first run `(txFrom, txHead]` |
| `verdict` | §4 |
| `incomplete` | Only when `verdict` is `incomplete`: `reason` and a human-readable `detail`, which names the first 20 items at fault (§4) |
| `counts.flow` | Flow rows per class; adds up to the flow file's row count |
| `counts.flowOutcome` | Flow rows per outcome |
| `counts.stock` | Stock rows per side and class |
| `counts.breaks` | `new`, `persisting`, `resolved`, and open breaks `openByLeg` (`flow`, `stock`, `book`) and `openByPriority` |
| `counts.unclassified` | Unclassified transactions per side |
| `statement.{asset}` | The bridge: `psp` and `product` (`amount`, `count`), `net`, `lines` (`class`, `outcome`, `earlierDay`, `amount`, `count`; `earlierDay` is `firstSeen < day`, false on a row with no `firstSeen`, such as an `in_progress` row whose application was undone), `residual`, `carriedOutside` (`class`, `outcome`, `amount` as `SUM(drift)`, `count`), `flowGross`. The open items: `suspense` (`openPrev`, `countPrev`, `fromLookups`, `open`, `count`, `continuityOk`). And `unclassified` per side and state |
| `books` | One entry per side, prefix and asset: `openSign`, `openPrev`, `opened`, `lettered`, `letteredOther`, `open`, `count`, `buckets`, `continuityOk` |
| `paymentAccounts` | The payment-account book (§5): one entry per account matching `psp.paymentAccount` and asset, with `account`, `asset`, `inputPrev`, `input`, `outputPrev`, `output` (the account's volumes at the previous cut and at this one), `flowCredits`, `flowDebits` (what the flow read's transactions posted on it) and `creditResidual`, `debitResidual`. The next run reads its `T_prev` values here |
| `files` | One entry per file: `name`, `rows`, `sha256` |
| `expiresAt` | For information: the run's start (`startedAt`, the instant in `runId`) plus the deployment's retention, as an instant. The storage's lifecycle rule deletes the files, counting from their creation, so a replayed or caught-up day's files expire a retention after that run (§2) |

**An `incomplete` run's manifest is reduced** (§2). It always has `schemaVersion`, `engine`,
`rule`, `runId`, `previousRun` (absent on a first run, as on any run), `period`, `startedAt`,
`finishedAt`, `verdict`, `incomplete` and `expiresAt`. Its `cuts` lists only the sides whose cut
was resolved: a side is missing when, for example, its ledger's `inserted_at` index is missing
(`missing_index`). It never has `counts`, `statement`, `books`, `paymentAccounts` or `files`.

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
| `breakOn` | On a `pending` row, the day it becomes a break: `firstSeen` plus the lagging side's `grace`. It stays on the row once the break is open, as long as the row keeps the class that set it |
| `merchantRef`, `pairedHold` | When the PSP reports a merchant reference, and the open hold it names |
| `psp` | The reference's PSP transactions: `tx`, `state`, `insertedAt`, plus `amount` on a `final` event, which is its net posting on `psp.paymentAccount` and what `pspAmount` sums. A `pending` or `failed` event carries `holdAmount` instead: its hold movement, in absolute value |
| `product` | The reference's applications: `tx`, `businessId`, `holdId`, `amount` (the net posting on the hold, in the settling direction), `insertedAt`. A transaction that letters two holds is listed once per hold |

A reference carried in or read by key keeps the transactions of its earlier days. Any other row
lists the window's transactions only.

**Flow classes.** A class reads the net amounts, not which transactions exist: applications that
sum to 0 (an application undone with its reference) count as no application. A payment applied then
undone is `unapplied_payment`, since the cash waits to be applied again, and it keeps its
`firstSeen`. A matched payment that the PSP fails and the product undoes is `failed`, with nothing
left to letter. The row still lists every transaction.

| `class` | Meaning | `outcome` (`priority`) |
|---|---|---|
| `matched` | PSP `final`, and applications summing to the same amount | `ok` |
| `under_applied` / `over_applied` | PSP `final`, and applications summing to less or to more. There is no tolerance: an unbooked fee is a break | `break` (2) |
| `unapplied_payment` | PSP `final`, nothing applied | `pending` within `product.grace`, then `break` (3) |
| `in_progress` | No final or failed PSP state, and nothing applied. With the PSP `pending`, its hold is in the PSP stock | `ok` |
| `failed` | PSP `failed`, nothing applied | `ok` |
| `applied_before_final` | An application whose reference the PSP has not finalised yet (`pending`, or not seen at all) | `pending` within `psp.grace`, then becomes `orphan_application` |
| `orphan_application` | An application whose reference is still not final past `psp.grace`, or was already `failed` | `break` (**1**) |
| `reversed_after_application` | The PSP reports `failed` after the product applied the reference, within `psp.grace` or not. A refund or chargeback is not this: it has its own reference | `break` (**1**) |

### `carried.ndjson.gz`

The flow rows whose `drift` is not 0, without `impact`, which describes only their own day's
window. The next run joins them with its window; they are a separate file so that it reads a small
file, not the whole flow. The sum of their `drift` is
`statement.{asset}.suspense.open`, and their count is `suspense.count`.

### `stock.ndjson.gz`

One row per hold open at the cut, plus one row per hold cleared since the previous run.

| Field | Meaning |
|---|---|
| `side`, `hold`, `asset` | The unique key |
| `prefix`, `holdId`, `openSign` | The rule's hold kind and the id after its prefix |
| `balance` | The ledger's balance at the cut, signed as the ledger shows it |
| `class`, `outcome` | Below |
| `lifecycle` | `new`, `persisting` or `cleared`, against the previous run, or on a first run against the stock rewound to `T_prev` (§2) |
| `openedAt`, `ageDays`, `bucket` | When the hold opened (its opening transaction's `timestamp`, the business date), its age at the cut in days, and its age bucket: `0-1d`, `2-7d`, `8-30d` or `>30d`, fixed by the engine (ADR-005 §7 item 8). The opening is known when it lies in a window recon read, a first run's seed included. `openedAt` is null for a hold opened before the chain's seed (§2): before `backfillFrom`, or on the product side before the seed's start, `psp.grace` earlier. Its opening was never read, so `ageDays` is a lower bound counted from `rule.backfillFrom`, and `bucket` follows it |
| `previousBalance`, `clearedAt`, `clearedBy` | On a cleared hold: its balance at the previous cut, when it was lettered, and the `ref` that lettered it. No `clearedBy` means it was lettered without a PSP reference |
| `pairedRef` | The unapplied payment whose `merchantRef` names this hold |

**Stock classes:**

| `class` | Meaning | `outcome` (`priority`) |
|---|---|---|
| `open` | Open in the direction its prefix expects | `ok` |
| `wrong_sign` | The balance has the sign opposite `openSign`: an over-application, or a skipped state | `break` (4) |
| `cleared` | Open at the previous cut, lettered since: listed once, with a balance of 0 | `ok` |

The two stock books are aged, never joined to each other: an unpaid invoice has no PSP counterpart
by design. An open hold is never a break for its age: the buckets show it, and holds held too long
are the [`stale_holds`](./stale-holds.md) template's job.

### `breaks.ndjson.gz`

Every open break of the three legs, plus the breaks resolved since the previous run. A break row is
the complete row of its flow or stock file, or, for a book break, its entry of the manifest's
`paymentAccounts` with the `direction` it breaks on, so a reader never joins files to show a break,
plus these fields:

| Field | Meaning |
|---|---|
| `breakId` | A hash of rule, leg, key and asset, **not the class**. The key is `ref` for a flow break, `side` + `hold` for a stock break, and `side` + `account` + `direction` for a book break. It stays the same from day to day, and comments and assignments attach to it |
| `leg` | `flow`, `stock` or `book` |
| `priority` | 1 to 4, below |
| `lifecycle` | `new`, `persisting` or `resolved` |
| `openedOn`, `resolvedOn` | The day the break opened, and the day it was resolved |
| `amount` | Signed. On a flow break it is the `drift` (`psp − product`); on a stock break, the balance in the open direction; on a book break, the residual of its direction (the account's movement minus the flow's) |

| `priority` | Classes | Why |
|---|---|---|
| 1 | `orphan_application`, `reversed_after_application` | The product booked money with no cash behind it |
| 1 | `unkeyed_payment_movement` | The PSP payment account moved by money no keyed transaction explains: a final with no pending and no reference, or a payout or fee without its key |
| 2 | `under_applied`, `over_applied` | The amounts disagree |
| 3 | `unapplied_payment` past `product.grace` | Cash received and still not applied |
| 4 | `wrong_sign` | A hold of the wrong sign to investigate |

**Book class.** The leg `book` has one class, `unkeyed_payment_movement`, with `outcome` `break`
(**1**). A book row carries `side` (`psp`), `account`, `asset`, `direction` (`credit` or
`debit`), and the fields of its `paymentAccounts` entry. It is resolved on the first run whose
residual in that direction is 0 again.

`class`, `outcome`, `priority`, `lifecycle` and `amount` are the break's. A resolved break keeps
the class, priority and amount it had when it was last open, next to the row as it stands now, and
its `outcome` is `ok`. So `outcome = 'break'` counts the open breaks in every file.

### Unclassified transactions

A transaction whose state is in none of the rule's sets takes no part in matching. The run writes
no file for it: the manifest counts these transactions per side (`counts.unclassified`) and gives,
per asset, side and state value, their count and amount (`statement.{asset}.unclassified`). The
amount is a transaction's net posting, in absolute value, on the accounts the rule reads for that
side: `psp.paymentAccount` and the hold prefixes on the PSP side, the hold prefixes on the product
side. A typical cause is a connector booking refunds on the original payment's id ([connector
checklist](./transaction-level-reconciliation.md#mapping-a-connector-for-reconciliation), row 6).
The state value names the fix: add it to one of the rule's sets, or correct the mapping.

To list the transactions, read that side's ledger: `ListTransactions` filtered on the key's
presence, the state field equal to the value, and the day's id range `(txFrom, txTo]` of that
side in the manifest's `cuts`, with the field names from its `rule`.

## 7. How rows move from day to day

- **A pending flow row becomes a break on its `breakOn` day.** `unapplied_payment` goes from
  `pending` to `break` (P3). `applied_before_final` becomes `orphan_application` (P1).
- **A reference stays carried while its `drift` is not 0.** It leaves the carried file only when a
  booking brings the drift to 0: the PSP finalises it, or the product books the difference or
  reverses the application. There is no write-off state:
  a systematic difference, such as a fee never booked on every payment, is fixed by booking it.
- **A hold** moves up the age buckets while it stays open, and is listed once as `cleared` by the
  run whose window lettered it.
- **A break keeps its `breakId` for life.**
  - When its class changes (an orphan later reported `failed`, an unapplied payment later applied
    short), it stays the same break, `persisting`. Its earlier class is on the previous run's
    break row with the same `breakId`.
  - A resolved break that opens again is `new` again, with a new `openedOn`, and keeps its history.

## 8. Rules a reader can rely on

- **Every data file is written on every complete run**, even with no row, so a glob never breaks
  on a quiet day. The manifest gives each file's row count.
- **Every run that exists has a signed capture and a manifest**, the manifest written last, and
  its status comes from the capture. An `incomplete` run writes no data file, and its manifest is
  reduced to the fields §6 lists: no `counts`, `statement`, `books`, `paymentAccounts` or `files`,
  so a reader that sums figures over manifests keeps the complete runs only (§9).
- **The manifest carries aggregates only.** The statement's figures render from the manifest
  alone; lists of items (breaks, pending items, resolved breaks) come from the files, through the
  API. The one exception is `incomplete.detail`, which names the first 20 items at fault of a run
  that writes no data file (§4).
- **One file per kind.** Each data file is a single gzip, whatever its size: the flow file of a
  1M-payment day weighs 74–142 MB and takes ~5 s to write on one thread (design doc §7.14). A
  reader that follows the manifest's `files`, or globs `flow*.ndjson.gz`, keeps working if a later
  format splits a file.
- **The same cut gives the same bytes.** Keys follow the order of the file's JSON Schema, rows the
  order below, and gzip uses a fixed level with no name and no timestamp. A replay therefore
  reproduces every data file's SHA-256, provided four things hold:
  - the same engine version;
  - the same rule version;
  - the same previous run, whose carried, stock and breaks files the day starts from, so a replay
    while that run's files are kept (a retention after that run, §2);
  - no key, state, business-id or merchant-reference metadata changed since the original run,
    because the ledger serves the current metadata. Recon does not watch for such a change
    (ADR-005 decision 25).

  Only the manifest differs, through its instants. So a replay within the retention can be
  compared with the original by the SHA-256s in their manifests' `files`, a query over the day's
  manifests. With the same rule version and previous run, a mismatch reveals a metadata change or
  an engine change, which `engine` shows.
- **Compatibility.** A new optional field may appear within `lettering/1`, so a reader ignores
  fields it does not know. Removing or renaming a field, changing its meaning, or adding a value to
  an enumeration changes `schemaVersion`.

| File | Unique key | Order |
|---|---|---|
| `flow` | `ref`, `asset` | `ref`, `asset` |
| `carried` | `ref`, `asset` | `ref`, `asset` |
| `stock` | `side`, `hold`, `asset` | `side`, `hold`, `asset` |
| `breaks` | `breakId` | open before resolved, then `priority`, then `\|amount\|` descending, then `breakId` |

## 9. Queries

**Internal tooling.** The team validates and analyses result files with
[`tools/lettering-duckdb`](./lettering-duckdb.md): ready-made queries and checks, tested on §10.

A few standalone examples follow, on the worked example; the paths are relative to the rule's
prefix.

**The current run of each day.** Pick it from the manifests before anything else, with DuckDB,
then read only its data files. A run that stopped before its manifest, or an `incomplete` one, is
never picked.

```sql
CREATE VIEW current_runs AS
SELECT day, run FROM read_json_objects('rule=psp-vs-billing/day=*/run=*/manifest.json', hive_partitioning = true)
WHERE json->>'verdict' <> 'incomplete'
QUALIFY run = max(run) OVER (PARTITION BY day);

CREATE VIEW flow AS
SELECT * FROM read_json_auto('rule=psp-vs-billing/day=*/run=*/flow.ndjson.gz', hive_partitioning = true)
SEMI JOIN current_runs USING (day, run);
```

**Rebuild the bridge** from the flow file alone:

```sql
SELECT class, outcome, coalesce(firstSeen < day, false) AS earlierDay,
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
    "backfillFrom": "2026-08-01",
    "psp":     {"ledger": "psp",  "key": "payments.formance.com/payment-id",
                "state": {"field": "formance.com/observation.event-type", "pending": ["payin.pending"],
                          "final": ["payin.succeeded"], "failed": ["payin.compensate"]},
                "grace": "7d", "paymentAccount": "fpay:stripe:account:*:main",
                "merchantRef": "merchant_ref",
                "holds": [{"prefix": "fpay:stripe:payment:hold:pending:", "openSign": "positive"}]},
    "product": {"ledger": "main", "key": "psp_payment_ref",
                "state": {"field": "transition_kind", "final": ["to_final"]},
                "grace": "1d",
                "holds": [{"prefix": "main:hold:invoice:", "openSign": "negative", "businessId": "invoice_no"},
                          {"prefix": "main:hold:refund:",  "openSign": "positive", "businessId": "refund_no"}]}
  },
  "runId": "r-20260925T000004Z",
  "previousRun": {"runId": "r-20260924T000003Z", "day": "2026-09-23", "manifestSha256": "a3e0…"},
  "period": {"type": "daily", "day": "2026-09-24", "cutoff": "2026-09-24T23:59:59+02:00", "tz": "Europe/Paris"},
  "startedAt": "2026-09-25T00:00:04Z",
  "finishedAt": "2026-09-25T00:00:31Z",
  "cuts": [
    {"side": "psp",     "ledger": "psp",  "txFrom": 1204000, "txTo": 1318500, "txHead": 1320606},
    {"side": "product", "ledger": "main", "txFrom": 880400,  "txTo": 902750,  "txHead": 904500}
  ],
  "verdict": "breaks",
  "counts": {
    "flow": {"matched": 3, "under_applied": 1, "over_applied": 0, "unapplied_payment": 2,
             "applied_before_final": 1, "orphan_application": 0, "reversed_after_application": 0,
             "in_progress": 1, "failed": 0},
    "flowOutcome": {"ok": 4, "pending": 2, "break": 2},
    "stock": {"psp":     {"open": 2, "wrong_sign": 0, "cleared": 0},
              "product": {"open": 5, "wrong_sign": 1, "cleared": 1}},
    "breaks": {"new": 1, "persisting": 2, "resolved": 0,
               "openByLeg": {"flow": 2, "stock": 1, "book": 0}, "openByPriority": {"1": 0, "2": 1, "3": 1, "4": 1}},
    "unclassified": {"psp": 1, "product": 0}
  },
  "statement": {
    "EUR/2": {
      "psp":     {"amount": "400000", "count": 4},
      "product": {"amount": "415000", "count": 6},
      "net": "-15000",
      "lines": [
        {"class": "unapplied_payment",    "outcome": "pending", "earlierDay": false, "amount": "80000",  "count": 1},
        {"class": "applied_before_final", "outcome": "pending", "earlierDay": false, "amount": "-30000", "count": 1},
        {"class": "under_applied",        "outcome": "break",   "earlierDay": false, "amount": "5000",   "count": 1},
        {"class": "matched",              "outcome": "ok",      "earlierDay": true,  "amount": "-70000", "count": 1}
      ],
      "residual": "0",
      "carriedOutside": [{"class": "unapplied_payment", "outcome": "break", "amount": "50000", "count": 1}],
      "flowGross": "55000",
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
  "paymentAccounts": [
    {"account": "fpay:stripe:account:acct_eu:main", "asset": "EUR/2",
     "inputPrev": "9120000", "input": "9520000", "outputPrev": "310000", "output": "330000",
     "flowCredits": "400000", "flowDebits": "20000", "creditResidual": "0", "debitResidual": "0"}
  ],
  "files": [
    {"name": "flow.ndjson.gz",         "rows": 8, "sha256": "e41d…"},
    {"name": "carried.ndjson.gz",      "rows": 4, "sha256": "7a02…"},
    {"name": "stock.ndjson.gz",        "rows": 9, "sha256": "c9b8…"},
    {"name": "breaks.ndjson.gz",       "rows": 3, "sha256": "15fe…"}
  ],
  "expiresAt": "2026-12-24T00:00:04Z"
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
{"ref":"PAY-39","asset":"EUR/2","class":"unapplied_payment","outcome":"break","pspAmount":"50000","productAmount":"0","drift":"50000","impact":"0","firstSeen":"2026-09-20","breakOn":"2026-09-21","psp":[{"tx":1071229,"state":"payin.succeeded","amount":"50000","insertedAt":"2026-09-20T09:14:55Z"}],"product":[]}
{"ref":"PAY-40","asset":"EUR/2","class":"matched","outcome":"ok","pspAmount":"70000","productAmount":"70000","drift":"0","impact":"-70000","firstSeen":"2026-09-23","psp":[{"tx":1160874,"state":"payin.succeeded","amount":"70000","insertedAt":"2026-09-23T15:03:12Z"}],"product":[{"tx":884517,"businessId":"INV-5","holdId":"INV-5","amount":"70000","insertedAt":"2026-09-24T06:12:44Z"}]}
{"ref":"PAY-42","asset":"EUR/2","class":"matched","outcome":"ok","pspAmount":"100000","productAmount":"100000","drift":"0","impact":"0","firstSeen":"2026-09-24","psp":[{"tx":1249870,"state":"payin.pending","holdAmount":"100000","insertedAt":"2026-09-24T07:58:40Z"},{"tx":1250981,"state":"payin.succeeded","amount":"100000","insertedAt":"2026-09-24T08:01:17Z"}],"product":[{"tx":891204,"businessId":"INV-7","holdId":"INV-7","amount":"100000","insertedAt":"2026-09-24T08:05:10Z"}]}
{"ref":"PAY-43","asset":"EUR/2","class":"matched","outcome":"ok","pspAmount":"100000","productAmount":"100000","drift":"0","impact":"0","firstSeen":"2026-09-24","psp":[{"tx":1261022,"state":"payin.pending","holdAmount":"100000","insertedAt":"2026-09-24T09:20:05Z"},{"tx":1262410,"state":"payin.succeeded","amount":"100000","insertedAt":"2026-09-24T09:22:48Z"}],"product":[{"tx":893118,"businessId":"INV-8","holdId":"INV-8","amount":"60000","insertedAt":"2026-09-24T09:25:31Z"},{"tx":893119,"businessId":"INV-10","holdId":"INV-10","amount":"40000","insertedAt":"2026-09-24T09:25:31Z"}]}
{"ref":"PAY-44","asset":"EUR/2","class":"under_applied","outcome":"break","pspAmount":"120000","productAmount":"115000","drift":"5000","impact":"5000","firstSeen":"2026-09-24","psp":[{"tx":1287004,"state":"payin.pending","holdAmount":"120000","insertedAt":"2026-09-24T11:47:31Z"},{"tx":1288115,"state":"payin.succeeded","amount":"120000","insertedAt":"2026-09-24T11:50:02Z"}],"product":[{"tx":895660,"businessId":"INV-11","holdId":"INV-11","amount":"115000","insertedAt":"2026-09-24T11:55:48Z"}]}
{"ref":"PAY-45","asset":"EUR/2","class":"unapplied_payment","outcome":"pending","pspAmount":"80000","productAmount":"0","drift":"80000","impact":"80000","firstSeen":"2026-09-24","breakOn":"2026-09-25","merchantRef":"INV-12","pairedHold":"main:hold:invoice:INV-12","psp":[{"tx":1300312,"state":"payin.pending","holdAmount":"80000","insertedAt":"2026-09-24T13:10:26Z"},{"tx":1301876,"state":"payin.succeeded","amount":"80000","insertedAt":"2026-09-24T13:12:59Z"}],"product":[]}
{"ref":"PAY-46","asset":"EUR/2","class":"in_progress","outcome":"ok","pspAmount":"0","productAmount":"0","drift":"0","impact":"0","psp":[{"tx":1312455,"state":"payin.pending","holdAmount":"25000","insertedAt":"2026-09-24T20:41:09Z"}],"product":[]}
{"ref":"PAY-99","asset":"EUR/2","class":"applied_before_final","outcome":"pending","pspAmount":"0","productAmount":"30000","drift":"-30000","impact":"-30000","firstSeen":"2026-09-24","breakOn":"2026-10-01","psp":[{"tx":1309640,"state":"payin.pending","holdAmount":"30000","insertedAt":"2026-09-24T19:55:37Z"}],"product":[{"tx":899031,"businessId":"INV-13","holdId":"INV-13","amount":"30000","insertedAt":"2026-09-24T17:02:20Z"}]}
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
{"ref":"PAY-39","asset":"EUR/2","class":"unapplied_payment","outcome":"break","pspAmount":"50000","productAmount":"0","drift":"50000","firstSeen":"2026-09-20","breakOn":"2026-09-21","psp":[{"tx":1071229,"state":"payin.succeeded","amount":"50000","insertedAt":"2026-09-20T09:14:55Z"}],"product":[]}
{"ref":"PAY-44","asset":"EUR/2","class":"under_applied","outcome":"break","pspAmount":"120000","productAmount":"115000","drift":"5000","firstSeen":"2026-09-24","psp":[{"tx":1287004,"state":"payin.pending","holdAmount":"120000","insertedAt":"2026-09-24T11:47:31Z"},{"tx":1288115,"state":"payin.succeeded","amount":"120000","insertedAt":"2026-09-24T11:50:02Z"}],"product":[{"tx":895660,"businessId":"INV-11","holdId":"INV-11","amount":"115000","insertedAt":"2026-09-24T11:55:48Z"}]}
{"ref":"PAY-45","asset":"EUR/2","class":"unapplied_payment","outcome":"pending","pspAmount":"80000","productAmount":"0","drift":"80000","firstSeen":"2026-09-24","breakOn":"2026-09-25","merchantRef":"INV-12","pairedHold":"main:hold:invoice:INV-12","psp":[{"tx":1300312,"state":"payin.pending","holdAmount":"80000","insertedAt":"2026-09-24T13:10:26Z"},{"tx":1301876,"state":"payin.succeeded","amount":"80000","insertedAt":"2026-09-24T13:12:59Z"}],"product":[]}
{"ref":"PAY-99","asset":"EUR/2","class":"applied_before_final","outcome":"pending","pspAmount":"0","productAmount":"30000","drift":"-30000","firstSeen":"2026-09-24","breakOn":"2026-10-01","psp":[{"tx":1309640,"state":"payin.pending","holdAmount":"30000","insertedAt":"2026-09-24T19:55:37Z"}],"product":[{"tx":899031,"businessId":"INV-13","holdId":"INV-13","amount":"30000","insertedAt":"2026-09-24T17:02:20Z"}]}
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
{"side":"product","hold":"main:hold:invoice:INV-3","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-3","openSign":"negative","balance":"-120000","class":"open","outcome":"ok","lifecycle":"persisting","openedAt":"2026-08-14T10:02:00Z","ageDays":41,"bucket":">30d"}
{"side":"product","hold":"main:hold:invoice:INV-5","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-5","openSign":"negative","balance":"0","class":"cleared","outcome":"ok","lifecycle":"cleared","openedAt":"2026-08-21T09:40:00Z","ageDays":34,"bucket":">30d","previousBalance":"-70000","clearedAt":"2026-09-24T06:12:44Z","clearedBy":"PAY-40"}
{"side":"product","hold":"main:hold:invoice:INV-9","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-9","openSign":"negative","balance":"-50000","class":"open","outcome":"ok","lifecycle":"persisting","openedAt":"2026-09-12T07:45:00Z","ageDays":12,"bucket":"8-30d"}
{"side":"product","hold":"main:hold:refund:RF-2","asset":"EUR/2","prefix":"main:hold:refund:","holdId":"RF-2","openSign":"positive","balance":"20000","class":"open","outcome":"ok","lifecycle":"new","openedAt":"2026-09-24T14:30:00Z","ageDays":0,"bucket":"0-1d"}
{"side":"psp","hold":"fpay:stripe:payment:hold:pending:PAY-46","asset":"EUR/2","prefix":"fpay:stripe:payment:hold:pending:","holdId":"PAY-46","openSign":"positive","balance":"25000","class":"open","outcome":"ok","lifecycle":"new","openedAt":"2026-09-24T20:41:09Z","ageDays":0,"bucket":"0-1d"}
{"side":"psp","hold":"fpay:stripe:payment:hold:pending:PAY-99","asset":"EUR/2","prefix":"fpay:stripe:payment:hold:pending:","holdId":"PAY-99","openSign":"positive","balance":"30000","class":"open","outcome":"ok","lifecycle":"new","openedAt":"2026-09-24T19:55:37Z","ageDays":0,"bucket":"0-1d"}
```

INV-14 is positive while invoice holds open negative, hence `wrong_sign`. INV-3, open for 41 days,
is in the `>30d` bucket and is not a break. INV-5, open yesterday at −700.00 on the ledger, was
lettered by PAY-40 today: it stays in the file once, as `cleared`.

**`breaks.ndjson.gz`**, 3 open, 3 rows:

```text
{"breakId":"a93d02e6b7f1c448","leg":"flow","priority":2,"lifecycle":"new","openedOn":"2026-09-24","amount":"5000","ref":"PAY-44","asset":"EUR/2","class":"under_applied","outcome":"break","pspAmount":"120000","productAmount":"115000","drift":"5000","impact":"5000","firstSeen":"2026-09-24","psp":[{"tx":1287004,"state":"payin.pending","holdAmount":"120000","insertedAt":"2026-09-24T11:47:31Z"},{"tx":1288115,"state":"payin.succeeded","amount":"120000","insertedAt":"2026-09-24T11:50:02Z"}],"product":[{"tx":895660,"businessId":"INV-11","holdId":"INV-11","amount":"115000","insertedAt":"2026-09-24T11:55:48Z"}]}
{"breakId":"1c7f3a90d2e84b55","leg":"flow","priority":3,"lifecycle":"persisting","openedOn":"2026-09-21","amount":"50000","ref":"PAY-39","asset":"EUR/2","class":"unapplied_payment","outcome":"break","pspAmount":"50000","productAmount":"0","drift":"50000","impact":"0","firstSeen":"2026-09-20","breakOn":"2026-09-21","psp":[{"tx":1071229,"state":"payin.succeeded","amount":"50000","insertedAt":"2026-09-20T09:14:55Z"}],"product":[]}
{"breakId":"7b24e1f09c3d6a12","leg":"stock","priority":4,"lifecycle":"persisting","openedOn":"2026-09-21","amount":"-10000","side":"product","hold":"main:hold:invoice:INV-14","asset":"EUR/2","prefix":"main:hold:invoice:","holdId":"INV-14","openSign":"negative","balance":"10000","class":"wrong_sign","outcome":"break","openedAt":"2026-09-18T16:20:00Z","ageDays":6,"bucket":"2-7d"}
```

A stock break's `amount` reads in the open direction: INV-14 is 100.00 on the wrong side. No break
is resolved today: INV-5, which PAY-40 lettered, was an open hold, not a break.

**The statement**, as a UI may present it. It is an example of presentation, not an engine
output: the engine writes no text. The figures come from the manifest, whose `statement` block and
`counts` the alert's evidence carries; the breaks and the pending items come from the breaks and
flow files, through recon's API.

```text
psp-vs-billing — 24 Sep 2026 (cut-off 23:59:59 Europe/Paris) — BREAKS
3 open breaks (2 flow, 1 stock), 0 at P1 · open flow breaks 550.00 EUR gross, net −150.00 EUR

EUR
  PSP — finalised payments in the window                           4,000.00  (4)
− Product — applications in the window                             4,150.00  (6)
= Net difference                                                     −150.00
  explained by:
    + unapplied payments of the window (within product.grace)        +800.00  (1)  pending
    − applications awaiting a final PSP state (within psp.grace)     −300.00  (1)  pending
    ± under / over applications                             P2        +50.00  (1)
    ± matched, entered the join on an earlier day                    −700.00  (1)
  = unexplained residual                                                0.00  ✓
Carried from earlier days, outside the window's net:
    unapplied payments past product.grace                   P3        500.00  (1)
Gross open flow breaks: Σ|drift| = 550.00

Open items (psp − product)
  at the previous cut (23 Sep)                                     1,200.00  (2)
+ net difference of the window                                       −150.00
± entered through a lookup                                              0.00
= at this cut, handed to the next run                              1,050.00  (4)  ✓

Open books at the cut               total      count   0–1d  2–7d  8–30d  >30d   continuity
  PSP pending holds                  550.00       2       2     —     —      —      ✓
  Product invoices                 2,450.00       5       —     2     2      1      ✓   1 wrong_sign
  Product refunds                    200.00       1       1     —     —      —      ✓
  Lettered outside matching: none

⚠ Unclassified: 1 PSP transaction with state "payin.refunded", 200.00 — the rule's state sets or the
  connector mapping need attention.
Detail: {bucketID}/reconciliation/rule=psp-vs-billing/day=2026-09-24/run=r-20260925T000004Z/
```

The breaks, from `breaks.ndjson.gz` (3 open, none resolved), and the pending items, from the
`pending` rows of `flow.ndjson.gz`:

```text
Open breaks
  P2  under_applied       PAY-44 → INV-11, short by 50.00   new
  P3  unapplied_payment   PAY-39, 500.00, since 20 Sep      persisting
  P4  wrong_sign          INV-14, 100.00 over-applied       persisting
Pending (not breaks)
  PAY-45, 800.00, a break on 25 Sep — its merchant reference names INV-12: apply it.
  PAY-99 → INV-13, 300.00, applied before the PSP finalised it — an orphan application (P1) on
  1 Oct unless the PSP finalises it.
```

- **The PSP total** counts the four payments finalised in the window: PAY-42, 43, 44 and 45.
- **The product total** counts the six applications booked in it. That includes PAY-40's, whose
  payment was finalised the day before, and PAY-99's, whose payment the PSP has not finalised yet.
- **Why `BREAKS` and not `INCOMPLETE`.** Every difference is explained, so the residual is zero,
  and the verdict is `BREAKS`. Without the breaks, the unclassified refund would make it
  `RECONCILED_WITH_WARNINGS`, not green.
