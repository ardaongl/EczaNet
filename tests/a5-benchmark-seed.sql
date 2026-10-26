-- Isolated benchmark only. 90 completed days, 3000 sales/day, 5000 products and all 20 branches.
BEGIN;
INSERT INTO "pl-locations"(id,name,kind) SELECT i,'Benchmark branch '||i,'branch' FROM generate_series(1,20) i;
INSERT INTO "pl-locations"(id,name,kind) VALUES(21,'Benchmark warehouse','warehouse');
INSERT INTO "pl-wy-users"(id,email,password_hash,role) VALUES(1,'benchmark@invalid','$2-benchmark-only','pharmacist');
INSERT INTO "pl-products"(barcode,name,dose,form,base_unit,pack_size,sale_multiple)
SELECT lpad(i::text,13,'0'),'Benchmark product '||i,'5mg','tablet','tablet',10,1 FROM generate_series(1,5000) i;
UPDATE "pl-product-history" SET opened_on='2026-07-27';
INSERT INTO "pl-price-periods"(product_id,valid_from,pack_price) SELECT id,'2026-01-01',1000 FROM "pl-products";
INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on)
SELECT p.id,1,'BENCH-'||n,CASE n WHEN 1 THEN '2026-10-26'::date WHEN 2 THEN '2026-10-31'::date ELSE '2027-12-31'::date END FROM "pl-products" p CROSS JOIN generate_series(1,3) n;
INSERT INTO "pl-sales"(location_id,actor_id,total,effective_at,patient_total,institution_total)
SELECT ((n-1)%20)+1,1,100,('2026-07-27'::date+((n-1)/3000)::integer)+time '12:00',100,0 FROM generate_series(1,270000) n;
INSERT INTO "pl-sale-lines"(sale_id,product_id,quantity,pack_size,blister_size,base_unit,price_id,pack_price,total,snapshot)
SELECT s.id,((s.id-1)%5000)+1,1,10,0,'tablet',((s.id-1)%5000)+1,1000,100,'{}'::jsonb FROM "pl-sales" s;
INSERT INTO "pl-sale-allocations" SELECT l.id,lot.id,1 FROM "pl-sale-lines" l JOIN "pl-lots" lot ON lot.product_id=l.product_id AND lot.number='BENCH-3';
-- Deferred integrity checks see the complete balanced ledger at COMMIT.
INSERT INTO "pl-stock-events"(kind,reference) SELECT 'BENCHMARK_OPENING',b.id||':'||l.id FROM "pl-locations" b CROSS JOIN "pl-lots" l WHERE b.kind='branch' ORDER BY b.id,l.id;
INSERT INTO "pl-stock-balances"(location_id,lot_id,account,quantity)
SELECT b.id,l.id,a.account,CASE WHEN a.account='physical' THEN 10 ELSE -10 END FROM "pl-locations" b CROSS JOIN "pl-lots" l CROSS JOIN (VALUES('physical'),('supply')) a(account) WHERE b.kind='branch';
INSERT INTO "pl-stock-entries"(event_id,location_id,lot_id,account,quantity) SELECT e.id,b.location_id,b.lot_id,b.account,b.quantity FROM "pl-stock-balances" b JOIN "pl-stock-events" e ON e.reference=b.location_id||':'||b.lot_id;
COMMIT;
