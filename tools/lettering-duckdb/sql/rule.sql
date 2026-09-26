-- Views over MANY days of one rule, keeping only each day's current run. Load
-- schema.sql first, and set the rule's directory (local or s3://):
--
--   SET VARIABLE rule = 'path/to/rule=psp-vs-billing';
--
-- A day's current run is its latest complete run (results doc §2): run ids sort
-- by start instant, and an incomplete run writes its manifest only, so it is left
-- out here even when it came last. `day` and `run` come from the path.

CREATE OR REPLACE MACRO lettering_glob(name) AS
    getvariable('rule') || '/day=*/run=*/' || name || '*.ndjson.gz';

-- Every run's manifest, complete or not.
CREATE OR REPLACE VIEW runs AS
SELECT day, run, json->>'verdict' AS verdict, json AS m
FROM read_json_objects(getvariable('rule') || '/day=*/run=*/manifest.json', hive_partitioning = true);

CREATE OR REPLACE VIEW current_runs AS
SELECT * FROM runs
WHERE verdict <> 'incomplete'
QUALIFY run = max(run) OVER (PARTITION BY day);

-- The day a query is about: the `day` variable, or the latest day with a current run.
CREATE OR REPLACE MACRO lettering_target_day() AS
    coalesce(getvariable('day')::DATE, (SELECT max(day) FROM current_runs));

CREATE OR REPLACE VIEW flow_days AS
SELECT f.* EXCLUDE (filename, ordinality)
FROM flow_file(lettering_glob('flow'), true) f SEMI JOIN current_runs USING (day, run);
CREATE OR REPLACE VIEW carried_days AS
SELECT f.* EXCLUDE (filename, ordinality)
FROM flow_file(lettering_glob('carried'), true) f SEMI JOIN current_runs USING (day, run);
CREATE OR REPLACE VIEW stock_days AS
SELECT s.* EXCLUDE (filename, ordinality)
FROM stock_file(lettering_glob('stock'), true) s SEMI JOIN current_runs USING (day, run);
CREATE OR REPLACE VIEW breaks_days AS
SELECT b.* EXCLUDE (filename, ordinality)
FROM breaks_file(lettering_glob('breaks'), true) b SEMI JOIN current_runs USING (day, run);
CREATE OR REPLACE VIEW unclassified_days AS
SELECT u.* EXCLUDE (filename, ordinality)
FROM unclassified_file(lettering_glob('unclassified'), true) u SEMI JOIN current_runs USING (day, run);

-- The current runs' manifests, flattened: one row per day and asset, prefix or side.
CREATE OR REPLACE VIEW statement_days AS
SELECT day, run, verdict, a AS asset, m->'statement'->a AS s
FROM current_runs, unnest(json_keys(m->'statement')) t(a);

CREATE OR REPLACE VIEW books_days AS
SELECT day, run, unnest(from_json(m->'books', lettering_books_shape()), recursive := true)
FROM current_runs;

-- Each side's transaction window (txFrom, txTo]: what the day itself booked.
CREATE OR REPLACE VIEW cuts_days AS
SELECT day, run, unnest(from_json(m->'cuts', lettering_cuts_shape()), recursive := true)
FROM current_runs;
