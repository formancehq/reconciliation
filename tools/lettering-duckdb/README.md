# lettering-duckdb

Checks and queries for the result files of a transaction-level (lettering) rule, format
`lettering/1`. The files and every field are specified in
[transaction-level-results.md](../../docs/technical/transaction-level-results.md), the source of
truth. This tool is how an implementer, support or a customer checks a run and exploits the files
([EN-2353](https://formance-team.atlassian.net/browse/EN-2353)).

It is SQL run by the [DuckDB](https://duckdb.org) CLI, plus a small shell wrapper. There is no
compiled binary, and recon itself does not depend on it: the service, once built (EN-2322), asserts
the format's identities in its own Go tests. DuckDB reads the gzipped NDJSON and the
`rule=/day=/run=` layout as they are, locally or on object storage.

## Requirements

The DuckDB CLI, 1.4 or later. The repository's Nix shell provides the pinned version (1.4.3):

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
violated. An incomplete run writes its manifest only, so the command prints its reason and checks
only that no data file exists.

```bash
tools/lettering-duckdb/lettering check-chain <earlier-run-dir> <run-dir>
```

Checks that a run chains onto the previous complete run: `previousRun` (run id, day, manifest
SHA-256), the open items and books picking up where the earlier run left them, and every item it
carried appearing again in the flow.

```bash
tools/lettering-duckdb/lettering query <name> <rule-dir> [variable=value ...]
```

Runs one query of the pack over every day of a rule, keeping each day's current run (its latest
complete run). `lettering queries` lists them.

A run directory is `…/rule=<id>/day=<YYYY-MM-DD>/run=<runId>`, and a rule directory is
`…/rule=<id>`. Both can be local paths or `s3://` URLs.

| Environment | Effect |
|---|---|
| `DUCKDB` | The DuckDB CLI to run (default: `duckdb` on `PATH`) |
| `LETTERING_INIT` | A SQL file run first, for example the object-storage secret below |
| `LETTERING_MODE` | A CLI output mode: `csv` for a spreadsheet, `json`, `markdown`… |

For example, the applications of 24 September as CSV:

```bash
LETTERING_MODE=csv tools/lettering-duckdb/lettering query applications ./rule=psp-vs-billing day=2026-09-24 > applications.csv
```

## The query pack

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
| `applications` | One row per product application, for a spreadsheet | `day` |
| `psp-events` | One row per PSP event, for a spreadsheet | `day` |

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
| `breaks_vs_rows` | Every open break of the flow and stock files is in the breaks file, and back |
| `break_amount`, `break_outcome`, `break_priority` | A break's amount, outcome and priority follow its leg, lifecycle and class |
| `row_amounts`, `row_impact`, `row_outcome` | A flow row's amounts follow from its transactions, a carried row has no `impact`, and each row's outcome (and a stock row's sign) follows from its class |
| `triage_break`, `triage_pending` | The manifest's triage matches the breaks and the pending flow rows |
| `unique_key`, `row_order` | Each file's unique key and row order (results doc §8) |
| `verdict` | The verdict follows from the files |

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

`testdata/` holds the worked example of the results doc (§10), on 24 September 2026. Its NDJSON
lines are the doc's, byte for byte. The manifest is the doc's, with real SHA-256 values in place
of the illustrative ones. The run of 23 September is partial: it holds only what `check-chain`
reads, its manifest and carried file. Once EN-2322 produces these files from its scenario, they
replace this copy.

```bash
just lettering-duckdb-tests
```

The tests check the worked day and its chain. They corrupt copies of it and assert that each
corruption is reported:
- a carried row removed;
- a bridge line altered;
- a stock break with the wrong sign;
- a duplicate key;
- a manifest count off by one;
- two rows out of order;
- an open break missing from the breaks file;
- a product amount that its applications contradict;
- a wrong-sign hold classed open;
- an application outside the window;
- a broken chain.

They also check that legitimate variants pass: a flow file in two parts, a period's last run with
its `period.json`, and incomplete runs. Finally they check the choice of each day's current run,
the results of several queries, and that every query runs. They are not part of `just tests`, and
they need only the DuckDB CLI, `gzip` and `sed`.

When `schemaVersion` changes, the pack, its test data and this README change in the same PR.
