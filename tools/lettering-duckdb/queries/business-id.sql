-- Everything about one business id (an invoice, a refund) or one payment reference,
-- over every day of the rule.
-- Variables: rule, id (required: INV-12, PAY-45…).
-- Columns: day, file, class, outcome, ref, hold, amount, detail. `amount` is the
-- drift (psp - product) on a flow row, the balance in the open direction on a stock
-- row, and the break's amount on a break row.
SELECT day, 'flow' AS file, class, outcome, ref, pairedHold AS hold, drift AS amount,
       'psp ' || pspAmount || ', product ' || productAmount AS detail
FROM flow_days
WHERE ref = getvariable('id') OR merchantRef = getvariable('id')
   OR list_contains(list_transform(product, p -> p.businessId), getvariable('id'))
   OR list_contains(list_transform(product, p -> p.holdId), getvariable('id'))
UNION ALL
SELECT day, 'stock', class, outcome, coalesce(clearedBy, pairedRef), hold, open_dir(balance, openSign),
       lifecycle || ', ' || ageDays || ' days old'
FROM stock_days
WHERE holdId = getvariable('id') OR clearedBy = getvariable('id') OR pairedRef = getvariable('id')
UNION ALL
SELECT day, 'breaks', class, outcome, ref, hold, amount,
       'P' || priority
       || CASE WHEN lifecycle = 'resolved' THEN ' resolved on ' || resolvedOn
               ELSE ' ' || lifecycle || ' since ' || openedOn END
       || CASE WHEN acceptedOn IS NOT NULL THEN ', accepted on ' || acceptedOn ELSE '' END
FROM breaks_days
WHERE ref = getvariable('id') OR holdId = getvariable('id') OR merchantRef = getvariable('id')
   OR list_contains(list_transform(product, p -> p.businessId), getvariable('id'))
   OR list_contains(list_transform(product, p -> p.holdId), getvariable('id'))
UNION ALL
SELECT day, 'unclassified', state, outcome, ref, NULL, amount, 'tx ' || tx
FROM unclassified_days
WHERE ref = getvariable('id')
ORDER BY day, file;
