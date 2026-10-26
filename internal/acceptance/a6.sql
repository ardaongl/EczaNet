WITH expected AS (
 SELECT m.event_id,s.location_id,a.lot_id,sum(a.quantity) qty FROM "pl-seed-history-sales" m
 JOIN "pl-sales" s ON s.id=m.sale_id JOIN "pl-sale-lines" l ON l.sale_id=s.id
 JOIN "pl-sale-allocations" a ON a.line_id=l.id GROUP BY 1,2,3
),actual AS (
 SELECT e.event_id,e.location_id,e.lot_id,
 sum(quantity) FILTER(WHERE account='physical') physical,
 sum(quantity) FILTER(WHERE account='sold') sold,
 count(*) FILTER(WHERE account NOT IN ('physical','sold')) other
 FROM "pl-stock-entries" e JOIN "pl-stock-events" h ON h.id=e.event_id WHERE h.kind='seed.history' GROUP BY 1,2,3
)
SELECT 'seed_history_document_ledger',count(*) FROM expected x FULL JOIN actual a USING(event_id,location_id,lot_id)
WHERE x.event_id IS NULL OR a.event_id IS NULL OR a.physical IS DISTINCT FROM -x.qty OR a.sold IS DISTINCT FROM x.qty OR a.other<>0
UNION ALL SELECT 'sale_allocation_quantity',count(*) FROM "pl-sale-lines" l
WHERE l.quantity<>(SELECT coalesce(sum(quantity),0) FROM "pl-sale-allocations" a WHERE a.line_id=l.id);
