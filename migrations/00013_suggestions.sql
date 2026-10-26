-- +goose Up
-- Existing business rows are neither rewritten nor backfilled.
CREATE TABLE "pl-product-history" (product_id bigint PRIMARY KEY REFERENCES "pl-products", opened_on date NOT NULL);
CREATE TABLE "pl-suggestion-policy" (
 product_id bigint REFERENCES "pl-products", location_id bigint REFERENCES "pl-locations",
 version text NOT NULL CHECK(length(version)>0), lead_days integer NOT NULL DEFAULT 3 CHECK(lead_days>=0),
 review_days integer NOT NULL DEFAULT 2 CHECK(review_days>=0), safety_days integer NOT NULL DEFAULT 2 CHECK(safety_days>=0),
 season numeric(8,4) NOT NULL DEFAULT 1 CHECK(season>0 AND season<=100), minimum bigint NOT NULL DEFAULT 0 CHECK(minimum>=0),
 CHECK(lead_days+review_days+safety_days BETWEEN 1 AND 90), PRIMARY KEY(product_id,location_id)
);
CREATE TABLE "pl-procurement-eta" (allocation_id bigint PRIMARY KEY REFERENCES "pl-order-allocations", expected_on date NOT NULL);
CREATE TABLE "pl-daily-sales" (
 location_id bigint REFERENCES "pl-locations",product_id bigint REFERENCES "pl-products",day date NOT NULL,
 gross numeric NOT NULL CHECK(gross>=0), returned numeric NOT NULL CHECK(returned BETWEEN 0 AND gross),
 PRIMARY KEY(location_id,product_id,day)
);
CREATE TABLE "pl-summary-watermark" (id boolean PRIMARY KEY DEFAULT true CHECK(id), source text NOT NULL, refreshed_at timestamptz NOT NULL);
CREATE TABLE "pl-suggestion-runs" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, as_of date NOT NULL, version text NOT NULL DEFAULT 'onr-v1',
 source text NOT NULL, summary_source text NOT NULL, summary_ms numeric NOT NULL, calculation_ms numeric,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), published boolean NOT NULL DEFAULT false, row_count bigint
);
CREATE TABLE "pl-order-suggestions" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,run_id bigint NOT NULL REFERENCES "pl-suggestion-runs",
 location_id bigint NOT NULL REFERENCES "pl-locations", product_id bigint NOT NULL REFERENCES "pl-products",
 observed_days integer NOT NULL, gross numeric NOT NULL, returned numeric NOT NULL, rate numeric NOT NULL,
 horizon integer NOT NULL,season numeric NOT NULL,minimum bigint NOT NULL, target numeric NOT NULL,
 physical numeric NOT NULL,risk numeric NOT NULL,effective numeric NOT NULL,pending numeric NOT NULL,
 excluded_pending numeric NOT NULL, unknown_lot boolean NOT NULL, suggested numeric NOT NULL, boxes numeric NOT NULL,
 return_quantity numeric NOT NULL, defaults boolean NOT NULL, history_unknown boolean NOT NULL,policy_version text NOT NULL,
 UNIQUE(run_id,location_id,product_id)
);
CREATE INDEX "pl-suggestions-page" ON "pl-order-suggestions"(run_id,location_id,id);
CREATE TABLE "pl-suggestion-consumptions" (
 suggestion_id bigint NOT NULL REFERENCES "pl-order-suggestions", kind text NOT NULL CHECK(kind IN ('need','return')),
 document_id bigint NOT NULL, PRIMARY KEY(suggestion_id,kind)
);
-- +goose StatementBegin
CREATE FUNCTION "pl-product-opened"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 INSERT INTO "pl-product-history" VALUES(NEW.id,(clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date); RETURN NEW; END $$;
CREATE TRIGGER "pl-product-opened" AFTER INSERT ON "pl-products" FOR EACH ROW EXECUTE FUNCTION "pl-product-opened"();
CREATE FUNCTION "pl-sales-source"() RETURNS text LANGUAGE sql STABLE AS $$
 SELECT concat((SELECT count(*)::text||':'||coalesce(max(id),0) FROM "pl-sales"),':',(SELECT count(*)::text||':'||coalesce(max(id),0) FROM "pl-returns")) $$;
CREATE FUNCTION "pl-suggestion-source"() RETURNS text LANGUAGE sql STABLE AS $$
 SELECT md5(concat("pl-sales-source"(),':',(SELECT count(*)::text||':'||coalesce(max(id),0) FROM "pl-stock-events"),':',
 (SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY id)::text,'')) FROM "pl-orders" t),':',
 (SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY allocation_id)::text,'')) FROM "pl-procurement-eta" t),':',
 (SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY product_id,location_id)::text,'')) FROM "pl-suggestion-policy" t),':',
 (SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY product_id)::text,'')) FROM "pl-product-history" t),':',
 (SELECT md5(coalesce(string_agg(id::text||active::text,',' ORDER BY id),'')) FROM "pl-products"))) $$;
