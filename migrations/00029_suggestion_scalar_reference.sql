-- +goose Up
-- Lossless canonical key: positive bigint text never contains ':'. Both sides
-- are GENERATED ALWAYS, so callers cannot forge a mismatched run/product pair.
ALTER TABLE "pl-suggestion-run-products" ADD COLUMN product_scope_key text COLLATE "C"
 GENERATED ALWAYS AS (run_id::text||':'||product_id::text) STORED;
ALTER TABLE "pl-suggestion-run-products" ADD CONSTRAINT "pl-run-product-scalar-key" UNIQUE(product_scope_key);
ALTER TABLE "pl-order-suggestions" ADD COLUMN product_scope_key text COLLATE "C"
 GENERATED ALWAYS AS (run_id::text||':'||product_id::text) STORED;
ALTER TABLE "pl-order-suggestions" ADD CONSTRAINT "pl-suggestion-product-scalar"
 FOREIGN KEY(product_scope_key) REFERENCES "pl-suggestion-run-products"(product_scope_key);
-- The validated scalar FK has precisely the same pair equality and parent chain.
ALTER TABLE "pl-order-suggestions" DROP CONSTRAINT "pl-suggestion-product-scope";
-- +goose Down
SELECT 1;
