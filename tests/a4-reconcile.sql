\set ON_ERROR_STOP on
CREATE TEMP TABLE "pl-a4-checks"(name text,violations bigint);
INSERT INTO "pl-a4-checks"
 SELECT 'stock_event_balance',count(*) FROM (SELECT event_id,lot_id FROM "pl-stock-entries" GROUP BY event_id,lot_id HAVING sum(quantity)<>0) x
 UNION ALL
 SELECT 'balance_vs_entries',count(*) FROM "pl-stock-balances" b WHERE quantity<>(SELECT coalesce(sum(quantity),0) FROM "pl-stock-entries" e WHERE (e.location_id,e.lot_id,e.account)=(b.location_id,b.lot_id,b.account))
 UNION ALL
 SELECT 'negative_physical',count(*) FROM "pl-stock-balances" WHERE account NOT IN ('supply','sold','count') AND quantity<0
 UNION ALL
 SELECT 'return_line_quantity',count(*) FROM "pl-sale-lines" l WHERE (SELECT coalesce(sum(quantity),0) FROM "pl-return-lines" r WHERE r.sale_line_id=l.id)>l.quantity
 UNION ALL
 SELECT 'return_lot_quantity',count(*) FROM "pl-sale-allocations" a WHERE (SELECT coalesce(sum(quantity),0) FROM "pl-return-lines" r WHERE r.sale_line_id=a.line_id AND r.lot_id=a.lot_id)>a.quantity
 UNION ALL
 SELECT 'return_snapshot_shares',count(*) FROM "pl-return-lines" r JOIN "pl-sale-lines" l ON l.id=r.sale_line_id WHERE
 r.patient_total<>round(coalesce((l.snapshot->'pricing'->>'patient_total')::numeric,l.total)*r.cumulative_quantity/l.quantity)-round(coalesce((l.snapshot->'pricing'->>'patient_total')::numeric,l.total)*(r.cumulative_quantity-r.quantity)/l.quantity)
 OR r.institution_total<>round(coalesce((l.snapshot->'pricing'->>'institution_total')::numeric,0)*r.cumulative_quantity/l.quantity)-round(coalesce((l.snapshot->'pricing'->>'institution_total')::numeric,0)*(r.cumulative_quantity-r.quantity)/l.quantity)
 UNION ALL
 SELECT 'return_header_shares',count(*) FROM "pl-returns" r WHERE r.patient_total<>(SELECT coalesce(sum(patient_total),0) FROM "pl-return-lines" l WHERE return_id=r.id) OR r.institution_total<>(SELECT coalesce(sum(institution_total),0) FROM "pl-return-lines" l WHERE return_id=r.id)
 UNION ALL
 SELECT 'return_source_event',count(*) FROM "pl-return-lines" r JOIN "pl-returns" h ON h.id=r.return_id WHERE NOT EXISTS(SELECT 1 FROM "pl-quarantine-items" i JOIN "pl-stock-events" e ON e.id=i.event_id WHERE i.return_line_id=r.id AND i.lot_id=r.lot_id AND i.location_id=h.location_id AND i.quantity=r.quantity AND e.kind='return' AND e.reference=h.id::text)
 UNION ALL
 SELECT 'controlled_return_reference',count(*) FROM "pl-controlled-returns" r JOIN "pl-return-lines" l ON l.id=r.return_line_id JOIN "pl-controlled-ledger" c ON c.id=r.ledger_id WHERE r.quantity<>l.quantity OR c.sale_line_id<>l.sale_line_id OR c.lot_id<>l.lot_id
 UNION ALL
 SELECT 'quarantine_source_overconsumed',count(*) FROM "pl-quarantine-items" i WHERE i.quantity < (SELECT coalesce(sum(quantity),0) FROM "pl-quarantine-decisions" WHERE item_id=i.id)+(SELECT coalesce(sum(quantity),0) FROM "pl-quarantine-count-losses" WHERE item_id=i.id)
 UNION ALL
 SELECT 'quarantine_sources_vs_balance',count(*) FROM (SELECT location_id,lot_id,sum(i.quantity-(SELECT coalesce(sum(quantity),0) FROM "pl-quarantine-decisions" WHERE item_id=i.id)-(SELECT coalesce(sum(quantity),0) FROM "pl-quarantine-count-losses" WHERE item_id=i.id)) AS remaining FROM "pl-quarantine-items" i GROUP BY location_id,lot_id) x WHERE x.remaining>coalesce((SELECT quantity FROM "pl-stock-balances" b WHERE (b.location_id,b.lot_id,b.account)=(x.location_id,x.lot_id,'quarantine')),0)
 UNION ALL
 SELECT 'new_transfer_receipt_equation',count(*) FROM "pl-transfer-lines" l JOIN "pl-transfers" t ON t.id=l.transfer_id WHERE EXISTS(SELECT 1 FROM "pl-transfer-receipts" WHERE transfer_id=t.id) AND l.quantity<>(SELECT coalesce(sum(r.good::numeric+r.damaged+r.missing),0) FROM "pl-transfer-receipt-lines" r WHERE r.transfer_id=t.id AND r.lot_id=l.lot_id)+coalesce((SELECT quantity FROM "pl-stock-balances" WHERE location_id=t.source_id AND lot_id=l.lot_id AND account='transit:'||t.id),0)
 UNION ALL
 SELECT 'closed_transfer_transit',count(*) FROM "pl-transfers" t JOIN "pl-stock-balances" b ON b.location_id=t.source_id AND b.account='transit:'||t.id WHERE t.status='CLOSED' AND b.quantity<>0
 UNION ALL
 SELECT 'nonfinal_missing',count(*) FROM "pl-transfer-receipt-lines" l JOIN "pl-transfer-receipts" r ON r.id=l.receipt_id WHERE NOT r.final AND l.missing<>0
 UNION ALL
 SELECT 'count_expected_frozen',count(*) FROM "pl-count-lines" l JOIN "pl-counts" c ON c.id=l.count_id WHERE c.status='OPEN' AND c.expires_at>clock_timestamp() AND l.expected<>coalesce((SELECT quantity FROM "pl-stock-balances" b WHERE (b.location_id,b.lot_id,b.account)=(c.location_id,l.lot_id,l.account)),0)
 UNION ALL
 SELECT 'count_results_state',count(*) FROM "pl-count-results" r JOIN "pl-counts" c ON c.id=r.count_id WHERE c.status<>'COMPLETED'
 UNION ALL
 SELECT 'incomplete_idempotency',count(*) FROM "pl-idempotency" WHERE status IS NULL OR result IS NULL;
