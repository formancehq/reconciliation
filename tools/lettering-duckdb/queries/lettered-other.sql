-- Which holds did a transaction with no PSP reference (a credit note, a write-off) letter
-- to zero? One row per such hold. The books query gives each book's lettered_other total,
-- which also counts the holds that unclassified transactions with a reference cleared.
-- Variables: rule.
-- Columns: day, side, prefix, asset, hold, amount (open direction), cleared_at.
SELECT day, side, prefix, asset, hold, open_dir(previousBalance, openSign) AS amount, clearedAt AS cleared_at
FROM stock_days
WHERE class = 'cleared' AND clearedBy IS NULL AND open_dir(previousBalance, openSign) > 0
ORDER BY day, side, prefix, hold;
