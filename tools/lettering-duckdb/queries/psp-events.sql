-- One row per PSP event booked on the day, for a spreadsheet or a BI tool. A final
-- event carries `amount`; a pending or failed one carries `hold_amount` instead. Only
-- the PSP side's window (txFrom, txTo] counts, so the earlier events of a row carried
-- in are left out.
-- Variables: rule, day (optional, default the latest day).
-- Columns: day, ref, class, outcome, tx, state, amount, hold_amount, inserted_at.
SELECT f.day, f.ref, f.class, f.outcome, f.e.tx, f.e.state, f.e.amount,
       f.e.holdAmount AS hold_amount, f.e.insertedAt AS inserted_at
FROM (SELECT day, run, ref, class, outcome, unnest(psp) AS e FROM flow_days
      WHERE day = lettering_target_day()) f
JOIN cuts_days c ON c.day = f.day AND c.run = f.run AND c.side = 'psp'
WHERE f.e.tx > c.txFrom AND f.e.tx <= c.txTo
ORDER BY f.ref, f.e.tx;
