-- +goose Up
-- Count plus maximum detects late commits with an older allocated identity.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION "pl-sales-source"() RETURNS text LANGUAGE sql STABLE AS $$
 SELECT concat((SELECT count(*)::text||':'||coalesce(max(id),0) FROM "pl-sales"),':',(SELECT count(*)::text||':'||coalesce(max(id),0) FROM "pl-returns")) $$;
CREATE OR REPLACE FUNCTION "pl-suggestion-source"() RETURNS text LANGUAGE sql STABLE AS $$
 SELECT md5(concat("pl-sales-source"(),':',(SELECT count(*)::text||':'||coalesce(max(id),0) FROM "pl-stock-events"),':',
 (SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY id)::text,'')) FROM "pl-orders" t),':',
 (SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY allocation_id)::text,'')) FROM "pl-procurement-eta" t),':',
 (SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY product_id,location_id)::text,'')) FROM "pl-suggestion-policy" t),':',
 (SELECT md5(coalesce(jsonb_agg(to_jsonb(t) ORDER BY product_id)::text,'')) FROM "pl-product-history" t),':',
 (SELECT md5(coalesce(string_agg(id::text||active::text,',' ORDER BY id),'')) FROM "pl-products"))) $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