INSERT INTO "pl-a4-checks"
 SELECT 'order_received_vs_receipts',count(*) FROM "pl-order-lines" l WHERE received<>(SELECT coalesce(sum(accepted),0) FROM "pl-order-receipt-lines" r WHERE r.line_id=l.id)
 UNION ALL SELECT 'allocation_bounds_and_need',count(*) FROM "pl-order-allocations" a JOIN "pl-needs" n ON n.id=a.need_id JOIN "pl-order-lines" l ON l.id=a.line_id WHERE a.quantity<>n.quantity OR a.location_id<>n.location_id OR l.product_id<>n.product_id OR n.status<>'APPROVED' OR a.received+a.cancelled>a.quantity
 UNION ALL SELECT 'line_allocation_bounds',count(*) FROM "pl-order-lines" l WHERE l.quantity<(SELECT coalesce(sum(quantity),0) FROM "pl-order-allocations" WHERE line_id=l.id) OR l.received<(SELECT coalesce(sum(received),0) FROM "pl-order-allocations" WHERE line_id=l.id) OR l.cancelled<(SELECT coalesce(sum(cancelled),0) FROM "pl-order-allocations" WHERE line_id=l.id)
 UNION ALL SELECT 'allocation_vs_lots',count(*) FROM "pl-order-allocations" a WHERE a.received<>(SELECT coalesce(sum(quantity),0) FROM "pl-allocation-lots" WHERE allocation_id=a.id)
 UNION ALL SELECT 'held_stock_not_above_physical',count(*) FROM (SELECT o.location_id,x.lot_id,sum(x.quantity-x.shipped) AS held FROM "pl-allocation-lots" x JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id JOIN "pl-orders" o ON o.id=l.order_id GROUP BY o.location_id,x.lot_id) r WHERE held>coalesce((SELECT quantity FROM "pl-stock-balances" WHERE location_id=r.location_id AND lot_id=r.lot_id AND account='physical'),0)
 UNION ALL SELECT 'allocation_shipped_vs_transfers',count(*) FROM "pl-allocation-lots" x WHERE shipped<>(SELECT coalesce(sum(quantity),0) FROM "pl-transfer-allocations" t WHERE t.allocation_id=x.allocation_id AND t.lot_id=x.lot_id)
 UNION ALL SELECT 'transfer_allocation_scope',count(*) FROM "pl-transfer-allocations" x JOIN "pl-transfers" t ON t.id=x.transfer_id JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id JOIN "pl-orders" o ON o.id=l.order_id JOIN "pl-lots" lot ON lot.id=x.lot_id WHERE t.source_id<>o.location_id OR t.target_id<>a.location_id OR lot.product_id<>l.product_id OR x.quantity<>(SELECT quantity FROM "pl-transfer-lines" WHERE transfer_id=x.transfer_id AND lot_id=x.lot_id)
 UNION ALL SELECT 'transfer_allocation_disposition',count(*) FROM "pl-transfer-allocations" x WHERE x.good<>(SELECT coalesce(sum(good),0) FROM "pl-transfer-receipt-lines" WHERE transfer_id=x.transfer_id AND lot_id=x.lot_id) OR x.damaged<>(SELECT coalesce(sum(damaged),0) FROM "pl-transfer-receipt-lines" WHERE transfer_id=x.transfer_id AND lot_id=x.lot_id) OR x.missing<>(SELECT coalesce(sum(missing),0) FROM "pl-transfer-receipt-lines" WHERE transfer_id=x.transfer_id AND lot_id=x.lot_id)
 UNION ALL SELECT 'pipeline_disjoint_conservation',count(*) FROM "pl-procurement-pipeline" p JOIN "pl-orders" o ON o.id=p.order_id WHERE p.allocation_id IS NOT NULL AND o.status<>'DRAFT' AND p.quantity<>p.expected+p.at_warehouse+p.in_transit+p.delivered+p.exceptions+p.cancelled
 UNION ALL SELECT 'draft_not_expected_supply',count(*) FROM "pl-procurement-pipeline" p JOIN "pl-orders" o ON o.id=p.order_id WHERE o.status='DRAFT' AND p.expected<>0;
TABLE "pl-a4-checks";
DO $$BEGIN IF EXISTS(SELECT 1 FROM "pl-a4-checks" WHERE violations<>0) THEN RAISE EXCEPTION 'A4 reconciliation failed'; END IF; END$$;
-- This is A4 reconciliation, not a waiver of the unchanged A3 full-price invariant.
-- Always run tests/a3-reconcile.sql as well; historical3916 remains visible there.
