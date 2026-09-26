-- Checks one COMPLETE run of a lettering rule against the rules of
-- docs/technical/transaction-level-results.md. Load sql/schema.sql and
-- sql/run.sql first (the `lettering check` wrapper does it).
--
-- Prints one row per violated rule, naming the rule and the offending key, then
-- a final line. It raises an error when a rule is violated, so a script that runs
-- it with `duckdb -bail` exits non-zero. An incomplete run has no data file to
-- check: read its manifest's `incomplete.reason` instead.

CREATE OR REPLACE TEMP TABLE violations (rule VARCHAR, key VARCHAR, detail VARCHAR);

CREATE OR REPLACE TEMP MACRO v_int(x) AS coalesce(x::HUGEINT, 0);

-- The rule's durations, in days ("7d" -> 7; NULL when absent), and its age buckets.
CREATE OR REPLACE TEMP MACRO v_days(x) AS regexp_extract(x, '^([0-9]+)d$', 1)::INTEGER;
CREATE OR REPLACE TEMP VIEW v_rule AS
SELECT v_days(m->'rule'->'psp'->>'grace') AS psp_grace,
       v_days(m->'rule'->'product'->>'grace') AS product_grace,
       v_days(m->'rule'->'psp'->>'maxAge') AS psp_max_age,
       v_days(m->'rule'->'product'->>'maxAge') AS product_max_age,
       list_transform(from_json(m->'rule'->'buckets', '["VARCHAR"]'), b -> v_days(b)) AS bounds,
       m->'period'->>'tz' AS tz
FROM manifest;
-- The label of an age bucket, from the bucket bounds: [1, 7, 30] gives 0-1d, 2-7d, 8-30d, >30d.
CREATE OR REPLACE TEMP MACRO v_bucket(age, bounds) AS
    CASE WHEN age > bounds[-1] THEN '>' || bounds[-1] || 'd'
         WHEN age <= bounds[1] THEN '0-' || bounds[1] || 'd'
         ELSE (list_filter(bounds, b -> b < age)[-1] + 1) || '-' || list_filter(bounds, b -> b >= age)[1] || 'd' END;

-- Run-level facts ----------------------------------------------------------------

INSERT INTO violations
SELECT 'schema_version', run_id, 'schemaVersion is ' || coalesce(schema_version, 'missing')
FROM m_run WHERE schema_version IS DISTINCT FROM 'lettering/1';

-- Files: listed, present, row counts, SHA-256 -------------------------------------

CREATE OR REPLACE TEMP TABLE actual_files AS
SELECT lettering_name(filename) AS name, sha256(content) AS sha256 FROM read_blob(lettering_file('flow'))
UNION ALL SELECT lettering_name(filename), sha256(content) FROM read_blob(lettering_file('carried'))
UNION ALL SELECT lettering_name(filename), sha256(content) FROM read_blob(lettering_file('stock'))
UNION ALL SELECT lettering_name(filename), sha256(content) FROM read_blob(lettering_file('breaks'))
UNION ALL SELECT lettering_name(filename), sha256(content) FROM read_blob(lettering_file('unclassified'))
UNION ALL SELECT lettering_name(filename), sha256(content)
          FROM read_blob(coalesce(getvariable('period'), coalesce(getvariable('run'), '/nonexistent') || '/period*.json'));

CREATE OR REPLACE TEMP TABLE actual_rows AS
SELECT file AS name, count(*) AS rows FROM flow GROUP BY file
UNION ALL SELECT file, count(*) FROM carried GROUP BY file
UNION ALL SELECT file, count(*) FROM stock GROUP BY file
UNION ALL SELECT file, count(*) FROM breaks GROUP BY file
UNION ALL SELECT file, count(*) FROM unclassified GROUP BY file;

INSERT INTO violations
SELECT 'file_missing', l.name, 'listed in the manifest, not found'
FROM m_files l ANTI JOIN actual_files a USING (name);

INSERT INTO violations
SELECT 'file_unlisted', a.name, 'found, not listed in the manifest'
FROM actual_files a ANTI JOIN m_files l USING (name);

INSERT INTO violations
SELECT 'file_sha256', l.name, 'manifest ' || l.sha256 || ', file ' || a.sha256
FROM m_files l JOIN actual_files a USING (name) WHERE l.sha256 <> a.sha256;

INSERT INTO violations
SELECT 'file_rows', l.name, 'manifest ' || l.rows || ', file ' || coalesce(r.rows, 0)
FROM m_files l LEFT JOIN actual_rows r USING (name)
WHERE l.name LIKE '%.ndjson.gz' AND l.rows <> coalesce(r.rows, 0);

-- Counts ------------------------------------------------------------------------------

