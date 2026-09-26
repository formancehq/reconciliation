-- What did each day's flow contain, class by class? A payment shows as `matched`
-- on the day it was lettered, so the matched rows are the day's reconciled payments.
-- A row still open is carried in, and shows again, every day until it is matched:
-- sum `impact`, the day's own movement, across days, never the amounts.
-- Variables: rule.
-- Columns: day, asset, class, outcome, payments, psp_amount, product_amount, drift,
-- impact.
SELECT day, asset, class, outcome, count(*) AS payments,
       sum(pspAmount) AS psp_amount, sum(productAmount) AS product_amount, sum(drift) AS drift,
       sum(impact) AS impact
FROM flow_days
GROUP BY ALL
ORDER BY day, asset, class, outcome;
