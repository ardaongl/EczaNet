-- +goose Up
CREATE INDEX "pl-entry-event" ON "pl-stock-entries"(event_id,lot_id);
-- +goose Down
SELECT 1;