INSERT INTO violations
WITH listed AS (SELECT k AS class, (m->'counts'->'flow'->>k)::BIGINT AS n
                FROM manifest, unnest(json_keys(m->'counts'->'flow')) t(k)),
     found AS (SELECT class, count(*) AS n FROM flow GROUP BY class)
SELECT 'counts_flow', coalesce(l.class, f.class), 'manifest ' || coalesce(l.n, 0) || ', flow file ' || coalesce(f.n, 0)
FROM listed l FULL JOIN found f USING (class)
WHERE coalesce(l.n, 0) <> coalesce(f.n, 0);

INSERT INTO violations
WITH listed AS (SELECT k AS outcome, (m->'counts'->'flowOutcome'->>k)::BIGINT AS n
                FROM manifest, unnest(json_keys(m->'counts'->'flowOutcome')) t(k)),
     found AS (SELECT outcome, count(*) AS n FROM flow GROUP BY outcome)
SELECT 'counts_flow_outcome', coalesce(l.outcome, f.outcome), 'manifest ' || coalesce(l.n, 0) || ', flow file ' || coalesce(f.n, 0)
FROM listed l FULL JOIN found f USING (outcome)
WHERE coalesce(l.n, 0) <> coalesce(f.n, 0);

INSERT INTO violations
WITH listed AS (SELECT side, k AS class, (m->'counts'->'stock'->side->>k)::BIGINT AS n
                FROM (SELECT m, unnest(json_keys(m->'counts'->'stock')) AS side FROM manifest),
                     unnest(json_keys(m->'counts'->'stock'->side)) t(k)),
     found AS (SELECT side, class, count(*) AS n FROM stock GROUP BY ALL)
SELECT 'counts_stock', coalesce(l.side, f.side) || '/' || coalesce(l.class, f.class),
       'manifest ' || coalesce(l.n, 0) || ', stock file ' || coalesce(f.n, 0)
FROM listed l FULL JOIN found f USING (side, class)
WHERE coalesce(l.n, 0) <> coalesce(f.n, 0);

INSERT INTO violations
WITH c AS (SELECT m->'counts'->'breaks' AS b FROM manifest),
     listed AS (
        SELECT 'new' AS k, (b->>'new')::BIGINT AS n FROM c
        UNION ALL SELECT 'persisting', (b->>'persisting')::BIGINT FROM c
        UNION ALL SELECT 'resolved', (b->>'resolved')::BIGINT FROM c
        UNION ALL SELECT 'accepted', (b->>'accepted')::BIGINT FROM c
        UNION ALL SELECT 'openByLeg.' || k, (b->'openByLeg'->>k)::BIGINT FROM c, unnest(json_keys(b->'openByLeg')) t(k)
        UNION ALL SELECT 'openByPriority.' || k, (b->'openByPriority'->>k)::BIGINT FROM c, unnest(json_keys(b->'openByPriority')) t(k)),
     found AS (
        SELECT lifecycle AS k, count(*) AS n FROM breaks GROUP BY ALL
        UNION ALL SELECT 'accepted', count(*) FROM breaks WHERE outcome = 'break' AND acceptedOn IS NOT NULL
        UNION ALL SELECT 'openByLeg.' || leg, count(*) FROM breaks WHERE outcome = 'break' GROUP BY ALL
        UNION ALL SELECT 'openByPriority.' || priority, count(*) FROM breaks WHERE outcome = 'break' GROUP BY ALL)
SELECT 'counts_breaks', coalesce(l.k, f.k), 'manifest ' || coalesce(l.n, 0) || ', breaks file ' || coalesce(f.n, 0)
FROM listed l FULL JOIN found f USING (k)
WHERE coalesce(l.n, 0) <> coalesce(f.n, 0);

INSERT INTO violations
WITH listed AS (SELECT k AS side, (m->'counts'->'unclassified'->>k)::BIGINT AS n
                FROM manifest, unnest(json_keys(m->'counts'->'unclassified')) t(k)),
     found AS (SELECT side, count(*) AS n FROM unclassified GROUP BY side)
SELECT 'counts_unclassified', coalesce(l.side, f.side), 'manifest ' || coalesce(l.n, 0) || ', unclassified file ' || coalesce(f.n, 0)
FROM listed l FULL JOIN found f USING (side)
WHERE coalesce(l.n, 0) <> coalesce(f.n, 0);

-- The bridge ---------------------------------------------------------------------------

INSERT INTO violations
WITH found AS (SELECT asset, sum(impact) AS net FROM flow GROUP BY asset)
SELECT 'bridge_net', coalesce(s.asset, f.asset),
       'statement net ' || coalesce(s.s->>'net', 'missing') || ', SUM(impact) ' || coalesce(f.net, 0)
