-- +goose Up
-- Cold input profile read ~32 MB of heap for a few numeric summary columns.
-- Cover the complete demand input in location/product order; keep the PK unchanged.
CREATE INDEX "pl-daily-sales-demand" ON "pl-daily-sales"(location_id,product_id,day) INCLUDE(gross,returned);
-- +goose Down
SELECT 1;
