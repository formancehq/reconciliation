-- What must be done today? The open breaks that are not accepted, most urgent first.
-- Variables: rule, day (optional, default the latest day).
-- Columns: priority, class, lifecycle, asset, amount, ref, hold, opened_on,
-- days_open, detail. `amount` is the drift (psp - product) of a flow break, and the
-- balance in the hold's open direction for a stock break.
SELECT priority, class, lifecycle, asset, amount, ref, hold, openedOn AS opened_on,
       day - openedOn AS days_open,
       CASE leg WHEN 'flow' THEN nullif(array_to_string(list_sort(list_distinct(list_transform(product, p -> p.holdId))), ', '), '')
                ELSE ageDays || ' days old' END AS detail
FROM breaks_days
WHERE day = lettering_target_day() AND outcome = 'break' AND acceptedOn IS NULL
ORDER BY priority, abs(amount) DESC;
