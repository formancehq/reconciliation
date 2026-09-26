# lettering-duckdb

Checks and queries for the result files of a transaction-level (lettering) rule, format
`lettering/1`. The files and every field are specified in
[transaction-level-results.md](../../docs/technical/transaction-level-results.md), the source of
truth.

This is an internal tool ([EN-2353](https://formance-team.atlassian.net/browse/EN-2353)). The team
uses it to validate the data of a run's files, during implementation, QA or support, and to
analyse them to answer reconciliation questions: which payments were reconciled on a day, why a
day's net is not zero, what is left to act on, where an invoice stands. It is not a product
shipped to customers, who read the files with their own tools (results doc §9).

It is SQL run by the [DuckDB](https://duckdb.org) CLI, plus a small shell wrapper. There is no
compiled binary, and recon itself does not depend on it: the service, once built (EN-2322), asserts
the format's identities in its own Go tests. DuckDB reads the gzipped NDJSON and the
`rule=/day=/run=` layout as they are, locally or on object storage.

## Requirements

The DuckDB CLI, 1.4 or later (the tests pass on 1.4.3 and 1.5.5), and a POSIX shell (tested
with the macOS `sh` and with `dash`). On Windows, run the SQL files directly (see below). The
repository's Nix shell provides the pinned version (1.4.3):

```bash
nix develop --impure
```

Elsewhere, install it from [duckdb.org](https://duckdb.org/docs/installation/), or point `DUCKDB`
at a binary.

## Commands

```bash
tools/lettering-duckdb/lettering check <run-dir>
```

Checks one run's files against the rules of the results doc that the files themselves can show,
and prints one row per violated rule, then `ok: the run is sound`. It exits 1 when a rule is
violated.
- A missing data file is reported as `file_missing`: a complete run writes every data file, even
  empty.
- An incomplete run writes its manifest only, so the command prints its reason and checks only
  that no data file exists.
- A directory that holds no `manifest.json` is refused with the kind of directory to pass.

On a day of 200,000 payments (a 7 MB flow file), `check` takes about 2 seconds.

```bash
tools/lettering-duckdb/lettering check-chain <earlier-run-dir> <run-dir>
```

Checks that a run chains onto the run its manifest's `previousRun` names, which must be complete.
It reads the earlier run's manifest, carried, stock and breaks files, and checks:
- `previousRun` itself: run id, day and manifest SHA-256;
- that the window starts at the earlier run's cut;
- that every carried item shows up again, and its drift moves only by this window's impact;
- that `fromLookups` equals the drift of the rows not carried in;
- that the open items and books pick up where the earlier run left them;
- the lifecycle of every break and hold: new, persisting, resolved or cleared, the `openedOn`, the
  `previousClass` and the cleared balance.

```bash
tools/lettering-duckdb/lettering query <name> <rule-dir> [variable=value ...]
```

Runs one query of the pack over every day of a rule, keeping each day's current run (its latest
complete run). `lettering queries` lists them.
- A variable the query does not declare is refused, and so is a required one left out.
- A `day` with no complete run is refused rather than answered with nothing: its activity, if any,
  is in the next complete run's window, as `current-runs` shows.

A run directory is `…/rule=<id>/day=<YYYY-MM-DD>/run=<runId>`, and a rule directory is
`…/rule=<id>`. Both can be local paths or `s3://` URLs. A trailing slash, and the glob characters
`[`, `*` and `?` in a directory's name, are handled.

| Exit status | Meaning |
|---|---|
| 0 | The run is sound, or the query ran |
| 1 | A rule is violated |
| 2 | Wrong arguments: an unknown command, query or variable, a missing required variable, a wrong kind of directory |
| 3 | The files could not be read, or DuckDB failed (a day with no complete run, a value that is not a date…) |

| Environment | Effect |
|---|---|
| `DUCKDB` | The DuckDB CLI to run (default: `duckdb` on `PATH`) |
| `LETTERING_INIT` | A SQL file run first, for example the object-storage secret below. Its output is discarded |
| `LETTERING_MODE` | `csv`: a standard CSV, with a header even when there is no row and empty fields for missing values, ready for a spreadsheet or a BI tool. Otherwise any output mode of the CLI: `json`, `markdown`… |

For example, the applications of 24 September as CSV:

```bash
LETTERING_MODE=csv tools/lettering-duckdb/lettering query applications ./rule=psp-vs-billing day=2026-09-24 > applications.csv
```

## The query pack

One query per reconciliation question:

| Query | Question | Variables |
|---|---|---|
| `current-runs` | Which run counts for each day, and why an incomplete one concluded nothing? | |
| `daily-flow` | What did each day's flow contain, class by class? The `matched` rows are the day's reconciled payments | |
| `bridge` | Why is the day's net difference what it is? Rebuilds the statement's bridge from the flow file | `day` |
| `open-breaks` | What must be done today? Open breaks not accepted, most urgent first | `day` |
| `pending` | What turns into a break soon, and on which day? | `day` |
| `business-id` | Everything about one invoice, refund or payment reference, over every day | `id` (required) |
| `open-items` | How much is still unmatched, day after day? | |
| `books` | What is open on each ledger, day after day, in each hold's open direction? | |
| `stock-ageing` | How old is what waits for payment? Open holds per age bucket, day after day | |
| `lettered-other` | What was lettered outside matching (credit notes, write-offs)? | |
| `applications` | One row per product application booked on the day, for a spreadsheet | `day` |
| `psp-events` | One row per PSP event of the day that takes part in matching, for a spreadsheet | `day` |

`day` defaults to the latest day with a current run. Each file starts with its question, its
variables and its output columns.

## What `check` verifies

| Rule | What it checks |
|---|---|
| `schema_version` | The manifest's `schemaVersion` is `lettering/1` |
| `file_missing`, `file_unlisted`, `file_rows`, `file_sha256` | The manifest lists exactly the files present, with their row counts and SHA-256 |
| `counts_flow`, `counts_flow_outcome`, `counts_stock`, `counts_breaks`, `counts_unclassified` | The manifest's counts match the files |
| `bridge_net`, `bridge_totals`, `bridge_line`, `bridge_carried_outside`, `bridge_gross` | The bridge: the net is `SUM(impact)` and `psp − product`; each line matches the flow rows; the carried lines, the gross and the offsetting flag match |
| `bridge_residual`, `bridge_product_vs_books` | The residual is 0, recomputed from the applications booked in the product window (`cuts`), and the product total equals the books' `lettered − letteredOther` |
| `statement_unclassified` | The statement's unclassified totals match the file |
| `carried_vs_flow`, `suspense_open`, `suspense_identity` | The carried file holds exactly the flow rows whose drift is not 0; the open items equal its sum and count, and `open = openPrev + net + fromLookups` |
| `books_continuity`, `books_vs_stock` | Each book closes, and equals its open stock rows in the open direction |
| `breaks_vs_rows`, `break_vs_row` | Every open break of the flow and stock files is in the breaks file, and back, with the same class and amount |
| `break_amount`, `break_outcome`, `break_priority` | A break's amount, outcome and priority follow its leg, lifecycle and class |
| `row_drift`, `row_break_on` | A pending or break row has a drift and a matched, in-progress or failed one has none; `breakOn` is `firstSeen` plus the lagging side's grace |
| `stock_age`, `books_buckets` | A hold's `ageDays` is counted in the rule's timezone, it is `stuck` exactly when older than its side's `maxAge`, and its bucket, like each book's bucket counts, follows from the rule's bounds |
| `row_amounts`, `row_impact`, `row_outcome` | A flow row's amounts follow from its transactions, a carried row has no `impact`, and each row's outcome (and a stock row's sign) follows from its class |
| `triage_break`, `triage_pending`, `triage_count` | The manifest's triage matches the breaks and the pending flow rows, and lists the first `topK` open breaks, pending rows and resolved breaks |
| `unique_key`, `row_order` | Each file's unique key and row order (results doc §8) |
| `verdict_mismatch` | The verdict follows from the files |

## Using the SQL without the wrapper

The SQL files are plain DuckDB SQL, with no CLI dot-command, so any DuckDB client runs them. Set
the variables, run `sql/schema.sql`, then `sql/run.sql` (one run, variable `run`) or
`sql/rule.sql` (many days, variable `rule`), then a check or a query. In Python:

```python
import duckdb

con = duckdb.connect()
con.execute("SET VARIABLE rule = 's3://bucket/prefix/reconciliation/rule=psp-vs-billing'")
for path in ["sql/schema.sql", "sql/rule.sql"]:
    con.execute(open(path).read())
print(con.sql(open("queries/open-breaks.sql").read()).df())
```

The views (`flow_days`, `stock_days`, `breaks_days`, `statement_days`, `books_days`,
`cuts_days`…) are a good starting point for your own queries. The file columns are declared, not
inferred, so an empty file still has them; the `*_days` views add `day`, `run` and `rule` from the
path. Amounts are exact `HUGEINT` minor units, and instants are UTC `TIMESTAMP`s.

## Reading from object storage

DuckDB loads its `httpfs` extension on first use of an `s3://` path. Give it credentials with a
secret, in the file named by `LETTERING_INIT`:

```sql
CREATE SECRET (TYPE s3, PROVIDER credential_chain);
```

Azure storage works the same way through DuckDB's `azure` extension and an `az://` path.

Recon's API returns one pre-signed URL per file. A URL cannot be globbed, so the per-run views
take each file's URL through its own variable, which overrides the run directory. Run
`sql/schema.sql`, `sql/run.sql` and `check.sql` directly, since the wrapper takes a directory:

```sql
SET VARIABLE manifest = 'https://…/manifest.json?…';
SET VARIABLE flow = 'https://…/flow.ndjson.gz?…';
-- likewise carried, stock, breaks, unclassified and period
```

A file in parts, and the multi-day queries, need a directory: download the files first, keeping
the `rule=/day=/run=` layout. The tests run on local files only; object storage and pre-signed
URLs are not covered by them.

## Test data and tests

`testdata/` holds three rules, all produced by `testdata/generate.py` (Python standard library
only, deterministic):

```bash
python3 tools/lettering-duckdb/testdata/generate.py
```

- **`rule=psp-vs-billing`**: the worked example of the results doc (§10), 24 September 2026. Its
  NDJSON lines are the doc's, byte for byte. The manifest is the doc's, with real SHA-256 values in
  place of the illustrative ones. The run of 23 September is partial: it holds only what
  `check-chain` reads.
- **`rule=qa-scenarios`**: seven days, two assets, one scripted story per case. The stories cover
  every flow class, including:
  - an application at `pending` that is finalised later, or never;
  - a payment reversed after application, found by a product lookup;
  - an application on a payment already failed, found by a PSP lookup;
  - a payment paid before `backfillFrom`, which gives a non-zero `fromLookups`;
  - a payment split across two invoices;
  - a second application on a matched payment;
  - an application undone with its reference.

  The stock covers every class. The stories also include an acceptance that lapses when the class
  changes, a stock break resolved on a hold that stays open, a credit note (`letteredOther`), an
  application in no state set, a refund pair and a refund on the original payment id.
- **`rule=qa-verdicts`**: one week that goes through every verdict. It includes a day with no
  activity at all (every data file empty), an incomplete run followed by a two-day window, a
  retry, and the week's `period.json`.

The two qa rules come from a small reference engine in `generate.py` that follows the results doc.
It also writes `expected/`: the CSV each query must return, computed without DuckDB. A file named
`<query>_<variable>=<value>.csv` is the query run with that variable. Once EN-2322 produces result
files from its scenarios, they are compared with these.

```bash
just lettering-duckdb-tests
```

The tests check four things:
1. Every run passes `check`, and every run chains onto the run its manifest names.
2. Every query returns exactly what `expected/` holds, including `business-id` for five stories.
3. Every rule of `check.sql` and `check-chain.sql` fires on at least one corrupted copy, and the
   suite fails if a rule is never exercised. A rule's name always contains an underscore.
4. Legitimate variants pass, and awkward inputs are handled:
   - legitimate variants: a file in parts, a field unknown to `lettering/1`, an incomplete run;
   - awkward inputs: a path with a space, a quote and glob characters, a variable value with a
     quote, an unknown or missing variable, a wrong directory, a day that is not a date or has no
     complete run, an unreadable file, an init file that prints a row, an empty result in CSV.

They pass on DuckDB 1.4.3 and 1.5.5, under the macOS `sh` and under `dash`. They are not part of
`just tests`. They need only the DuckDB CLI, `gzip`, `sed`, `sort` and `comm`.

When `schemaVersion` changes, the pack, its test data and this README change in the same PR.