-- Full idempotent rebuild includes older days affected by today's returns. Missing days are implicit zero.
CREATE FUNCTION "pl-refresh-daily-sales"() RETURNS void LANGUAGE plpgsql AS $$ BEGIN
 PERFORM pg_advisory_xact_lock(33915);
 WITH r AS (SELECT sale_line_id,sum(quantity) qty FROM "pl-return-lines" GROUP BY sale_line_id),
 totals AS (SELECT s.location_id,l.product_id,(s.effective_at AT TIME ZONE 'Europe/Istanbul')::date AS day,
 sum(l.quantity) gross,sum(coalesce(r.qty,0)) returned FROM "pl-sales" s JOIN "pl-sale-lines" l ON l.sale_id=s.id
 LEFT JOIN r ON r.sale_line_id=l.id GROUP BY 1,2,3)
 INSERT INTO "pl-daily-sales" SELECT * FROM totals ON CONFLICT(location_id,product_id,day)
 DO UPDATE SET gross=excluded.gross,returned=excluded.returned;
 INSERT INTO "pl-summary-watermark" VALUES(true,"pl-sales-source"(),clock_timestamp())
 ON CONFLICT(id) DO UPDATE SET source=excluded.source,refreshed_at=excluded.refreshed_at;
 END $$;
-- Pure set calculation; the caller supplies D and owns a consistent snapshot and atomic publication.
CREATE FUNCTION "pl-calculate-suggestions"(d date) RETURNS TABLE(
 location_id bigint,product_id bigint,observed_days integer,gross numeric,returned numeric,rate numeric,
 horizon integer,season numeric,minimum bigint,target numeric,physical numeric,risk numeric,effective numeric,
 pending numeric,excluded_pending numeric,unknown_lot boolean,suggested numeric,boxes numeric,return_quantity numeric,
 defaults boolean,history_unknown boolean,policy_version text) LANGUAGE sql STABLE AS $$
 WITH RECURSIVE
 sales AS (SELECT location_id,product_id,sum(gross) gross,sum(returned) returned FROM "pl-daily-sales"
 WHERE day>=d-90 AND day<d GROUP BY 1,2),
 skeleton AS (SELECT l.id location_id,p.id product_id,p.pack_size,
 greatest(0,least(90,d-coalesce(h.opened_on,d-90))) days,
 coalesce(s.gross,0) gross,coalesce(s.returned,0) returned,
 coalesce(c.lead_days+c.review_days+c.safety_days,7) horizon,coalesce(c.season,1) season,
 coalesce(c.minimum,0) minimum,c.version IS NULL defaults,h.product_id IS NULL history_unknown,coalesce(c.version,'default-v1') policy_version
 FROM "pl-products" p CROSS JOIN "pl-locations" l LEFT JOIN "pl-product-history" h ON h.product_id=p.id
 LEFT JOIN "pl-suggestion-policy" c ON c.product_id=p.id AND c.location_id=l.id
 LEFT JOIN sales s ON s.product_id=p.id AND s.location_id=l.id WHERE p.active AND l.kind='branch'),
 demand AS (SELECT *,CASE WHEN days=0 THEN 0 ELSE (gross-returned)/days*season END rate,
 CASE WHEN days=0 THEN 0 ELSE (gross-returned)*season*10000 END rate_n,greatest(days,1)*10000 rate_d FROM skeleton),
 lots AS (SELECT b.location_id,l.product_id,l.id,l.expires_on,b.quantity,
 row_number() OVER(PARTITION BY b.location_id,l.product_id ORDER BY l.expires_on,l.id) n
 FROM "pl-stock-balances" b JOIN "pl-lots" l ON l.id=b.lot_id
 WHERE b.account='physical' AND b.quantity>0 AND l.expires_on>=d),
 consumption AS (
 SELECT a.location_id,a.product_id,a.id,a.n,a.expires_on,a.quantity,
 least(a.quantity*x.rate_d,greatest(0,x.rate_n*least(a.expires_on-d+1,x.horizon))) used
 FROM lots a JOIN demand x USING(location_id,product_id) WHERE a.n=1
 UNION ALL
 SELECT a.location_id,a.product_id,a.id,a.n,a.expires_on,a.quantity,
 c.used+least(a.quantity*x.rate_d,greatest(0,x.rate_n*least(a.expires_on-d+1,x.horizon)-c.used))
 FROM consumption c JOIN lots a ON a.location_id=c.location_id AND a.product_id=c.product_id AND a.n=c.n+1
 JOIN demand x ON x.location_id=a.location_id AND x.product_id=a.product_id),
 lot_risks AS (SELECT c.*,c.used-coalesce(lag(c.used) OVER(PARTITION BY c.location_id,c.product_id ORDER BY n),0) allocated FROM consumption c),
 stock AS (SELECT c.location_id,c.product_id,sum(c.quantity) physical,
 sum(CASE WHEN c.expires_on-d+1<=x.horizon THEN least(c.quantity,div(greatest(0,c.quantity*x.rate_d-c.allocated)+x.rate_d-1,x.rate_d)) ELSE 0 END) risk,
 sum(CASE WHEN c.expires_on<=d+30 AND x.gross=0 THEN c.quantity ELSE 0 END) return_quantity
 FROM lot_risks c JOIN demand x USING(location_id,product_id) GROUP BY 1,2),
 -- Preserve the A4 disjoint pipeline. Known lots are checked separately, never added to physical branch stock.
 stages AS (SELECT p.branch_id location_id,p.product_id,p.expected quantity,e.expected_on,true unknown
 FROM "pl-procurement-pipeline" p LEFT JOIN "pl-procurement-eta" e USING(allocation_id) WHERE p.branch_id IS NOT NULL
 UNION ALL
 SELECT a.location_id,l.product_id,x.quantity-x.shipped,e.expected_on,false FROM "pl-allocation-lots" x
 JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id
 JOIN "pl-lots" lot ON lot.id=x.lot_id LEFT JOIN "pl-procurement-eta" e ON e.allocation_id=a.id
 WHERE lot.expires_on>=greatest(d,e.expected_on)
 UNION ALL
 SELECT a.location_id,l.product_id,t.quantity-t.good-t.damaged-t.missing,e.expected_on,false FROM "pl-transfer-allocations" t
 JOIN "pl-order-allocations" a ON a.id=t.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id
 JOIN "pl-lots" lot ON lot.id=t.lot_id LEFT JOIN "pl-procurement-eta" e ON e.allocation_id=a.id
 WHERE lot.expires_on>=greatest(d,e.expected_on)),
 pipeline AS (SELECT x.location_id,x.product_id,
 sum(CASE WHEN expected_on BETWEEN d AND d+x.horizon-1 THEN t.quantity ELSE 0 END) pending,
 sum(CASE WHEN expected_on BETWEEN d AND d+x.horizon-1 THEN 0 ELSE t.quantity END) excluded,
 bool_or(t.unknown AND t.quantity>0 AND expected_on BETWEEN d AND d+x.horizon-1) unknown
 FROM stages t JOIN demand x USING(location_id,product_id) GROUP BY 1,2),
 result AS (SELECT x.*,greatest(x.minimum,div(x.rate_n*x.horizon+x.rate_d-1,x.rate_d)) target,coalesce(s.physical,0) physical,
 coalesce(s.risk,0) risk,coalesce(s.physical-s.risk,0) effective,coalesce(t.pending,0) pending,
 coalesce(t.excluded,0) excluded,coalesce(t.unknown,false) unknown,coalesce(s.return_quantity,0) return_quantity
 FROM demand x LEFT JOIN stock s USING(location_id,product_id) LEFT JOIN pipeline t USING(location_id,product_id)),
 rounded AS (SELECT *,ceil(greatest(0,target-effective-pending)/pack_size) boxes FROM result)
 SELECT location_id,product_id,days,gross,returned,rate,horizon,season,minimum,target,physical,risk,effective,
 pending,excluded,unknown,boxes*pack_size,boxes,return_quantity,defaults,history_unknown,policy_version FROM rounded $$;
-- Runtime may append runs/results, refresh derived summaries, and lock immutable suggestion rows.
GRANT SELECT,INSERT ON "pl-product-history","pl-procurement-eta","pl-suggestion-runs","pl-order-suggestions","pl-suggestion-consumptions","pl-daily-sales","pl-summary-watermark" TO eczanet_runtime;
GRANT UPDATE(expected_on) ON "pl-procurement-eta" TO eczanet_runtime;
GRANT SELECT ON "pl-suggestion-policy" TO eczanet_runtime;
GRANT UPDATE(gross,returned) ON "pl-daily-sales" TO eczanet_runtime;
GRANT UPDATE(source,refreshed_at) ON "pl-summary-watermark" TO eczanet_runtime;
GRANT UPDATE(published,row_count,calculation_ms) ON "pl-suggestion-runs" TO eczanet_runtime;
GRANT UPDATE(id) ON "pl-order-suggestions" TO eczanet_runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eczanet_runtime;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
