-- Checks that a run chains onto the previous complete run. Load sql/schema.sql
-- and sql/run.sql first, with `run` set to the later run, and set the earlier one:
--
--   SET VARIABLE prev = 'path/to/rule=…/day=2026-09-23/run=r-20260924T000003Z';
--
-- Reads the earlier run's manifest, carried, stock and breaks files. Prints one row
-- per violated rule and raises an error when there is one, like check.sql.

CREATE OR REPLACE TEMP TABLE chain_violations (rule VARCHAR, key VARCHAR, detail VARCHAR);

CREATE OR REPLACE TEMP VIEW prev_manifest AS
SELECT content::JSON AS m, sha256(content) AS sha256
FROM read_text(getvariable('prev') || '/manifest.json');

CREATE OR REPLACE TEMP VIEW prev_carried AS
SELECT * FROM flow_file(getvariable('prev') || '/carried*.ndjson.gz', false);
CREATE OR REPLACE TEMP VIEW prev_stock AS
SELECT * FROM stock_file(getvariable('prev') || '/stock*.ndjson.gz', false);
CREATE OR REPLACE TEMP VIEW prev_breaks AS
SELECT * FROM breaks_file(getvariable('prev') || '/breaks*.ndjson.gz', false);

-- This run's window starts at the earlier run's cut, on each side.
INSERT INTO chain_violations
WITH prev_cuts AS (SELECT unnest(from_json(m->'cuts', lettering_cuts_shape()), recursive := true) FROM prev_manifest)
SELECT 'window_start', c.side,
       'txFrom ' || c.txFrom || ' logFrom ' || c.logFrom || ', earlier txTo ' || p.txTo || ' logTo ' || p.logTo
FROM m_cuts c JOIN prev_cuts p USING (side)
WHERE c.txFrom <> p.txTo OR c.logFrom <> p.logTo;

-- A carried item's drift moves only by this window's impact.
INSERT INTO chain_violations
SELECT 'carried_drift', p.ref || '/' || p.asset,
       'earlier drift ' || p.drift || ', now drift ' || f.drift || ' with impact ' || f.impact
FROM prev_carried p JOIN flow f USING (ref, asset)
WHERE f.drift - f.impact <> p.drift;

-- fromLookups is the drift the rows not carried in already had before this window.
INSERT INTO chain_violations
WITH found AS (SELECT f.asset, sum(f.drift - f.impact) AS from_lookups
               FROM flow f ANTI JOIN prev_carried p USING (ref, asset) GROUP BY f.asset)
SELECT 'from_lookups', s.asset,
       'fromLookups ' || coalesce(s.s->'suspense'->>'fromLookups', 'missing') || ', rows not carried in ' || coalesce(f.from_lookups, 0)
FROM m_statement s LEFT JOIN found f USING (asset)
WHERE coalesce((s.s->'suspense'->>'fromLookups')::HUGEINT, 0) <> coalesce(f.from_lookups, 0);

-- A break is new when it was not open before, and otherwise keeps its openedOn; its
-- previousClass names the class it had; an open break never vanishes without being resolved.
INSERT INTO chain_violations
WITH was AS (SELECT * FROM prev_breaks WHERE outcome = 'break')
SELECT 'break_lifecycle', b.breakId,
       CASE WHEN w.breakId IS NULL AND b.lifecycle <> 'new' THEN b.lifecycle || ', but it was not open before'
            WHEN w.breakId IS NOT NULL AND b.lifecycle = 'new' THEN 'new, but it was open before'
            WHEN w.breakId IS NOT NULL AND b.openedOn <> w.openedOn THEN 'openedOn ' || b.openedOn || ', was ' || w.openedOn
            ELSE 'previousClass ' || coalesce(b.previousClass, 'none') || ', earlier class ' || w.class END
FROM breaks b LEFT JOIN was w USING (breakId)
WHERE (w.breakId IS NULL AND b.lifecycle <> 'new')
   OR (w.breakId IS NOT NULL AND b.lifecycle = 'new')
   OR (w.breakId IS NOT NULL AND b.openedOn <> w.openedOn)
   OR (w.breakId IS NOT NULL AND b.lifecycle = 'persisting'
       AND b.previousClass IS DISTINCT FROM CASE WHEN b.class <> w.class THEN w.class END)
UNION ALL
SELECT 'break_lifecycle', w.breakId, 'open before, and neither persisting nor resolved now'
FROM (SELECT * FROM prev_breaks WHERE outcome = 'break') w ANTI JOIN breaks b USING (breakId);

-- A hold is new when it was not open before; a hold open before is still listed, open or
-- cleared, and a cleared one carries its earlier balance.
INSERT INTO chain_violations
WITH was AS (SELECT * FROM prev_stock WHERE class <> 'cleared')
SELECT 'stock_lifecycle', s.side || '/' || s.hold || '/' || s.asset,
       s.lifecycle || CASE WHEN w.hold IS NULL THEN ', but it was not open before'
                           WHEN s.lifecycle = 'new' THEN ', but it was open before'
                           ELSE ', previousBalance ' || coalesce(s.previousBalance::VARCHAR, 'missing') || ', earlier balance ' || w.balance END
FROM stock s LEFT JOIN was w USING (side, hold, asset)
WHERE (w.hold IS NULL AND s.lifecycle <> 'new')
   OR (w.hold IS NOT NULL AND s.lifecycle = 'new')
   OR (s.lifecycle = 'cleared' AND s.previousBalance IS DISTINCT FROM w.balance)
UNION ALL
SELECT 'stock_lifecycle', w.side || '/' || w.hold || '/' || w.asset, 'open before, missing now: neither open nor cleared'
FROM (SELECT * FROM prev_stock WHERE class <> 'cleared') w ANTI JOIN stock s USING (side, hold, asset);

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
