-- +goose Up
-- Offline historical stock posting is batched by branch/lot; every individual
-- sale remains traceable to that posting. Runtime cannot create/rewrite links.
CREATE TABLE "pl-seed-history-sales" (
 sale_id bigint PRIMARY KEY REFERENCES "pl-sales",
 event_id bigint NOT NULL REFERENCES "pl-stock-events"
);
CREATE INDEX "pl-seed-history-event" ON "pl-seed-history-sales"(event_id);
GRANT SELECT ON "pl-seed-history-sales" TO eczanet_runtime;
-- +goose Down
SELECT 1;
