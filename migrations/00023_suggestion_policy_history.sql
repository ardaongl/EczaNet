-- +goose Up
CREATE TABLE "pl-suggestion-policy-history" (
 product_id bigint NOT NULL REFERENCES "pl-products",location_id bigint NOT NULL REFERENCES "pl-locations",
 version text NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(product_id,location_id,version)
);
-- Preserve the initial version of the A5 configurations already created in this session.
INSERT INTO "pl-suggestion-policy-history"(product_id,location_id,version,payload)
SELECT product_id,location_id,version,to_jsonb(p) FROM "pl-suggestion-policy" p;
GRANT SELECT,INSERT ON "pl-suggestion-policy-history" TO eczanet_runtime;
-- +goose Down
SELECT 1;