FROM m_statement s FULL JOIN found f USING (asset)
WHERE v_int(s.s->>'net') <> coalesce(f.net, 0);

INSERT INTO violations
SELECT 'bridge_totals', asset, 'net ' || (s->>'net') || ' <> psp ' || (s->'psp'->>'amount') || ' - product ' || (s->'product'->>'amount')
FROM m_statement
WHERE v_int(s->>'net') <> v_int(s->'psp'->>'amount') - v_int(s->'product'->>'amount');

-- The residual is B from the rows minus B from the books (the statement's product
-- total): the rows' B is every application item booked in the product window.
INSERT INTO violations
WITH window_b AS (
    SELECT f.asset, sum(f.p.amount) AS b
    FROM (SELECT asset, unnest(product) AS p FROM flow) f, m_cuts c
    WHERE c.side = 'product' AND f.p.tx > c.txFrom AND f.p.tx <= c.txTo
    GROUP BY f.asset)
SELECT 'bridge_residual', s.asset,
       'residual ' || coalesce(s.s->>'residual', 'missing') || '; window applications ' || coalesce(w.b, 0)
       || ' - statement product ' || (s.s->'product'->>'amount')
FROM m_statement s LEFT JOIN window_b w USING (asset)
WHERE v_int(s.s->>'residual') <> 0
   OR coalesce(w.b, 0) <> v_int(s.s->'product'->>'amount');

INSERT INTO violations
WITH books AS (SELECT asset, sum(lettered - letteredOther) AS b FROM m_books WHERE side = 'product' GROUP BY asset)
SELECT 'bridge_product_vs_books', s.asset, 'statement product ' || (s.s->'product'->>'amount') || ', books lettered - letteredOther ' || coalesce(b.b, 0)
FROM m_statement s LEFT JOIN books b USING (asset)
WHERE v_int(s.s->'product'->>'amount') <> coalesce(b.b, 0);

INSERT INTO violations
WITH found AS (
    SELECT f.asset, f.class, f.outcome, f.firstSeen < r.day AS earlierDay, sum(f.impact) AS amount, count(*) AS n
    FROM flow f, m_run r WHERE f.impact <> 0 GROUP BY ALL)
SELECT 'bridge_line', concat_ws('/', coalesce(l.asset, f.asset), coalesce(l.class, f.class), coalesce(l.outcome, f.outcome),
                                CASE WHEN coalesce(l.earlierDay, f.earlierDay) THEN 'earlier' ELSE 'window' END),
       'statement ' || coalesce(l.amount, 0) || ' (' || coalesce(l.count, 0) || '), flow ' || coalesce(f.amount, 0) || ' (' || coalesce(f.n, 0) || ')'
FROM m_lines l FULL JOIN found f USING (asset, class, outcome, earlierDay)
WHERE coalesce(l.amount, 0) <> coalesce(f.amount, 0) OR coalesce(l.count, 0) <> coalesce(f.n, 0);

INSERT INTO violations
WITH found AS (
    SELECT f.asset, f.class, f.outcome, sum(f.drift) AS amount, count(*) AS n
    FROM flow f, m_run r WHERE f.impact = 0 AND f.drift <> 0 AND f.firstSeen < r.day GROUP BY ALL)
SELECT 'bridge_carried_outside', concat_ws('/', coalesce(l.asset, f.asset), coalesce(l.class, f.class), coalesce(l.outcome, f.outcome)),
       'statement ' || coalesce(l.amount, 0) || ' (' || coalesce(l.count, 0) || '), flow ' || coalesce(f.amount, 0) || ' (' || coalesce(f.n, 0) || ')'
FROM m_carried_outside l FULL JOIN found f USING (asset, class, outcome)
WHERE coalesce(l.amount, 0) <> coalesce(f.amount, 0) OR coalesce(l.count, 0) <> coalesce(f.n, 0);

INSERT INTO violations
WITH found AS (SELECT asset, sum(abs(drift)) AS gross, bool_or(drift > 0) AND bool_or(drift < 0) AS offsetting
               FROM flow WHERE outcome = 'break' GROUP BY asset)
SELECT 'bridge_gross', s.asset,
       'statement flowGross ' || (s.s->>'flowGross') || ' offsetting ' || (s.s->>'offsetting')
       || ', flow ' || coalesce(f.gross, 0) || ' offsetting ' || coalesce(f.offsetting, false)
FROM m_statement s LEFT JOIN found f USING (asset)
WHERE v_int(s.s->>'flowGross') <> coalesce(f.gross, 0)
   OR (s.s->>'offsetting')::BOOLEAN IS DISTINCT FROM coalesce(f.offsetting, false);

