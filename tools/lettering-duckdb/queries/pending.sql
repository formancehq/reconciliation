-- What turns into a break soon? The pending payments and applications, with the
-- day each becomes a break.
-- Variables: rule, day (optional, default the latest day).
-- Columns: break_on, days_left, class, asset, amount, ref, merchant_ref, paired_hold,
-- applied_to.
SELECT breakOn AS break_on, breakOn - day AS days_left, class, asset, drift AS amount, ref,
       merchantRef AS merchant_ref, pairedHold AS paired_hold,
       list_sort(list_distinct(list_transform(product, p -> p.holdId))) AS applied_to
FROM flow_days
WHERE day = lettering_target_day() AND outcome = 'pending'
ORDER BY breakOn, abs(drift) DESC;
