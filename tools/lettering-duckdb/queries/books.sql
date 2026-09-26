-- What is open on each ledger, day after day? The open books, in each hold's open
-- direction (an unpaid invoice counts positive).
-- Variables: rule.
-- Columns: day, side, prefix, asset, open_prev, opened, lettered, lettered_other,
-- open, holds.
SELECT day, side, prefix, asset, openPrev AS open_prev, opened, lettered,
       letteredOther AS lettered_other, open, count AS holds
FROM books_days
ORDER BY day, side, prefix, asset;