INSERT INTO violations
WITH listed AS (SELECT asset, unnest(from_json(s->'unclassified', '[{"side":"VARCHAR","state":"VARCHAR","amount":"HUGEINT","count":"BIGINT"}]'), recursive := true)
                FROM m_statement),
     found AS (SELECT asset, side, state, sum(amount) AS amount, count(*) AS n FROM unclassified GROUP BY ALL)
SELECT 'statement_unclassified', concat_ws('/', coalesce(l.asset, f.asset), coalesce(l.side, f.side), coalesce(l.state, f.state)),
       'statement ' || coalesce(l.amount, 0) || ' (' || coalesce(l.count, 0) || '), file ' || coalesce(f.amount, 0) || ' (' || coalesce(f.n, 0) || ')'
FROM listed l FULL JOIN found f USING (asset, side, state)
WHERE coalesce(l.amount, 0) <> coalesce(f.amount, 0) OR coalesce(l.count, 0) <> coalesce(f.n, 0);

-- The open items (carried) ---------------------------------------------------------------

INSERT INTO violations
SELECT 'carried_vs_flow', coalesce(c.ref, f.ref) || '/' || coalesce(c.asset, f.asset),
       CASE WHEN c.ref IS NULL THEN 'flow row with drift ' || f.drift || ' is not carried'
            WHEN f.ref IS NULL THEN 'carried row is not a flow row with a non-zero drift'
            ELSE 'carried drift ' || c.drift || ', flow drift ' || f.drift END
FROM carried c FULL JOIN (SELECT * FROM flow WHERE drift <> 0) f USING (ref, asset)
WHERE c.ref IS NULL OR f.ref IS NULL OR c.drift <> f.drift;

INSERT INTO violations
WITH found AS (SELECT asset, sum(drift) AS open, count(*) AS n FROM carried GROUP BY asset)
SELECT 'suspense_open', s.asset,
       'statement open ' || coalesce(s.s->'suspense'->>'open', 'missing') || ' (' || coalesce(s.s->'suspense'->>'count', '?') || ')'
       || ', carried file ' || coalesce(f.open, 0) || ' (' || coalesce(f.n, 0) || ')'
FROM m_statement s LEFT JOIN found f USING (asset)
WHERE v_int(s.s->'suspense'->>'open') <> coalesce(f.open, 0)
   OR v_int(s.s->'suspense'->>'count') <> coalesce(f.n, 0);

INSERT INTO violations
SELECT 'suspense_identity', asset,
       'open ' || (s->'suspense'->>'open') || ' <> openPrev ' || (s->'suspense'->>'openPrev')
       || ' + net ' || (s->>'net') || ' + fromLookups ' || (s->'suspense'->>'fromLookups')
FROM m_statement
WHERE v_int(s->'suspense'->>'open')
      <> v_int(s->'suspense'->>'openPrev') + v_int(s->>'net') + v_int(s->'suspense'->>'fromLookups')
   OR (s->'suspense'->>'continuityOk')::BOOLEAN IS NOT TRUE;

-- The open books --------------------------------------------------------------------------

INSERT INTO violations
SELECT 'books_continuity', concat_ws('/', side, prefix, asset),
       'open ' || open || ' <> openPrev ' || openPrev || ' + opened ' || opened || ' - lettered ' || lettered
FROM m_books
WHERE open <> openPrev + opened - lettered OR continuityOk IS NOT TRUE;

INSERT INTO violations
WITH found AS (SELECT side, prefix, asset, sum(open_dir(balance, openSign)) AS open, count(*) AS n
               FROM stock WHERE class <> 'cleared' GROUP BY ALL)
SELECT 'books_vs_stock', concat_ws('/', coalesce(b.side, f.side), coalesce(b.prefix, f.prefix), coalesce(b.asset, f.asset)),
       'books ' || coalesce(b.open, 0) || ' (' || coalesce(b.count, 0) || '), stock ' || coalesce(f.open, 0) || ' (' || coalesce(f.n, 0) || ')'
FROM m_books b FULL JOIN found f USING (side, prefix, asset)
WHERE coalesce(b.open, 0) <> coalesce(f.open, 0) OR coalesce(b.count, 0) <> coalesce(f.n, 0);

-- Breaks --------------------------------------------------------------------------------

INSERT INTO violations
SELECT 'break_amount', breakId,
       'amount ' || amount || ', expected ' ||
       CASE WHEN leg = 'flow' THEN 'the drift ' || drift
            WHEN lifecycle = 'resolved' THEN 'the previous balance in the open direction ' || open_dir(previousBalance, openSign)
            ELSE 'the balance in the open direction ' || open_dir(balance, openSign) END
