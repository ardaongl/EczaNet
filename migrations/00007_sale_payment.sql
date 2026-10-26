-- +goose Up
ALTER TABLE "pl-sales" ADD COLUMN payment_method text NOT NULL DEFAULT 'cash' CHECK(payment_method IN ('cash','card'));
-- +goose Down
SELECT 1;
