-- +goose Up
-- Preserve exactly the same natural-key uniqueness, ordered as the bulk product/location stream.
CREATE UNIQUE INDEX "pl-suggestion-natural-v2" ON "pl-order-suggestions"(run_id,product_id,location_id);
-- +goose StatementBegin
DO $$ DECLARE previous_name text; BEGIN
 SELECT conname INTO STRICT previous_name FROM pg_constraint
 WHERE conrelid='"pl-order-suggestions"'::regclass AND contype='u';
 ALTER TABLE "pl-order-suggestions" ADD CONSTRAINT "pl-suggestion-natural-v2" UNIQUE USING INDEX "pl-suggestion-natural-v2";
 EXECUTE format('ALTER TABLE "pl-order-suggestions" DROP CONSTRAINT %I',previous_name);
END $$;
-- +goose StatementEnd
-- +goose Down
SELECT 1;
