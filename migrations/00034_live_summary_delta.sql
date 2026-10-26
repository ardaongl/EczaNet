-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION "pl-refresh-daily-sales"() RETURNS void LANGUAGE plpgsql AS $$ BEGIN
 PERFORM pg_advisory_xact_lock(33915);
 WITH r AS (SELECT sale_line_id,sum(quantity) qty FROM "pl-return-lines" GROUP BY sale_line_id),
 totals AS (SELECT s.location_id,l.product_id,(s.effective_at AT TIME ZONE 'Europe/Istanbul')::date AS day,
 sum(l.quantity) gross,sum(coalesce(r.qty,0)) returned FROM "pl-sales" s JOIN "pl-sale-lines" l ON l.sale_id=s.id
 LEFT JOIN r ON r.sale_line_id=l.id GROUP BY 1,2,3)
 -- Filter unchanged rows before INSERT, avoiding a row lock for every historical
 -- product/day when only a few live documents changed. The complete recomputation
 -- still includes original-day returns and the same watermark/snapshot.
 INSERT INTO "pl-daily-sales"
 SELECT t.* FROM totals t LEFT JOIN "pl-daily-sales" old USING(location_id,product_id,day)
 WHERE old.location_id IS NULL OR old.gross IS DISTINCT FROM t.gross OR old.returned IS DISTINCT FROM t.returned
 ON CONFLICT(location_id,product_id,day)
 DO UPDATE SET gross=excluded.gross,returned=excluded.returned
 WHERE "pl-daily-sales".gross IS DISTINCT FROM excluded.gross OR "pl-daily-sales".returned IS DISTINCT FROM excluded.returned;
 INSERT INTO "pl-summary-watermark" VALUES(true,"pl-sales-source"(),clock_timestamp())
 ON CONFLICT(id) DO UPDATE SET source=excluded.source,refreshed_at=excluded.refreshed_at;
 END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
