-- The shape of a run's directory, read from its manifest and its file names only, before any
-- data file is opened. check.sql and check-chain.sql build views over every kind of data file,
-- and a view over a kind with no file fails, so the `lettering` wrapper checks the shape first:
--
--   SELECT * FROM run_shape('path/to/rule=psp-vs-billing/day=2026-09-24/run=r-20260925T000004Z');
--   SELECT * FROM shape_violations('path/to/…/run=…');
--   SELECT * FROM chain_shape_violations('path/to/the/earlier/run=…');
--
-- Needs no other file. The directory may be local or s3://, without a trailing slash.
-- Rules: docs/technical/transaction-level-results.md §6 (an incomplete run's reduced manifest)
-- and §8 (every data file is written on every complete run).

-- One row: the verdict, an incomplete run's reason, the number of data files, the kinds of data
-- file that have no file, and the fields a reduced manifest lacks or should not have.
CREATE OR REPLACE MACRO run_shape(dir) AS TABLE
SELECT m->>'verdict' AS verdict,
       m->'incomplete'->>'reason' AS reason,
       m->>'runId' AS run_id,
       (SELECT count(*) FROM glob(dir || '/*.ndjson.gz')) AS files,
       list_filter([
           CASE WHEN (SELECT count(*) FROM glob(dir || '/flow*.ndjson.gz')) = 0 THEN 'flow' END,
           CASE WHEN (SELECT count(*) FROM glob(dir || '/carried*.ndjson.gz')) = 0 THEN 'carried' END,
           CASE WHEN (SELECT count(*) FROM glob(dir || '/stock*.ndjson.gz')) = 0 THEN 'stock' END,
           CASE WHEN (SELECT count(*) FROM glob(dir || '/breaks*.ndjson.gz')) = 0 THEN 'breaks' END
       ], k -> k IS NOT NULL) AS missing,
       list_concat(
           list_transform(list_filter(['schemaVersion', 'engine', 'rule', 'runId', 'period', 'startedAt', 'finishedAt',
                                       'verdict', 'incomplete.reason', 'incomplete.detail', 'expiresAt'],
                                      k -> json_extract(m, '$.' || k) IS NULL), k -> 'no ' || k),
           list_transform(list_filter(['counts', 'statement', 'books', 'paymentAccounts', 'files'],
                                      k -> json_extract(m, '$.' || k) IS NOT NULL), k -> 'has ' || k)) AS fields
FROM (SELECT content::JSON AS m FROM read_text(dir || '/manifest.json'));

-- The rules the shape of one run can break, in the order the wrapper reports them. An incomplete
-- run that keeps them has nothing else to check: it writes no data file. Each rule is written
-- `SELECT '<rule>', …` like those of check.sql, so test.sh finds it and makes it fire.
CREATE OR REPLACE MACRO shape_violations(dir) AS TABLE
SELECT rule, key, detail FROM (
    SELECT 'incomplete_files', run_id, 'incomplete run (' || coalesce(reason, '-') || ') with ' || files || ' data file(s); an incomplete run writes no data file', 1
    FROM run_shape(dir) WHERE verdict = 'incomplete' AND files > 0
    UNION ALL
    SELECT 'incomplete_fields', run_id, 'incomplete run (' || coalesce(reason, '-') || '): ' || array_to_string(fields, ', ') || '; its manifest is reduced to the fields of results doc §6', 2
    FROM run_shape(dir) WHERE verdict = 'incomplete' AND files = 0 AND len(fields) > 0
    UNION ALL
    SELECT 'file_missing', missing[i], 'no ' || missing[i] || ' file; a complete run writes every data file, even empty', 2 + i
    FROM run_shape(dir), range(1, len(missing) + 1) r(i) WHERE verdict IS DISTINCT FROM 'incomplete'
) t(rule, key, detail, pos)
ORDER BY pos;

-- The rule the shape of a chain can break: an incomplete run is not a link in the chain, so it
-- cannot be the earlier run (check-chain.sql reads its data files).
CREATE OR REPLACE MACRO chain_shape_violations(prev) AS TABLE
SELECT rule, key, detail FROM (
    SELECT 'previous_run', 'verdict', prev || ' is an incomplete run, so it cannot be a link in the chain; pass the run the later manifest''s previousRun names'
    FROM run_shape(prev) WHERE verdict = 'incomplete'
) t(rule, key, detail);