FROM breaks
WHERE (leg = 'flow' AND lifecycle <> 'resolved' AND amount IS DISTINCT FROM drift)
   OR (leg = 'stock' AND lifecycle <> 'resolved' AND amount IS DISTINCT FROM open_dir(balance, openSign))
   OR (leg = 'stock' AND lifecycle = 'resolved' AND previousBalance IS NOT NULL
       AND amount IS DISTINCT FROM open_dir(previousBalance, openSign));

INSERT INTO violations
SELECT 'break_outcome', breakId, 'lifecycle ' || lifecycle || ' with outcome ' || outcome
FROM breaks
WHERE (lifecycle = 'resolved') <> (outcome = 'ok') OR outcome NOT IN ('ok', 'break');

INSERT INTO violations
SELECT 'break_priority', breakId, class || ' has priority ' || priority
FROM breaks
WHERE priority IS DISTINCT FROM CASE
    WHEN class IN ('orphan_application', 'reversed_after_application') THEN 1
    WHEN class IN ('under_applied', 'over_applied') THEN 2
    WHEN class = 'unapplied_payment' THEN 3
    WHEN class IN ('stuck', 'wrong_sign') THEN 4 END;

INSERT INTO violations
WITH t AS (SELECT unnest(from_json(m->'triage'->'breaks', '[{"breakId":"VARCHAR","priority":"INTEGER","class":"VARCHAR","lifecycle":"VARCHAR","amount":"HUGEINT"}]'), recursive := true) FROM manifest
           UNION ALL
           SELECT unnest(from_json(m->'triage'->'resolved', '[{"breakId":"VARCHAR","priority":"INTEGER","class":"VARCHAR","lifecycle":"VARCHAR","amount":"HUGEINT"}]'), recursive := true) FROM manifest)
SELECT 'triage_break', t.breakId,
       CASE WHEN b.breakId IS NULL THEN 'not in the breaks file'
            ELSE 'triage ' || t.class || ' ' || t.amount || ', breaks file ' || b.class || ' ' || b.amount END
FROM t LEFT JOIN breaks b USING (breakId)
WHERE b.breakId IS NULL OR t.class <> b.class OR t.amount <> b.amount
   OR (t.priority IS NOT NULL AND t.priority <> b.priority)
   OR (t.lifecycle IS NOT NULL AND t.lifecycle <> b.lifecycle);

INSERT INTO violations
WITH t AS (SELECT unnest(from_json(m->'triage'->'pending', '[{"ref":"VARCHAR","class":"VARCHAR","asset":"VARCHAR","amount":"HUGEINT","breakOn":"DATE"}]'), recursive := true) FROM manifest)
SELECT 'triage_pending', t.ref,
       CASE WHEN f.ref IS NULL THEN 'not a pending flow row'
            ELSE 'triage ' || t.amount || ' on ' || t.breakOn || ', flow ' || f.drift || ' on ' || f.breakOn END
FROM t LEFT JOIN (SELECT * FROM flow WHERE outcome = 'pending') f USING (ref, asset)
WHERE f.ref IS NULL OR t.amount <> f.drift OR t.breakOn IS DISTINCT FROM f.breakOn OR t.class <> f.class;

-- Every open break of the flow and stock files is in the breaks file, and back.
INSERT INTO violations
WITH open_rows AS (
    SELECT 'flow' AS leg, ref AS key, asset FROM flow WHERE outcome = 'break'
    UNION ALL SELECT 'stock', side || '/' || hold, asset FROM stock WHERE outcome = 'break'),
     open_breaks AS (
    SELECT leg, CASE leg WHEN 'flow' THEN ref ELSE side || '/' || hold END AS key, asset
    FROM breaks WHERE outcome = 'break')
SELECT 'breaks_vs_rows', coalesce(r.leg, b.leg) || ' ' || coalesce(r.key, b.key) || '/' || coalesce(r.asset, b.asset),
       CASE WHEN b.key IS NULL THEN 'a break in its ' || r.leg || ' file, missing from the breaks file'
            ELSE 'an open break with no break row in its ' || b.leg || ' file' END
FROM open_rows r FULL JOIN open_breaks b USING (leg, key, asset)
WHERE r.key IS NULL OR b.key IS NULL;

-- Rows (results doc §6) ---------------------------------------------------------------------

