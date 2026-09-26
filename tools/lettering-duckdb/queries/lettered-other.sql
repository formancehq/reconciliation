-- What was lettered outside matching (credit notes, write-offs, unclassified
-- transactions)? One `book` row per book and day with its total, then one `hold` row
-- per hold cleared with no PSP reference.
-- Variables: rule.
-- Columns: day, kind, side, prefix, asset, hold, amount (open direction), cleared_at.
SELECT day, 'book' AS kind, side, prefix, asset, NULL AS hold, letteredOther AS amount,
       NULL::TIMESTAMP AS cleared_at
FROM books_days
WHERE letteredOther <> 0
UNION ALL
SELECT day, 'hold', side, prefix, asset, hold, open_dir(previousBalance, openSign), clearedAt
FROM stock_days
WHERE class = 'cleared' AND clearedBy IS NULL
ORDER BY day, side, prefix, kind, hold;
