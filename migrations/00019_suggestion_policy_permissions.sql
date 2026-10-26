-- +goose Up
GRANT INSERT,UPDATE ON "pl-suggestion-policy" TO eczanet_runtime;
-- +goose Down
SELECT 1;
