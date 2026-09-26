# lettering-duckdb

Checks and queries for the result files of a transaction-level (lettering) rule, format
`lettering/1`. The team uses it to validate a run's files and to answer reconciliation questions
from them, during implementation, QA and support
([EN-2353](https://formance-team.atlassian.net/browse/EN-2353)). It is an internal tool, not
shipped to customers, and recon does not depend on it.

- How the tool is built and how to use it:
  [docs/technical/lettering-duckdb.md](../../docs/technical/lettering-duckdb.md).
- The files and every field (the source of truth):
  [docs/technical/transaction-level-results.md](../../docs/technical/transaction-level-results.md).

It is SQL run by the [DuckDB](https://duckdb.org) CLI (1.4 or later), plus a POSIX shell wrapper.
The repository's Nix shell provides DuckDB:

```bash
nix develop --impure
```

Check one run:

```bash
tools/lettering-duckdb/lettering check <run-dir>
```

Check that a run chains onto the run its manifest's `previousRun` names:

```bash
tools/lettering-duckdb/lettering check-chain <earlier-run-dir> <run-dir>
```

Run one query over every day of a rule:

```bash
tools/lettering-duckdb/lettering query <name> <rule-dir> [variable=value ...]
```

List the queries:

```bash
tools/lettering-duckdb/lettering queries
```

Exit status: 0 sound, 1 a rule is violated, 2 wrong arguments or directory, 3 unreadable files or
a SQL error. `LETTERING_MODE=csv` writes a standard CSV, and `LETTERING_INIT` names a SQL file run
first, such as an object-storage secret.

Regenerate the test data, then run the tests (not part of `just tests`):

```bash
python3 tools/lettering-duckdb/testdata/generate.py
```

```bash
just lettering-duckdb-tests
```

When `schemaVersion` changes, the pack, its test data and its doc change in the same PR.
