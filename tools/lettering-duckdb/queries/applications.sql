-- One row per product application booked on the day, for a spreadsheet or a BI tool.
-- A row carried in from an earlier day lists its earlier applications too; they are
-- left out here (only the product side's window (txFrom, txTo] counts), so several
-- days of this query never count an application twice.
-- Variables: rule, day (optional, default the latest day).
-- Columns: day, ref, class, outcome, tx, business_id, hold_id, amount, inserted_at.
SELECT f.day, f.ref, f.class, f.outcome, f.p.tx, f.p.businessId AS business_id,
       f.p.holdId AS hold_id, f.p.amount, f.p.insertedAt AS inserted_at
FROM (SELECT day, run, ref, class, outcome, unnest(product) AS p FROM flow_days
      WHERE day = lettering_target_day()) f
JOIN cuts_days c ON c.day = f.day AND c.run = f.run AND c.side = 'product'
WHERE f.p.tx > c.txFrom AND f.p.tx <= c.txTo
ORDER BY f.ref, f.p.tx, f.p.holdId;