-- A flow row's amounts follow from its transactions. pspAmount sums the final events'
-- amounts, and is 0 once a failed event follows a final one; productAmount sums the applications.
INSERT INTO violations
WITH failed AS (SELECT from_json(m->'rule'->'psp'->'state'->'failed', '["VARCHAR"]') AS states FROM manifest),
     flow_rows AS (
        SELECT 'flow' AS f, ref, asset, pspAmount, productAmount, drift, psp, product FROM flow
        UNION ALL SELECT 'carried', ref, asset, pspAmount, productAmount, drift, psp, product FROM carried
        UNION ALL SELECT 'breaks', ref, asset, pspAmount, productAmount, drift, psp, product
                  FROM breaks WHERE leg = 'flow' AND lifecycle <> 'resolved'),
     expected AS (
        SELECT r.*,
               CASE WHEN list_bool_or(list_transform(r.psp, e -> list_contains(failed.states, e.state)
                         AND e.tx > list_min(list_transform(list_filter(r.psp, x -> x.amount IS NOT NULL), x -> x.tx))))
                    THEN 0
                    ELSE coalesce(list_sum(list_transform(r.psp, e -> e.amount)), 0) END AS expected_psp,
               coalesce(list_sum(list_transform(r.product, p -> p.amount)), 0) AS expected_product
        FROM flow_rows r, failed)
SELECT 'row_amounts', f || ' ' || ref || '/' || asset,
       'psp ' || pspAmount || ' product ' || productAmount || ' drift ' || drift
       || ', transactions give psp ' || expected_psp || ' product ' || expected_product
FROM expected
WHERE pspAmount <> expected_psp OR productAmount <> expected_product OR drift <> pspAmount - productAmount;

INSERT INTO violations
SELECT 'row_impact', 'carried ' || ref || '/' || asset, 'a carried row has no impact'
FROM carried WHERE impact IS NOT NULL;

-- A row's outcome follows from its class (results doc §6 class tables).
INSERT INTO violations
SELECT 'row_outcome', 'flow ' || f.ref || '/' || f.asset, f.class || ' with outcome ' || f.outcome
FROM flow f, m_run r
WHERE NOT CASE
    WHEN f.class IN ('matched', 'in_progress', 'failed') THEN f.outcome = 'ok'
    WHEN f.class IN ('under_applied', 'over_applied', 'orphan_application', 'reversed_after_application') THEN f.outcome = 'break'
    WHEN f.class = 'unapplied_payment' THEN (f.outcome = 'pending' AND f.breakOn > r.day)
                                         OR (f.outcome = 'break' AND f.breakOn <= r.day)
    WHEN f.class = 'applied_before_final' THEN f.outcome = 'pending' AND f.breakOn > r.day
    ELSE false END
UNION ALL
SELECT 'row_outcome', 'stock ' || side || '/' || hold || '/' || asset,
       class || ' with outcome ' || outcome || ' and balance ' || balance
FROM stock
WHERE NOT CASE class
    WHEN 'open' THEN outcome = 'ok' AND open_dir(balance, openSign) > 0
    WHEN 'stuck' THEN outcome = 'break' AND open_dir(balance, openSign) > 0
    WHEN 'wrong_sign' THEN outcome = 'break' AND open_dir(balance, openSign) < 0
    WHEN 'cleared' THEN outcome = 'ok' AND balance = 0
    ELSE false END;

-- A class reads the net amounts: applications that sum to 0 count as none.
INSERT INTO violations
SELECT 'row_class', 'flow ' || ref || '/' || asset, class || ' with psp ' || pspAmount || ' product ' || productAmount
FROM flow
WHERE NOT CASE
    WHEN class IN ('unapplied_payment', 'in_progress', 'failed') THEN productAmount = 0
    WHEN class = 'matched' THEN productAmount <> 0 AND productAmount = pspAmount
    WHEN class = 'under_applied' THEN productAmount <> 0 AND productAmount < pspAmount
    WHEN class = 'over_applied' THEN productAmount <> 0 AND productAmount > pspAmount
    ELSE productAmount <> 0 END;

-- A pending or break row has a drift; a matched, in-progress or failed one has none.
INSERT INTO violations
SELECT 'row_drift', 'flow ' || ref || '/' || asset, class || ' ' || outcome || ' with drift ' || drift
FROM flow
WHERE (outcome IN ('pending', 'break') AND drift = 0)
   OR (class IN ('matched', 'in_progress', 'failed') AND drift <> 0);

-- breakOn is firstSeen plus the lagging side's grace.
INSERT INTO violations
SELECT 'row_break_on', 'flow ' || f.ref || '/' || f.asset,
       f.class || ': breakOn ' || coalesce(f.breakOn::VARCHAR, 'missing') || ', firstSeen ' || coalesce(f.firstSeen::VARCHAR, 'missing')
       || ' + grace ' || coalesce(CASE WHEN f.class = 'unapplied_payment' THEN g.product_grace ELSE g.psp_grace END::VARCHAR, '?')
