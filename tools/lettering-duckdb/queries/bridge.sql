-- Why is the day's net difference what it is? Rebuilds the statement's bridge from
-- the flow file alone.
-- Variables: rule, day (optional, default the latest day).
-- Columns: asset, class, outcome, earlier_day, amount, payments. The amounts add
-- up to the day's net difference, psp - product.
SELECT asset, class, outcome, coalesce(firstSeen < day, false) AS earlier_day,
       sum(impact) AS amount, count(*) AS payments
FROM flow_days
WHERE day = lettering_target_day() AND impact <> 0
GROUP BY ALL
ORDER BY asset, amount DESC;
