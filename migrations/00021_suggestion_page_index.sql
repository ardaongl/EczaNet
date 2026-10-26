-- +goose Up
-- The UNIQUE(run_id,location_id,product_id) index already scopes each result page.
-- Sort the bounded run slice by id for keyset output; avoid a redundant third index on every result insert.
DROP INDEX "pl-suggestions-page";
-- +goose Down
CREATE INDEX "pl-suggestions-page" ON "pl-order-suggestions"(run_id,location_id,id);
