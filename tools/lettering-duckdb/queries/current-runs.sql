-- Which run counts for each day?
-- Variables: rule.
-- Columns: day, run, verdict, current (true for the day's current run: its latest
-- complete run), reason (why an incomplete run concluded nothing).
SELECT r.day, r.run, r.verdict,
       c.run IS NOT NULL AS current,
       r.m->'incomplete'->>'reason' AS reason
FROM runs r LEFT JOIN current_runs c USING (day, run)
ORDER BY r.day, r.run;
