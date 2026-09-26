-- Views over ONE run. Load schema.sql first, and set the run's directory:
--
--   SET VARIABLE run = 'path/to/rule=psp-vs-billing/day=2026-09-24/run=r-20260925T000004Z';
--
-- The directory may be local or s3://. A file served on its own, such as a
-- pre-signed URL from recon's API, overrides its path: SET VARIABLE flow = 'https://…'
-- (likewise manifest, carried, stock, breaks, unclassified, period).
--
-- `flow*` also matches a file that comes in parts (flow-00000.ndjson.gz, …).

CREATE OR REPLACE MACRO lettering_file(name) AS
    coalesce(getvariable(name), getvariable('run') || '/' || name || '*.ndjson.gz');

CREATE OR REPLACE VIEW manifest AS
SELECT content::JSON AS m
FROM read_text(coalesce(getvariable('manifest'), getvariable('run') || '/manifest.json'));

-- Each data view adds `file` (the file's name) and `pos` (the row's position, in
-- file order). The run's day is the manifest's (m_run).
CREATE OR REPLACE VIEW flow AS
SELECT * EXCLUDE (filename, ordinality), lettering_name(filename) AS file, ordinality AS pos
FROM flow_file(lettering_file('flow'), false);
CREATE OR REPLACE VIEW carried AS
SELECT * EXCLUDE (filename, ordinality), lettering_name(filename) AS file, ordinality AS pos
FROM flow_file(lettering_file('carried'), false);
CREATE OR REPLACE VIEW stock AS
SELECT * EXCLUDE (filename, ordinality), lettering_name(filename) AS file, ordinality AS pos
FROM stock_file(lettering_file('stock'), false);
CREATE OR REPLACE VIEW breaks AS
SELECT * EXCLUDE (filename, ordinality), lettering_name(filename) AS file, ordinality AS pos
FROM breaks_file(lettering_file('breaks'), false);
CREATE OR REPLACE VIEW unclassified AS
SELECT * EXCLUDE (filename, ordinality), lettering_name(filename) AS file, ordinality AS pos
FROM unclassified_file(lettering_file('unclassified'), false);

-- The manifest, flattened.
CREATE OR REPLACE VIEW m_run AS
SELECT m->>'schemaVersion' AS schema_version, m->>'runId' AS run_id,
       (m->'period'->>'day')::DATE AS day, m->>'verdict' AS verdict
FROM manifest;

CREATE OR REPLACE VIEW m_files AS
SELECT unnest(from_json(m->'files', '[{"name":"VARCHAR","rows":"BIGINT","sha256":"VARCHAR"}]'), recursive := true)
FROM manifest;

CREATE OR REPLACE VIEW m_cuts AS
SELECT unnest(from_json(m->'cuts', lettering_cuts_shape()), recursive := true) FROM manifest;

CREATE OR REPLACE VIEW m_statement AS
SELECT a AS asset, m->'statement'->a AS s FROM manifest, unnest(json_keys(m->'statement')) t(a);

CREATE OR REPLACE VIEW m_lines AS
SELECT asset, unnest(from_json(s->'lines', lettering_lines_shape()), recursive := true) FROM m_statement;

CREATE OR REPLACE VIEW m_carried_outside AS
SELECT asset, unnest(from_json(s->'carriedOutside', lettering_lines_shape()), recursive := true) FROM m_statement;

CREATE OR REPLACE VIEW m_books AS
SELECT unnest(from_json(m->'books', lettering_books_shape()), recursive := true) FROM manifest;
