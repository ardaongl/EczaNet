INSERT INTO "pl-user-locations" SELECT 7,l FROM (VALUES(1::bigint),(21::bigint)) x(l) ON CONFLICT DO NOTHING;
-- Deterministic canonical data: date and seed drive product selection. Security
-- salts and runtime timestamps are intentionally excluded from logical checksums.
CREATE TEMP TABLE "pl-full-config" AS SELECT current_setting('eczanet.seed_date')::date d,abs(current_setting('eczanet.seed')::bigint % 4995)::integer seed;
INSERT INTO "pl-products"(barcode,name,dose,form,base_unit,pack_size,sale_multiple)
 SELECT '0099'||lpad(i::text,9,'0'),'DEMO-'||lpad(i::text,4,'0'),'5mg','tablet','tablet',10,1 FROM generate_series(1,4995) i ORDER BY i;
INSERT INTO "pl-ingredients"(name) VALUES('demo-normal');
INSERT INTO "pl-product-ingredients" SELECT p.id,i.id FROM "pl-products" p CROSS JOIN "pl-ingredients" i WHERE p.barcode LIKE '0099%' AND i.name='demo-normal';
UPDATE "pl-product-history" SET opened_on=(SELECT d-90 FROM "pl-full-config");
INSERT INTO "pl-price-periods"(product_id,valid_from,pack_price)
 SELECT id,(SELECT d-91 FROM "pl-full-config"),1000 FROM "pl-products" WHERE barcode LIKE '0099%' ORDER BY id;
INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on)
 SELECT p.id,1,'A6-OPEN-'||n,c.d+CASE n WHEN 1 THEN 30 ELSE 365 END FROM "pl-products" p CROSS JOIN "pl-full-config" c CROSS JOIN generate_series(1,2) n WHERE barcode LIKE '0099%' ORDER BY p.id,n;
CREATE TEMP TABLE "pl-full-sales" AS
 SELECT n,((n-1)%20)+1 loc,p.id product_id,pr.id price_id,lot.id lot_id,
 (c.d-90+((n-1)/3000)::integer)+time '12:00' effective
 FROM generate_series(1,270000) n CROSS JOIN "pl-full-config" c
 JOIN "pl-products" p ON p.barcode='0099'||lpad((1+((n-1+c.seed)%4995))::text,9,'0')
 JOIN "pl-price-periods" pr ON pr.product_id=p.id
 JOIN "pl-lots" lot ON lot.product_id=p.id AND lot.number='A6-OPEN-1' ORDER BY n;
ALTER TABLE "pl-full-sales" ADD COLUMN sale_id bigint, ADD COLUMN line_id bigint;
CREATE TEMP TABLE "pl-full-id-base" AS SELECT (SELECT coalesce(max(id),0) FROM "pl-sales") sale_base,(SELECT coalesce(max(id),0) FROM "pl-sale-lines") line_base;
UPDATE "pl-full-sales" SET sale_id=n+(SELECT sale_base FROM "pl-full-id-base"),line_id=n+(SELECT line_base FROM "pl-full-id-base");
INSERT INTO "pl-sales"(id,location_id,actor_id,total,effective_at,created_at,patient_total,institution_total) OVERRIDING SYSTEM VALUE
 SELECT sale_id,loc,1,100,effective,effective,100,0 FROM "pl-full-sales" ORDER BY n;
INSERT INTO "pl-sale-lines"(id,sale_id,product_id,quantity,pack_size,blister_size,base_unit,price_id,pack_price,total,policy,snapshot) OVERRIDING SYSTEM VALUE
 SELECT line_id,sale_id,product_id,1,10,0,'tablet',price_id,1000,100,'A2-normal-v1',
 jsonb_build_object('actor_id','1','effective_at',effective,'dispensed_product_id',product_id::text,'policy','A2-normal-v1','prescription_id','','pack_size',10,'blister_size',0,'base_unit','tablet','pack_price',1000,'price_id',price_id::text,'quantity',1,'total',100,'pricing',jsonb_build_object('patient_total',100,'institution_total',0)) FROM "pl-full-sales" ORDER BY n;
SELECT setval(pg_get_serial_sequence('"pl-sales"','id'),(SELECT max(id) FROM "pl-sales")),setval(pg_get_serial_sequence('"pl-sale-lines"','id'),(SELECT max(id) FROM "pl-sale-lines"));
INSERT INTO "pl-sale-allocations" SELECT line_id,lot_id,1 FROM "pl-full-sales" ORDER BY n;
CREATE TEMP TABLE "pl-full-opening" AS
 SELECT l.id lot_id,x.loc location_id,1000::bigint quantity
 FROM (SELECT DISTINCT loc,product_id FROM "pl-full-sales" UNION SELECT 1,id FROM "pl-products" WHERE barcode LIKE '0099%') x
 JOIN "pl-lots" l ON l.product_id=x.product_id AND l.number LIKE 'A6-OPEN-%' ;
