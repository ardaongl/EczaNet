-- +goose Up
-- Normalize the immutable run inputs. Every result still has ordinary FK protection
-- for run, product and location, transitively, without checking run three times.
CREATE TABLE "pl-suggestion-run-products" (
 run_id bigint NOT NULL REFERENCES "pl-suggestion-runs",
 product_id bigint NOT NULL REFERENCES "pl-products",
 PRIMARY KEY(run_id,product_id)
);
CREATE TABLE "pl-suggestion-run-locations" (
 run_id bigint NOT NULL REFERENCES "pl-suggestion-runs",
 location_id bigint NOT NULL REFERENCES "pl-locations",
 PRIMARY KEY(run_id,location_id)
);
-- Preserve all retained results and their parent protection during the upgrade.
INSERT INTO "pl-suggestion-run-products" SELECT DISTINCT run_id,product_id FROM "pl-order-suggestions";
INSERT INTO "pl-suggestion-run-locations" SELECT DISTINCT run_id,location_id FROM "pl-order-suggestions";
ALTER TABLE "pl-order-suggestions"
 ADD CONSTRAINT "pl-suggestion-product-scope" FOREIGN KEY(run_id,product_id) REFERENCES "pl-suggestion-run-products",
 ADD CONSTRAINT "pl-suggestion-location-scope" FOREIGN KEY(run_id,location_id) REFERENCES "pl-suggestion-run-locations";
ALTER TABLE "pl-order-suggestions"
 DROP CONSTRAINT "pl-order-suggestions_run_id_fkey",
 DROP CONSTRAINT "pl-order-suggestions_product_id_fkey",
 DROP CONSTRAINT "pl-order-suggestions_location_id_fkey";
GRANT SELECT,INSERT ON "pl-suggestion-run-products","pl-suggestion-run-locations" TO eczanet_runtime;
-- +goose Down
-- Forward-only; retained runs are not destroyed by rollback.
SELECT 1;
