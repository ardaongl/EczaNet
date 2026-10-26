-- +goose Up
-- Location's small, shared parent is faster than a per-run composite lookup.
-- Run/product remain protected transitively by the immutable product scope.
ALTER TABLE "pl-order-suggestions" ADD CONSTRAINT "pl-order-suggestions_location_id_fkey"
 FOREIGN KEY(location_id) REFERENCES "pl-locations";
ALTER TABLE "pl-order-suggestions" DROP CONSTRAINT "pl-suggestion-location-scope";
-- +goose Down
SELECT 1;
