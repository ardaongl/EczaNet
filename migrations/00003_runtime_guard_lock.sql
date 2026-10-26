-- +goose Up
-- PostgreSQL SELECT FOR UPDATE requires UPDATE on at least one column.
-- Guards have no business payload; expose no API for changing their identity.
GRANT UPDATE(product_id) ON "pl-inventory-guards" TO eczanet_runtime;
-- +goose Down
REVOKE UPDATE(product_id) ON "pl-inventory-guards" FROM eczanet_runtime;
