-- Checks that a run chains onto the previous complete run. Load sql/schema.sql
-- and sql/run.sql first, with `run` set to the later run, and set the earlier one:
--
--   SET VARIABLE prev = 'path/to/rule=…/day=2026-09-23/run=r-20260924T000003Z';
--
-- Reads only the earlier run's manifest and carried file. Prints one row per
-- violated rule and raises an error when there is one, like check.sql.

CREATE OR REPLACE TEMP TABLE chain_violations (rule VARCHAR, key VARCHAR, detail VARCHAR);

CREATE OR REPLACE TEMP VIEW prev_manifest AS
SELECT content::JSON AS m, sha256(content) AS sha256
FROM read_text(getvariable('prev') || '/manifest.json');

CREATE OR REPLACE TEMP VIEW prev_carried AS
SELECT * FROM flow_file(getvariable('prev') || '/carried*.ndjson.gz', false);

INSERT INTO chain_violations
SELECT 'previous_run', 'runId', 'previousRun.runId ' || coalesce(c.m->'previousRun'->>'runId', 'missing') || ', earlier run ' || (p.m->>'runId')
FROM manifest c, prev_manifest p WHERE (c.m->'previousRun'->>'runId') IS DISTINCT FROM (p.m->>'runId')
UNION ALL
SELECT 'previous_run', 'day', 'previousRun.day ' || coalesce(c.m->'previousRun'->>'day', 'missing') || ', earlier run ' || (p.m->'period'->>'day')
FROM manifest c, prev_manifest p WHERE (c.m->'previousRun'->>'day') IS DISTINCT FROM (p.m->'period'->>'day')
UNION ALL
SELECT 'previous_run', 'manifestSha256', 'previousRun.manifestSha256 ' || coalesce(c.m->'previousRun'->>'manifestSha256', 'missing') || ', earlier manifest ' || p.sha256
FROM manifest c, prev_manifest p WHERE (c.m->'previousRun'->>'manifestSha256') IS DISTINCT FROM p.sha256
UNION ALL
SELECT 'previous_run', 'verdict', 'the earlier run is incomplete, so it cannot be a link in the chain'
FROM prev_manifest p WHERE p.m->>'verdict' = 'incomplete';

-- The open items pick up where the earlier run left them.
INSERT INTO chain_violations
SELECT 'suspense_open_prev', s.asset,
       'openPrev ' || coalesce(s.s->'suspense'->>'openPrev', 'missing') || ' (' || coalesce(s.s->'suspense'->>'countPrev', '?') || ')'
       || ', earlier open ' || coalesce(p.m->'statement'->s.asset->'suspense'->>'open', '0')
       || ' (' || coalesce(p.m->'statement'->s.asset->'suspense'->>'count', '0') || ')'
FROM m_statement s, prev_manifest p
WHERE coalesce((s.s->'suspense'->>'openPrev')::HUGEINT, 0) <> coalesce((p.m->'statement'->s.asset->'suspense'->>'open')::HUGEINT, 0)
   OR coalesce((s.s->'suspense'->>'countPrev')::BIGINT, 0) <> coalesce((p.m->'statement'->s.asset->'suspense'->>'count')::BIGINT, 0);

-- Every item the earlier run carried shows up again in this run's flow.
INSERT INTO chain_violations
SELECT 'carried_in', p.ref || '/' || p.asset, 'carried by the earlier run, missing from this run''s flow'
FROM prev_carried p ANTI JOIN flow f USING (ref, asset);

-- The books pick up where the earlier run left them.
INSERT INTO chain_violations
WITH prev AS (SELECT unnest(from_json(m->'books', lettering_books_shape()), recursive := true) FROM prev_manifest)
SELECT 'books_open_prev', concat_ws('/', coalesce(b.side, p.side), coalesce(b.prefix, p.prefix), coalesce(b.asset, p.asset)),
       'openPrev ' || coalesce(b.openPrev, 0) || ', earlier open ' || coalesce(p.open, 0)
FROM m_books b FULL JOIN prev p USING (side, prefix, asset)
WHERE coalesce(b.openPrev, 0) <> coalesce(p.open, 0);

SELECT rule, key, detail FROM chain_violations ORDER BY rule, key;

SELECT CASE WHEN count(*) = 0 THEN 'ok: the run chains onto the earlier one'
            ELSE error(count(*) || ' chain violation(s)') END AS result
FROM chain_violations;
