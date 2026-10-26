-- A2 full database reconciliation. Run as migrator/owner, never print connection secrets.
SELECT count(*) AS invalid_table_names FROM pg_tables WHERE schemaname='public' AND tablename NOT LIKE 'pl-%';
SELECT count(*) AS empty_events FROM "pl-stock-events" e WHERE NOT EXISTS(SELECT 1 FROM "pl-stock-entries" x WHERE x.event_id=e.id);
SELECT count(*) AS unbalanced_lot_events FROM (SELECT event_id,lot_id FROM "pl-stock-entries" GROUP BY event_id,lot_id HAVING sum(quantity)<>0) x;
SELECT count(*) AS balance_mismatches FROM "pl-stock-balances" b WHERE b.quantity<>(SELECT coalesce(sum(e.quantity),0) FROM "pl-stock-entries" e WHERE (e.location_id,e.lot_id,e.account)=(b.location_id,b.lot_id,b.account));
SELECT count(*) AS negative_physical FROM "pl-stock-balances" WHERE account NOT IN ('supply','sold','count') AND quantity<0;
SELECT count(*) AS incomplete_idempotency FROM "pl-idempotency" WHERE status IS NULL OR result IS NULL;
SELECT count(*) AS immutable_lot_trigger_enabled FROM pg_trigger WHERE tgname='pl-lot-immutable' AND tgenabled='O';
SELECT table_name,privilege_type FROM information_schema.role_table_grants WHERE grantee='eczanet_runtime' AND table_name IN ('pl-stock-events','pl-stock-entries','pl-audit-events') ORDER BY 1,2;
SELECT p.id::text,p.barcode,p.name,l.number,b.location_id::text,b.account,b.quantity::text FROM "pl-stock-balances" b JOIN "pl-lots" l ON l.id=b.lot_id JOIN "pl-products" p ON p.id=l.product_id WHERE p.name LIKE 'A2-BASIC-%' ORDER BY p.id,b.location_id,b.account;
SELECT version_id FROM "pl-goose-db-version" WHERE is_applied ORDER BY id;
DO $$ BEGIN
 IF EXISTS(SELECT event_id,lot_id FROM "pl-stock-entries" GROUP BY event_id,lot_id HAVING sum(quantity)<>0) OR EXISTS(SELECT 1 FROM "pl-stock-events" e WHERE NOT EXISTS(SELECT 1 FROM "pl-stock-entries" x WHERE x.event_id=e.id)) OR EXISTS(SELECT 1 FROM "pl-stock-balances" b WHERE b.quantity<>(SELECT coalesce(sum(e.quantity),0) FROM "pl-stock-entries" e WHERE (e.location_id,e.lot_id,e.account)=(b.location_id,b.lot_id,b.account))) THEN RAISE EXCEPTION 'A2 reconciliation failed'; END IF;
END $$;