FROM flow f, v_rule g
WHERE (f.class = 'unapplied_payment' AND f.breakOn IS DISTINCT FROM f.firstSeen + g.product_grace)
   OR (f.class = 'applied_before_final' AND f.breakOn IS DISTINCT FROM f.firstSeen + g.psp_grace)
   OR (f.class = 'orphan_application' AND f.breakOn IS NOT NULL AND f.breakOn <> f.firstSeen + g.psp_grace);

-- A hold's age is counted in the rule's timezone; it is stuck when older than its side's maxAge,
-- and its bucket follows from its age.
INSERT INTO violations
WITH aged AS (
    SELECT s.*, r.day - timezone(g.tz, s.openedAt AT TIME ZONE 'UTC')::DATE AS age,
           CASE s.side WHEN 'psp' THEN g.psp_max_age ELSE g.product_max_age END AS max_age, g.bounds
    FROM stock s, m_run r, v_rule g)
SELECT 'stock_age', side || '/' || hold || '/' || asset,
       class || ', ageDays ' || ageDays || ' (opened ' || age || ' days before the cut), bucket ' || bucket
       || ', maxAge ' || coalesce(max_age::VARCHAR, 'none')
FROM aged
WHERE ageDays <> age
   OR bucket IS DISTINCT FROM v_bucket(ageDays, bounds)
   OR (class = 'stuck' AND NOT (max_age IS NOT NULL AND ageDays > max_age))
   OR (class = 'open' AND max_age IS NOT NULL AND ageDays > max_age);

INSERT INTO violations
WITH listed AS (
        SELECT b.side, b.prefix, b.asset, k AS bucket, (m->'books'->(b.i - 1)::INTEGER->'buckets'->>k)::BIGINT AS n
        FROM (SELECT m, unnest(from_json(m->'books', '[{"side":"VARCHAR","prefix":"VARCHAR","asset":"VARCHAR"}]'), recursive := true),
                     generate_subscripts(from_json(m->'books', '["JSON"]'), 1) AS i FROM manifest) b,
             unnest(json_keys(m->'books'->(b.i - 1)::INTEGER->'buckets')) t(k)),
     found AS (SELECT side, prefix, asset, bucket, count(*) AS n FROM stock WHERE class <> 'cleared' GROUP BY ALL)
SELECT 'books_buckets', concat_ws('/', coalesce(l.side, f.side), coalesce(l.prefix, f.prefix), coalesce(l.asset, f.asset), coalesce(l.bucket, f.bucket)),
       'books ' || coalesce(l.n, 0) || ', stock ' || coalesce(f.n, 0)
FROM listed l FULL JOIN found f USING (side, prefix, asset, bucket)
WHERE coalesce(l.n, 0) <> coalesce(f.n, 0);

-- An open break is the row it stands for: same class, and its amount is that row's.
INSERT INTO violations
SELECT 'break_vs_row', b.breakId, 'break ' || b.class || ' ' || b.amount || ', flow row ' || f.class || ' ' || f.drift
FROM breaks b JOIN flow f USING (ref, asset)
WHERE b.leg = 'flow' AND b.outcome = 'break' AND (b.class <> f.class OR b.amount <> f.drift)
UNION ALL
SELECT 'break_vs_row', b.breakId, 'break ' || b.class || ' ' || b.amount || ', stock row ' || s.class || ' ' || open_dir(s.balance, s.openSign)
FROM breaks b JOIN stock s USING (side, hold, asset)
WHERE b.leg = 'stock' AND b.outcome = 'break' AND (b.class <> s.class OR b.amount <> open_dir(s.balance, s.openSign));

-- The triage lists the first topK open breaks, pending rows and resolved breaks.
INSERT INTO violations
WITH t AS (SELECT (m->'triage'->>'topK')::INTEGER AS top_k,
                  json_array_length(m->'triage'->'breaks') AS n_breaks,
                  json_array_length(m->'triage'->'pending') AS n_pending,
                  json_array_length(m->'triage'->'resolved') AS n_resolved FROM manifest)
SELECT 'triage_count', 'breaks', 'triage ' || n_breaks || ', expected ' || least(top_k, (SELECT count(*) FROM breaks WHERE outcome = 'break'))
FROM t WHERE n_breaks <> least(top_k, (SELECT count(*) FROM breaks WHERE outcome = 'break'))
UNION ALL
SELECT 'triage_count', 'pending', 'triage ' || n_pending || ', expected ' || least(top_k, (SELECT count(*) FROM flow WHERE outcome = 'pending'))
FROM t WHERE n_pending <> least(top_k, (SELECT count(*) FROM flow WHERE outcome = 'pending'))
UNION ALL
SELECT 'triage_count', 'resolved', 'triage ' || n_resolved || ', expected ' || least(top_k, (SELECT count(*) FROM breaks WHERE lifecycle = 'resolved'))
FROM t WHERE n_resolved <> least(top_k, (SELECT count(*) FROM breaks WHERE lifecycle = 'resolved'));

