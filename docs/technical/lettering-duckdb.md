# Lettering DuckDB tool

`tools/lettering-duckdb` checks and queries the result files of a transaction-level (lettering)
rule, format `lettering/1`. This page explains how the tool is built and how to use it. The files
themselves, and every field, are specified in
[transaction-level-results.md](./transaction-level-results.md), the source of truth. When the tool
and that page disagree, the page wins and the tool is fixed.

🚧 Internal tool ([EN-2353](https://formance-team.atlassian.net/browse/EN-2353)). It runs today on
generated test data. Recon does not write these files yet: that is EN-2322.

## 1. What it is for

The team uses the tool for two jobs, during implementation, QA and support:

- **Validate the data.** Do a run's files keep every rule of the format? For example, do the
  counts match the files, does the bridge close, does the run chain onto the previous one?
- **Analyse the files.** Answer the reconciliation questions: which payments were reconciled on a
  day, why the day's net is not zero, what is left to act on, where an invoice stands.

It is not shipped to customers. They read the files with their own tools, and the results doc §9
gives them standalone examples. It is also not part of recon: the service asserts the format's
identities in its own Go tests, and recon's CI does not depend on DuckDB.

## 2. Quick start

The repository's Nix shell provides the DuckDB CLI (1.4.3). The tool also works with any DuckDB
1.4 or later, and needs a POSIX shell.

```bash
nix develop --impure
```

Check one run, then the link with the run before it:

```bash
tools/lettering-duckdb/lettering check tools/lettering-duckdb/testdata/rule=psp-vs-billing/day=2026-09-24/run=r-20260925T000004Z
```

```bash
tools/lettering-duckdb/lettering check-chain tools/lettering-duckdb/testdata/rule=psp-vs-billing/day=2026-09-23/run=r-20260924T000003Z tools/lettering-duckdb/testdata/rule=psp-vs-billing/day=2026-09-24/run=r-20260925T000004Z
```

Ask a question over every day of a rule:

```bash
tools/lettering-duckdb/lettering query open-breaks tools/lettering-duckdb/testdata/rule=psp-vs-billing
```

List the queries:

```bash
tools/lettering-duckdb/lettering queries
```

## 3. How it is built

### Why DuckDB

- **No loading step.** DuckDB reads the files where they are: gzipped NDJSON, the
  `rule=/day=/run=` layout as columns, and a local disk or object storage. There is no database
  and no service.
- **Exact amounts.** Amounts are integer minor units written as strings. DuckDB reads them as
  `HUGEINT`, so no figure goes through a float.
- **Plain SQL.** A check or a question is a SQL file anyone can read, rerun or pass to a colleague.
  The same files run from the CLI, Python or any other DuckDB client.

### Layout

```text
tools/lettering-duckdb/
  lettering            the shell wrapper: arguments, pre-checks, exit codes
  sql/schema.sql       readers, one table macro per file kind
  sql/run.sql          views over ONE run (variable `run`)
  sql/rule.sql         views over MANY days of a rule, current runs only (variable `rule`)
  check.sql            the rules one run's files can show
  check-chain.sql      the rules that tie a run to the previous one
  queries/*.sql        one reconciliation question per file
  test.sh              the test suite
  testdata/            three rules of result files, their expected query results, and generate.py
```

The SQL comes in layers. Each layer only uses the ones before it:

```mermaid
flowchart LR
    S[schema.sql<br/>readers] --> R1[run.sql<br/>one run]
    S --> R2[rule.sql<br/>many days]
    R1 --> C[check.sql]
    R1 --> CC[check-chain.sql]
    R2 --> Q[queries/*.sql]
    W[lettering<br/>wrapper] -. runs .-> C
    W -. runs .-> CC
    W -. runs .-> Q
```

### Readers (`sql/schema.sql`)

There is one table macro per file kind: `flow_file(path, hive)`, `stock_file` and `breaks_file`.
The carried file has the flow file's columns. Four choices make them reliable:

- **Declared columns.** Each column and its type is declared, never inferred. A complete run writes
  every data file, even with no row, and an empty file still has its columns. A key missing from a
  row reads as `NULL`, so a field added within `lettering/1` does not break a reader.
- **Compression stated.** The files are read as gzip without looking at their name.
- **Row position.** `WITH ORDINALITY` gives each row its position in the file. The order checks
  compare keys in that order, since DuckDB does not guarantee the order of rows it returns.
- **Path columns on demand.** The `hive` flag reads `rule`, `day` and `run` from the path. It is
  on for the multi-day views and off for one run, where those columns would be duplicated.

The file also holds `open_dir(balance, openSign)`, which reads a balance in the hold's open
direction, and the JSON shapes used to flatten the manifest's arrays.

### One run (`sql/run.sql`)

- **Variable.** `run` is the run's directory.
- **Data views.** `manifest`, `flow`, `carried`, `stock` and `breaks`. Each data view
  adds `file` (the file's name) and `pos` (the row's position).
- **Manifest views.** The manifest, flattened: `m_run`, `m_files`, `m_cuts`, `m_statement`,
  `m_lines`, `m_carried_outside`, `m_books`, and `m_payment_accounts` with `m_payment_directions`
  (the payment-account book, one row per account and asset, then per direction).

### Many days (`sql/rule.sql`)

- **Variable.** `rule` is the rule's directory.
- **Runs.** `runs` reads every manifest under `day=*/run=*`.
- **Current runs.** `current_runs` keeps, per day, the latest run that is not incomplete. Run ids
  sort by start instant, and an incomplete run writes a reduced manifest and no data file, so this
  is the results doc's current run (§2), even when a failed run came last.
- **Day views.** `flow_days`, `carried_days`, `stock_days` and `breaks_days` keep only the current
  runs' rows, with `day`, `run` and `rule` from the path.
- **Manifest views.** `statement_days`, `books_days` and `cuts_days` flatten the current runs'
  manifests. `cuts_days` gives each side's transaction window, which tells what the day itself
  booked.
- **Target day.** `lettering_target_day()` is the `day` variable, or else the latest day with a
  current run.
- **Guard.** The last statement fails when `day` names a day with no complete run. That day's
  activity is in the next complete run's window, so an empty answer would mislead.

### Checks (`check.sql`, `check-chain.sql`)

A check is a list of `INSERT INTO violations` statements, one per rule of the results doc, each
selecting the rows that break the rule with a rule name, a key and a detail. The final statement
prints the violations, then raises a DuckDB `error()` if there is any. So the SQL fails on a
violation in any client, and the wrapper turns that failure into exit status 1.

- **Independent computations.** A rule never trusts the figure it checks. The residual is
  recomputed from the applications booked in the product window (`cuts`), not read from the
  statement. The books are compared with the stock rows, and the verdict is derived from the files.
- **Rule names.** Every rule name contains an underscore (`bridge_net`, `row_order`). The test
  suite uses this to find the rule names and to check that each one fires on at least one corrupted
  copy (see [Tests](#tests-testsh)).
- **What the files cannot show.** The checks read the files, not the ledgers. They cannot tell
  that a transaction is missing from both the flow and the books. Recon's continuity and residual
  checks cover that, when it computes the run (results doc §5).

`check-chain.sql` also reads the earlier run: its manifest, carried, stock and breaks files. It
checks that the later run picks up exactly where the earlier one stopped.

### Queries (`queries/*.sql`)

Each query starts with three comment lines, which the wrapper reads:

```sql
-- What must be done today? The open breaks, most urgent first.
-- Variables: rule, day (optional, default the latest day).
-- Columns: priority, class, lifecycle, asset, amount, ref, hold, opened_on, ...
```

- **First line.** The question, which `lettering queries` prints.
- **`Variables:` line.** The variables the query takes. The wrapper refuses any other variable,
  and a variable marked `(required` must be given.
- **`Columns:` line.** The output columns.

A query reads only the `*_days` views and `lettering_target_day()`. A query about one day, such as
the applications booked on that day, filters on `cuts_days`. A carried row's history would
otherwise count again on every day it is carried.

### The wrapper (`lettering`)

The wrapper is a POSIX `sh` script. It concatenates the SQL layers and pipes them into the DuckDB
CLI. It also handles what SQL cannot:

- **Directory checks.** Before running anything, it refuses a directory that is not a run's or a
  rule's, and it checks an incomplete run's shape on its own (§4, "Incomplete run").
- **Quoting.** A trailing slash is removed from a directory, and a quote in a value is escaped.
- **Init file and exit status.** It runs `LETTERING_INIT` first and maps each outcome to an exit
  status (§6).
- **CSV output.** `LETTERING_MODE=csv` goes through `COPY … TO '/dev/stdout'`. The CLI's own CSV
  mode drops the header of an empty result and writes `NULL` for a missing value.

### Test data and the reference engine

`testdata/generate.py` writes all the test data. It uses the Python standard library only, is
deterministic, and never calls DuckDB:

```bash
python3 tools/lettering-duckdb/testdata/generate.py
```

- **`rule=psp-vs-billing`** is the results doc's worked example (§10). Its NDJSON lines are the
  doc's, byte for byte. The run of 23 September holds only what `check-chain` reads: a manifest
  with its cuts, so that `window_start` compares 23 and 24 September, and its carried, stock and
  breaks files.
- **`rule=qa-scenarios`** is seven days in two assets, one scripted story per case. It covers every
  flow and stock class, lookups on both ledgers, `fromLookups`, a split payment, a credit
  note, an unclassified transaction and refunds. Three stories undo an
  application with its reference, which the class must read on net amounts (results doc §6):

  | Story | What happens | Rows |
  |---|---|---|
  | S20 | Paid and applied on 4 Oct, undone on 5 Oct | `matched`, then `unapplied_payment`: a P3 break at once, since its `firstSeen` stays 4 Oct |
  | S21 | Paid and applied on 2 Oct; on 5 Oct the PSP fails the payment and the product undoes the application | `matched`, then `failed`, `ok`, drift 0: no break |
  | S22 | Applied on 5 Oct while the PSP is `pending`, undone on 6 Oct, still `pending` | `applied_before_final` (pending, carried), then `in_progress`, `ok`, no `firstSeen`, with a bridge line of +19.00 in the window |

  The first run, on 1 Oct, compares that day only. Its open items are seeded from `backfillFrom`,
  29 Sep, up to the cut of 30 Sep, with the product side from 26 Sep, and its starting stock and
  payment account are those at that cut (results doc §2):

  | Story | What happens | Rows |
  |---|---|---|
  | S23 | Paid on 30 Sep, applied on 1 Oct | seeded as an `unapplied_payment`, then `matched` on an earlier day; its invoice is `cleared` on 1 Oct |
  | S25 | Applied on 30 Sep while the PSP is `pending`, finalised on 2 Oct | seeded as `applied_before_final`, carried outside the net on 1 Oct, `matched` on an earlier day on 2 Oct; its PSP hold, open at the start, is `persisting`, then `cleared` |
  | S12 | Paid on 28 Sep, before the PSP seed, applied on 3 Oct | not seeded: a PSP lookup finds the payment, so `fromLookups` is 50.00 |
  | S24 | An invoice opened on 15 Sep, before the product seed, never paid | `openedAt` null, `ageDays` a lower bound from `backfillFrom`: 2 days on 1 Oct, in `2-7d`, for an invoice 16 days old |
  | S26 | A PSP `pending` on 27 Sep, before the PSP seed, finalised and applied on 2 Oct | its hold has a null `openedAt`, open on 1 Oct, `cleared` on 2 Oct |

  Two more stories move the PSP payment account with no key, so that the flow read cannot return
  them (results doc §5, the payment-account book):

  | Story | What happens | Breaks |
  |---|---|---|
  | K01 | A final with no pending and no reference credits the account 18.00 on 4 Oct | `unkeyed_payment_movement` on `credit`, P1, new on 4 Oct, resolved on 5 Oct |
  | K02 | Payouts without their movement key debit it 7.00 on 5 Oct and 3.00 on 6 Oct | `unkeyed_payment_movement` on `debit`, new on 5 Oct, persisting on 6 Oct with 3.00, resolved on 7 Oct |

- **`rule=qa-verdicts`** is one week through every verdict, with an empty day and two incomplete
  runs: a `missing_index` one, whose `cuts` holds the PSP side only, retried the same day, and a
  `short_range` one followed by a two-day window. Its first run, on 5 Oct, has `backfillFrom`
  5 Oct, so it seeds nothing, and its `txFrom` is 0 on both sides: no transaction precedes that
  day, and ids start at 1.

The two qa rules come from a small reference engine in `generate.py` that follows the results doc.
It also writes `expected/`, the CSV each query must return, computed in Python. A per-day query
is computed for three chosen days: two of `qa-scenarios` and the replayed day of `qa-verdicts`. The Python engine
and the SQL are two independent readings of the doc, so a test passes only when they agree. A
disagreement found this way is fixed in the doc first, then in the side that was wrong. Once
EN-2322 writes result files from the same scenarios, they are compared with this data.

### Tests (`test.sh`)

```bash
just lettering-duckdb-tests
```

The suite checks four things:

1. **Soundness.** Every run passes `check`, and every run chains onto the run its manifest names.
2. **Answers.** Every query returns exactly the CSV in `expected/`.
3. **Every rule fires.** Each rule of `check.sql` and `check-chain.sql` fires on at least one
   corrupted copy of a run. The suite fails when a rule is never exercised, so a new rule needs a
   test.
4. **Variants and wrong arguments.**
   - Legitimate variants pass: a field unknown to `lettering/1`, and an incomplete run.
   - Wrong arguments get their exit status and a message that says why: an unknown or missing
     variable, a wrong directory, a bad or missing day, an unreadable file. An init file that
     prints and an empty CSV result are handled too.

The suite runs on the DuckDB the Nix shell pins (1.4.3). It is not part of `just tests`, and it
needs only the DuckDB CLI, `gzip`, `sed`, `sort` and `comm`. It runs on local files: object storage
is not covered.

## 4. Using it

### Validate a run

```bash
tools/lettering-duckdb/lettering check <run-dir>
```

A run directory is `…/rule=<id>/day=<YYYY-MM-DD>/run=<runId>`, local or `s3://`. A sound run prints
an empty violation table, then `ok: the run is sound`, and exits 0. A run of 200,000 payments with
27-character references checks in about 3 seconds; its flow file weighs 14 MB, close to the 74 B a
row of design doc §7.14.

A violation prints one row per broken rule and key, then exits 1. Here the drift of a matched
payment was changed by hand from 0 to 1:

```text
rule            key                detail
carried_vs_flow PAY-42/EUR/2       flow row with drift 1 is not carried
file_sha256     flow.ndjson.gz     manifest de43852e…, file 5c32104a…
row_amounts     flow PAY-42/EUR/2  psp 100000 product 100000 drift 1, transactions give psp 100000 product 100000
row_drift       flow PAY-42/EUR/2  matched ok with drift 1
Invalid Input Error: 4 violation(s) of the lettering/1 rules
```

One fault usually breaks several rules. Read the rows by key: the rules named together point to
the fault, and `file_sha256` says whether the file was changed after the manifest was written.

- **Incomplete run.** `check` prints its reason and checks the run's shape only: no data file
  (`incomplete_files`), and a reduced manifest (`incomplete_fields`), with every field the results
  doc §6 requires and no `counts`, `statement`, `books`, `paymentAccounts` or `files`. The data is
  in the day's current run, which `current-runs` names.
- **Missing file.** A data file that is absent is reported as `file_missing`: a complete run writes
  every data file, even empty.

### Validate the chain

```bash
tools/lettering-duckdb/lettering check-chain <earlier-run-dir> <run-dir>
```

The earlier run is the one the later manifest's `previousRun` names: the current run of the most
recent earlier day, which is not always the day before. The command checks the
[rules of `check-chain`](#rules-of-check-chain) (§6).

It refuses an incomplete run on either side: an incomplete run is not a link in the chain. A
restarted rule's first run has no `previousRun`, so there is no chain to check.

### Answer a reconciliation question

```bash
tools/lettering-duckdb/lettering query <name> <rule-dir> [variable=value ...]
```

A query runs over every day of the rule and keeps each day's current run. `day` defaults to the
latest day with a current run. A day with no complete run is refused rather than answered with
nothing.

| Question | Query | Variables |
|---|---|---|
| Which run counts for each day, and why did an incomplete one conclude nothing? | `current-runs` | |
| Which payments were reconciled each day, and what else did the flow contain? | `daily-flow` (the `matched` rows are the reconciled payments) | |
| Why is the day's net difference what it is? | `bridge` | `day` |
| What must be done today? | `open-breaks` | `day` |
| What turns into a break soon, and on which day? | `pending` | `day` |
| Where does this invoice, refund or payment stand? | `business-id` | `id` (required) |
| How much is still unmatched, day after day? | `open-items` | |
| What is open on each ledger, day after day? | `books` | |
| How old is what waits for payment? | `stock-ageing` | |
| Which holds did a credit note or a write-off letter to zero? | `lettered-other` | |
| Which applications were booked on the day? | `applications` | `day` |

`open-breaks` on the worked example, 24 September, printed with `LETTERING_MODE=markdown`:

| priority | class | lifecycle | amount | ref | hold | opened_on | days_open | detail |
|---:|---|---|---:|---|---|---|---:|---|
| 2 | under_applied | new | 5000 | PAY-44 | | 2026-09-24 | 0 | INV-11 |
| 3 | unapplied_payment | persisting | 50000 | PAY-39 | | 2026-09-21 | 3 | |
| 4 | wrong_sign | persisting | -10000 | | main:hold:invoice:INV-14 | 2026-09-21 | 3 | 6 days old |

Amounts are in minor units of their asset: `EUR/2` 5000 is 50.00 EUR.

### Export to a spreadsheet

`LETTERING_MODE=csv` writes a standard CSV. It has a header even when there is no row, and empty
fields for missing values.

```bash
LETTERING_MODE=csv tools/lettering-duckdb/lettering query applications ./rule=psp-vs-billing day=2026-09-24 > applications.csv
```

`LETTERING_MODE` also takes any output mode of the DuckDB CLI: `json`, `markdown`, `line`…

### Write your own query

Start a DuckDB session with the rule's views:

```bash
duckdb -cmd "SET VARIABLE rule = 'tools/lettering-duckdb/testdata/rule=qa-scenarios'" -cmd ".read tools/lettering-duckdb/sql/schema.sql" -cmd ".read tools/lettering-duckdb/sql/rule.sql"
```

Loading `rule.sql` prints one `lettering_day_check` row, empty: it is the guard on `day`. Then
query the views:

| View | One row per |
|---|---|
| `current_runs` (and `runs`, every run) | day: its current run, its verdict, and its manifest as JSON (`m`) |
| `flow_days`, `carried_days` | day, payment reference and asset |
| `stock_days` | day and hold, open or cleared since the previous run |
| `breaks_days` | day and break, open or resolved since the previous run |
| `statement_days` | day and asset: the statement as JSON (`s`) |
| `books_days` | day, side, prefix and asset: the open books |
| `cuts_days` | day and side: the transaction window (`txFrom`, `txTo`] |

For example, the payments still unmatched after three days, all days together:

```sql
SELECT day, ref, class, drift, firstSeen
FROM carried_days
WHERE day = (SELECT max(day) FROM current_runs) AND day - firstSeen > 3
ORDER BY abs(drift) DESC;
```

Nested lists unfold with `unnest`. For example, `unnest(product)` in a flow row gives one row per
application, with its `holdId` and `amount`. Once a query is worth keeping, add it to `queries/`
(§5).

### Read from object storage

DuckDB loads its `httpfs` extension the first time it reads an `s3://` path. Credentials come from
a secret, in the file `LETTERING_INIT` names:

```sql
CREATE SECRET (TYPE s3, PROVIDER credential_chain);
```

```bash
LETTERING_INIT=s3.sql tools/lettering-duckdb/lettering query open-items s3://bucket/{bucketID}/reconciliation/rule=psp-vs-billing
```

- **Azure.** Azure storage works the same way, with DuckDB's `azure` extension and an `az://`
  path.
- **Pre-signed URLs.** The tool reads directories only. Download the files recon's API lists into
  a directory first, keeping the `rule=/day=/run=` layout.

### Use the SQL from Python

The SQL files use no CLI dot-command, so any DuckDB client runs them: set the variables, then run
the layers in order.

```python
import duckdb

con = duckdb.connect()
con.execute("SET VARIABLE rule = 'tools/lettering-duckdb/testdata/rule=psp-vs-billing'")
for path in ["sql/schema.sql", "sql/rule.sql"]:
    con.execute(open(f"tools/lettering-duckdb/{path}").read())
print(con.sql(open("tools/lettering-duckdb/queries/open-breaks.sql").read()).df())
```

## 5. Maintaining it

- **A new rule.** Add an `INSERT INTO violations` to `check.sql` (or `chain_violations` to
  `check-chain.sql`), named with an underscore and citing the results doc section. Add a corrupted
  copy to `test.sh` that makes it fire; the suite fails until you do. Add the rule to the table in
  §6.
- **A new query.** Add `queries/<name>.sql` with its three header lines. Compute its expected
  result in `generate.py`, which writes `expected/<name>.csv` (or `<name>_<variable>=<value>.csv`),
  then regenerate the test data. Add the query to the table in §4.
- **A change to the format.** Change the results doc first. Then change the readers, the checks,
  `generate.py` and the expected results, in the same PR. A new optional field needs a column in
  the reader only if a check or a query uses it. A change of `schemaVersion` changes the whole
  pack.
- **A change in `generate.py`.** Regenerate and review the diff of `testdata/`: the worked example
  must stay byte for byte the results doc's.

## 6. Reference

### Exit status

| Status | Meaning |
|---|---|
| 0 | The run is sound, or the query ran |
| 1 | A rule is violated |
| 2 | Wrong arguments: an unknown command, query or variable, a missing required variable, a wrong kind of directory |
| 3 | The files could not be read, or DuckDB failed (a day with no complete run, a value that is not a date…) |

### Environment

| Variable | Effect |
|---|---|
| `DUCKDB` | The DuckDB CLI to run (default: `duckdb` on `PATH`) |
| `LETTERING_INIT` | A SQL file run first, such as an object-storage secret. Its output is discarded |
| `LETTERING_MODE` | `csv` for a standard CSV, or any output mode of the DuckDB CLI |

### Rules of `check`

| Rule | What it checks |
|---|---|
| `schema_version` | The manifest's `schemaVersion` is `lettering/1` |
| `incomplete_files`, `incomplete_fields` | An incomplete run has no data file, and its reduced manifest has every field the results doc §6 requires and none it leaves out. The wrapper checks these before any SQL runs, and stops there |
| `file_missing`, `file_unlisted`, `file_rows`, `file_sha256` | The manifest lists exactly the files present, with their row counts and SHA-256 |
| `counts_flow`, `counts_flow_outcome`, `counts_stock`, `counts_breaks` | The manifest's counts match the files |
| `counts_unclassified` | The manifest's unclassified counts per side match the statement's unclassified lines (the run writes no file for them, results doc §6) |
| `bridge_net`, `bridge_totals`, `bridge_line`, `bridge_carried_outside`, `bridge_gross` | The net is `SUM(impact)` and `psp − product`; each line matches the flow rows; the carried lines and the gross match |
| `bridge_residual`, `bridge_product_vs_books` | The residual is 0, recomputed from the applications booked in the product window, and the product total equals the books' `lettered − letteredOther` |
| `carried_vs_flow`, `suspense_open`, `suspense_identity` | The carried file holds exactly the flow rows whose drift is not 0; the open items equal its sum and count, and `open = openPrev + net + fromLookups` |
| `books_continuity`, `books_vs_stock` | Each book closes, and equals its open stock rows in the open direction |
| `book_residual` | Each payment-account residual is the account's movement since the previous cut minus the flow's (`input − inputPrev − flowCredits`, `output − outputPrev − flowDebits`), and its volumes never go down |
| `breaks_vs_rows`, `break_vs_row` | Every open break of the flow and stock files, and every non-zero residual of the payment-account book, is in the breaks file, and back, with the same class and amount; a book break, open or resolved, carries its account's `paymentAccounts` entry as it stands |
| `break_amount`, `break_outcome`, `break_priority` | What `break_vs_row` cannot see: an open book break's amount is its direction's residual and a resolved one's residual is 0 again, and a resolved stock break keeps its hold's last open balance; a break's outcome and priority follow its lifecycle and class |
| `row_drift`, `row_break_on` | A pending or break row has a drift and a matched, in-progress or failed one has none; `breakOn` is `firstSeen` plus the lagging side's grace |
| `stock_age`, `books_buckets` | A hold's `ageDays` is counted in the rule's timezone from its `openedAt`, or from `rule.backfillFrom` when `openedAt` is null (a lower bound), and its bucket and each book's bucket counts follow from the engine's fixed buckets (0-1d, 2-7d, 8-30d, >30d) |
| `row_amounts`, `row_impact`, `row_outcome` | A flow row's amounts follow from its transactions, a carried row has no `impact`, and each row's outcome (and a stock row's sign) follows from its class |
| `row_class` | A flow row's class follows from its net amounts: applications that sum to 0 count as none |
| `unique_key`, `row_order` | Each file's unique key and row order (results doc §8) |
| `verdict_mismatch` | The verdict follows from the files and, for the warnings, the manifest's unclassified counts |

### Rules of `check-chain`

| Rule | What it checks |
|---|---|
| `previous_run` | `previousRun` names the earlier run: run id, day and manifest SHA-256 |
| `window_start` | Each side's window starts at the earlier run's cut |
| `carried_in`, `carried_drift` | Every carried item shows up again, and its drift moves only by this window's impact |
| `from_lookups` | `fromLookups` equals the drift of the rows not carried in |
| `suspense_open_prev`, `books_open_prev` | The open items and the books pick up where the earlier run left them |
| `book_prev` | The payment-account book picks up too: `inputPrev` and `outputPrev` are the earlier run's `input` and `output`, and an account and asset listed then are still listed |
| `break_lifecycle`, `stock_lifecycle` | Each break and hold is new, persisting, resolved or cleared as the earlier run implies, with its `openedOn` and the cleared balance; an open break is never lost, even when its class changes |
