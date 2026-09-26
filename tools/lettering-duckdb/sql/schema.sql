-- Readers for the lettering/1 result files, one table macro per file kind.
--
-- The file columns are declared, never inferred, so an empty file (every data file
-- is written on every complete run, even with no row) still has its columns.
-- Amounts are read as HUGEINT: exact minor units, written as strings in the files.
-- Instants are read as TIMESTAMP in UTC, as the files write them. A key missing
-- from a row reads as NULL. `hive` says whether to read `rule`, `day` and `run`
-- from the rule=/day=/run= segments of the path. The macros also return:
--   - `filename`, to tell a file's parts and runs apart;
--   - `ordinality`, the row's position in the scan, in file order.
--
-- Field definitions: docs/technical/transaction-level-results.md §6.

-- An amount in the hold's open direction: positive = open as its prefix expects.
CREATE OR REPLACE MACRO open_dir(balance, open_sign) AS
    CASE open_sign WHEN 'negative' THEN -balance ELSE balance END;

-- A file's name, without the query string of a pre-signed URL.
CREATE OR REPLACE MACRO lettering_name(path) AS
    regexp_replace(parse_filename(path), '\?.*$', '');

CREATE OR REPLACE MACRO flow_file(path, hive) AS TABLE
SELECT * FROM read_json(path, format = 'newline_delimited', filename = true, hive_partitioning = hive, columns = {
    ref: 'VARCHAR', asset: 'VARCHAR', class: 'VARCHAR', outcome: 'VARCHAR',
    pspAmount: 'HUGEINT', productAmount: 'HUGEINT', drift: 'HUGEINT', impact: 'HUGEINT',
    firstSeen: 'DATE', firstSide: 'VARCHAR', breakOn: 'DATE',
    merchantRef: 'VARCHAR', pairedHold: 'VARCHAR',
    psp: 'STRUCT(tx UBIGINT, state VARCHAR, amount HUGEINT, holdAmount HUGEINT, insertedAt TIMESTAMP)[]',
    product: 'STRUCT(tx UBIGINT, businessId VARCHAR, holdId VARCHAR, amount HUGEINT, insertedAt TIMESTAMP)[]'
}) WITH ORDINALITY;

CREATE OR REPLACE MACRO stock_file(path, hive) AS TABLE
SELECT * FROM read_json(path, format = 'newline_delimited', filename = true, hive_partitioning = hive, columns = {
    side: 'VARCHAR', hold: 'VARCHAR', asset: 'VARCHAR', prefix: 'VARCHAR', holdId: 'VARCHAR',
    openSign: 'VARCHAR', balance: 'HUGEINT', class: 'VARCHAR', outcome: 'VARCHAR',
    lifecycle: 'VARCHAR', openedAt: 'TIMESTAMP', ageDays: 'INTEGER', bucket: 'VARCHAR',
    pairedRef: 'VARCHAR', previousBalance: 'HUGEINT', clearedAt: 'TIMESTAMP', clearedBy: 'VARCHAR'
}) WITH ORDINALITY;

-- A break row is a flow or stock row plus the break's own fields.
CREATE OR REPLACE MACRO breaks_file(path, hive) AS TABLE
SELECT * FROM read_json(path, format = 'newline_delimited', filename = true, hive_partitioning = hive, columns = {
    breakId: 'VARCHAR', leg: 'VARCHAR', priority: 'INTEGER', lifecycle: 'VARCHAR',
    openedOn: 'DATE', resolvedOn: 'DATE', amount: 'HUGEINT', previousClass: 'VARCHAR',
    acceptedOn: 'DATE', class: 'VARCHAR', outcome: 'VARCHAR', asset: 'VARCHAR',
    ref: 'VARCHAR', pspAmount: 'HUGEINT', productAmount: 'HUGEINT', drift: 'HUGEINT',
    impact: 'HUGEINT', firstSeen: 'DATE', firstSide: 'VARCHAR', breakOn: 'DATE',
    merchantRef: 'VARCHAR', pairedHold: 'VARCHAR',
    psp: 'STRUCT(tx UBIGINT, state VARCHAR, amount HUGEINT, holdAmount HUGEINT, insertedAt TIMESTAMP)[]',
    product: 'STRUCT(tx UBIGINT, businessId VARCHAR, holdId VARCHAR, amount HUGEINT, insertedAt TIMESTAMP)[]',
    side: 'VARCHAR', hold: 'VARCHAR', prefix: 'VARCHAR', holdId: 'VARCHAR', openSign: 'VARCHAR',
    balance: 'HUGEINT', openedAt: 'TIMESTAMP', ageDays: 'INTEGER', bucket: 'VARCHAR',
    pairedRef: 'VARCHAR', previousBalance: 'HUGEINT', clearedAt: 'TIMESTAMP', clearedBy: 'VARCHAR'
}) WITH ORDINALITY;

CREATE OR REPLACE MACRO unclassified_file(path, hive) AS TABLE
SELECT * FROM read_json(path, format = 'newline_delimited', filename = true, hive_partitioning = hive, columns = {
    side: 'VARCHAR', tx: 'UBIGINT', ref: 'VARCHAR', asset: 'VARCHAR', outcome: 'VARCHAR',
    state: 'VARCHAR', amount: 'HUGEINT', insertedAt: 'TIMESTAMP'
}) WITH ORDINALITY;

-- JSON shapes of the manifest's arrays, for from_json.
CREATE OR REPLACE MACRO lettering_books_shape() AS
    '[{"side":"VARCHAR","prefix":"VARCHAR","asset":"VARCHAR","openSign":"VARCHAR","openPrev":"HUGEINT","opened":"HUGEINT","lettered":"HUGEINT","letteredOther":"HUGEINT","open":"HUGEINT","count":"BIGINT","continuityOk":"BOOLEAN"}]';
CREATE OR REPLACE MACRO lettering_lines_shape() AS
    '[{"class":"VARCHAR","outcome":"VARCHAR","earlierDay":"BOOLEAN","amount":"HUGEINT","count":"BIGINT","top":["VARCHAR"]}]';
CREATE OR REPLACE MACRO lettering_cuts_shape() AS
    '[{"side":"VARCHAR","ledger":"VARCHAR","txFrom":"UBIGINT","txTo":"UBIGINT","logFrom":"UBIGINT","logTo":"UBIGINT"}]';
