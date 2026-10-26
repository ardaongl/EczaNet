-- +goose Up
-- Deferred event validation and full-history reconciliation must stay indexed.
CREATE INDEX "pl-entry-event-lot" ON "pl-stock-entries"(event_id,lot_id) INCLUDE(quantity);
CREATE INDEX "pl-sale-lines-sale" ON "pl-sale-lines"(sale_id);
-- Simulator has only branch1 sales/prescriptions and warehouse receipts.
INSERT INTO "pl-user-locations"(user_id,location_id)
 SELECT id,l FROM "pl-wy-users" CROSS JOIN (VALUES(1::bigint),(21::bigint)) x(l)
 WHERE email='simulator@ddo-case.invalid' AND role='simulator'
 ON CONFLICT DO NOTHING;
-- +goose Down
SELECT 1;