INSERT INTO "pl-inventory-guards" SELECT DISTINCT location_id,l.product_id FROM "pl-full-opening" o JOIN "pl-lots" l ON l.id=o.lot_id;
INSERT INTO "pl-stock-events"(kind,reference,created_at)
 SELECT 'seed.opening',location_id||':'||lot_id,(SELECT d-91 FROM "pl-full-config") FROM "pl-full-opening" ORDER BY location_id,lot_id;
CREATE TEMP TABLE "pl-full-used" AS SELECT loc,lot_id,count(*) qty FROM "pl-full-sales" GROUP BY 1,2;
INSERT INTO "pl-stock-balances"
 SELECT location_id,o.lot_id,a.account,CASE a.account WHEN 'physical' THEN o.quantity-coalesce(u.qty,0) WHEN 'supply' THEN -o.quantity ELSE coalesce(u.qty,0) END
 FROM "pl-full-opening" o LEFT JOIN "pl-full-used" u ON u.loc=o.location_id AND u.lot_id=o.lot_id CROSS JOIN (VALUES('physical'),('supply'),('sold')) a(account);
ANALYZE "pl-full-opening"; ANALYZE "pl-full-sales"; ANALYZE "pl-stock-events";
INSERT INTO "pl-stock-entries"(event_id,location_id,lot_id,account,quantity)
 SELECT e.id,o.location_id,o.lot_id,a.account,CASE a.account WHEN 'physical' THEN o.quantity ELSE -o.quantity END FROM "pl-full-opening" o JOIN "pl-stock-events" e ON e.kind='seed.opening' AND e.reference=o.location_id||':'||o.lot_id CROSS JOIN (VALUES('physical'),('supply')) a(account) ORDER BY e.id,o.location_id,o.lot_id,a.account;
-- One offline history posting per branch/lot, with an explicit FK link for
-- every sale. This reduces repeated trigger work without disabling constraints
-- or losing source-document traceability. Ordinary API sales stay one-event each.
INSERT INTO "pl-stock-events"(kind,reference,created_at)
 SELECT 'seed.history',loc||':'||lot_id,(SELECT d FROM "pl-full-config") FROM "pl-full-used" ORDER BY loc,lot_id;
ANALYZE "pl-stock-events";
INSERT INTO "pl-seed-history-sales"
 SELECT s.sale_id,e.id FROM "pl-full-sales" s JOIN "pl-stock-events" e ON e.kind='seed.history' AND e.reference=s.loc||':'||s.lot_id;
INSERT INTO "pl-stock-entries"(event_id,location_id,lot_id,account,quantity)
 SELECT e.id,u.loc,u.lot_id,a.account,a.sign*u.qty FROM "pl-full-used" u
 JOIN "pl-stock-events" e ON e.kind='seed.history' AND e.reference=u.loc||':'||u.lot_id
 CROSS JOIN (VALUES('physical',-1),('sold',1)) a(account,sign) ORDER BY e.id,u.loc,u.lot_id,a.account;
ANALYZE "pl-stock-entries";
ANALYZE "pl-seed-history-sales";
CREATE TEMP TABLE "pl-full-invariant"(violations bigint CHECK(violations=0));
INSERT INTO "pl-full-invariant"
 SELECT count(*) FROM (
 SELECT m.event_id,s.location_id,a.lot_id,sum(a.quantity) qty FROM "pl-seed-history-sales" m JOIN "pl-sales" s ON s.id=m.sale_id JOIN "pl-sale-lines" l ON l.sale_id=s.id JOIN "pl-sale-allocations" a ON a.line_id=l.id GROUP BY 1,2,3) x
 WHERE x.qty IS DISTINCT FROM -(SELECT sum(quantity) FROM "pl-stock-entries" e WHERE e.event_id=x.event_id AND e.location_id=x.location_id AND e.lot_id=x.lot_id AND account='physical')
 OR x.qty IS DISTINCT FROM (SELECT sum(quantity) FROM "pl-stock-entries" e WHERE e.event_id=x.event_id AND e.location_id=x.location_id AND e.lot_id=x.lot_id AND account='sold');
-- Force deferred ledger validation before publishing the seed marker.
ANALYZE "pl-stock-balances";
ANALYZE "pl-stock-entries";
SET CONSTRAINTS ALL IMMEDIATE;
-- Initial derived summary is seed work, not a hidden recommendation warm-up.
ANALYZE "pl-sales"; ANALYZE "pl-sale-lines"; ANALYZE "pl-products"; ANALYZE "pl-lots"; ANALYZE "pl-product-history";
SET LOCAL jit=off; SET LOCAL work_mem='64MB';
SELECT "pl-refresh-daily-sales"();
ANALYZE "pl-daily-sales";
INSERT INTO "pl-feature-seeds" VALUES('a6-full-v3',(SELECT d FROM "pl-full-config"),current_setting('eczanet.seed'));