-- Keys and order (results doc §8) -----------------------------------------------------------

INSERT INTO violations
SELECT 'unique_key', 'flow ' || ref || '/' || asset, count(*) || ' rows' FROM flow GROUP BY ref, asset HAVING count(*) > 1
UNION ALL SELECT 'unique_key', 'carried ' || ref || '/' || asset, count(*) || ' rows' FROM carried GROUP BY ref, asset HAVING count(*) > 1
UNION ALL SELECT 'unique_key', 'stock ' || side || '/' || hold || '/' || asset, count(*) || ' rows' FROM stock GROUP BY side, hold, asset HAVING count(*) > 1
UNION ALL SELECT 'unique_key', 'breaks ' || breakId, count(*) || ' rows' FROM breaks GROUP BY breakId HAVING count(*) > 1
UNION ALL SELECT 'unique_key', 'unclassified ' || side || '/' || tx || '/' || asset, count(*) || ' rows' FROM unclassified GROUP BY side, tx, asset HAVING count(*) > 1;

-- Rows are read in file order; each file's key must never go backwards.
INSERT INTO violations
WITH keyed AS (
    SELECT 'flow' AS f, file, pos, ref || '/' || asset AS key, struct_pack(a := ref, b := asset) AS k FROM flow
    UNION ALL SELECT 'carried', file, pos, ref || '/' || asset, struct_pack(a := ref, b := asset) FROM carried),
     ordered AS (SELECT f, key, k, lag(k) OVER (PARTITION BY f ORDER BY file, pos) AS prev FROM keyed)
SELECT 'row_order', f || ' ' || key, 'follows a greater key' FROM ordered WHERE k < prev;

INSERT INTO violations
WITH ordered AS (SELECT side || '/' || hold || '/' || asset AS key, struct_pack(a := side, b := hold, c := asset) AS k,
                        lag(struct_pack(a := side, b := hold, c := asset)) OVER (ORDER BY file, pos) AS prev FROM stock)
SELECT 'row_order', 'stock ' || key, 'follows a greater key' FROM ordered WHERE k < prev;

INSERT INTO violations
WITH ordered AS (SELECT breakId, struct_pack(a := CASE WHEN outcome = 'break' THEN 0 ELSE 1 END, b := priority, c := -abs(amount)) AS k,
                        lag(struct_pack(a := CASE WHEN outcome = 'break' THEN 0 ELSE 1 END, b := priority, c := -abs(amount))) OVER (ORDER BY file, pos) AS prev
                 FROM breaks)
SELECT 'row_order', 'breaks ' || breakId, 'open before resolved, then priority, then |amount| descending' FROM ordered WHERE k < prev;

INSERT INTO violations
WITH ordered AS (SELECT side || '/' || tx || '/' || asset AS key, struct_pack(a := side, b := tx, c := asset) AS k,
                        lag(struct_pack(a := side, b := tx, c := asset)) OVER (ORDER BY file, pos) AS prev FROM unclassified)
SELECT 'row_order', 'unclassified ' || key, 'follows a greater key' FROM ordered WHERE k < prev;

-- The verdict ------------------------------------------------------------------------------

INSERT INTO violations
WITH expected AS (
    SELECT CASE
        WHEN (SELECT count(*) FROM breaks WHERE outcome = 'break') > 0 THEN 'breaks'
        WHEN (SELECT count(*) FROM unclassified) > 0
          OR (SELECT coalesce(json_array_length(m->'anomalies'->'key_metadata_mutated'), 0) FROM manifest) > 0
            THEN 'reconciled_with_warnings'
        WHEN (SELECT count(*) FROM flow WHERE outcome = 'pending') > 0 THEN 'reconciled_with_pending'
        ELSE 'reconciled' END AS verdict)
SELECT 'verdict_mismatch', r.run_id, 'manifest ' || r.verdict || ', files say ' || e.verdict
FROM m_run r, expected e WHERE r.verdict <> 'incomplete' AND r.verdict <> e.verdict;

-- Report ------------------------------------------------------------------------------------

SELECT rule, key, detail FROM violations ORDER BY rule, key;

SELECT CASE WHEN count(*) = 0 THEN 'ok: the run is sound'
            ELSE error(count(*) || ' violation(s) of the lettering/1 rules') END AS result
FROM violations;
