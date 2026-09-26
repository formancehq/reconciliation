-- How much is still unmatched, day after day? The statement's open items: the sum
-- of the carried drifts, which closes from one day to the next.
-- Variables: rule.
-- Columns: day, asset, verdict, open_prev, net, from_lookups, open, items,
-- open_breaks_gross.
SELECT day, asset, verdict,
       (s->'suspense'->>'openPrev')::HUGEINT AS open_prev,
       (s->>'net')::HUGEINT AS net,
       (s->'suspense'->>'fromLookups')::HUGEINT AS from_lookups,
       (s->'suspense'->>'open')::HUGEINT AS open,
       (s->'suspense'->>'count')::BIGINT AS items,
       (s->>'flowGross')::HUGEINT AS open_breaks_gross
FROM statement_days
ORDER BY day, asset;
