-- +goose Up
ALTER TABLE "pl-products" ADD CONSTRAINT "pl-dose-positive" CHECK (dose ~ '^[0-9]+([.,][0-9]+)? *[^0-9 .,-]' AND dose !~ '^0+([.,]0+)? *[^0-9 .,-]');
-- +goose Down
ALTER TABLE "pl-products" DROP CONSTRAINT "pl-dose-positive";
