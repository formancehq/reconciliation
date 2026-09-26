-- How old is what waits for payment? The open holds per age bucket, day after day.
-- Variables: rule.
-- Columns: day, side, prefix, asset, bucket, holds, amount (open direction),
-- stuck, wrong_sign.
SELECT day, side, prefix, asset, bucket, count(*) AS holds,
       sum(open_dir(balance, openSign)) AS amount,
       count(*) FILTER (WHERE class = 'stuck') AS stuck,
       count(*) FILTER (WHERE class = 'wrong_sign') AS wrong_sign
FROM stock_days
WHERE class <> 'cleared'
GROUP BY ALL
ORDER BY day, side, prefix, asset, min(ageDays);
