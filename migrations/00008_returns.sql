-- +goose Up
CREATE TABLE "pl-returns" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 sale_id bigint NOT NULL REFERENCES "pl-sales",
 location_id bigint NOT NULL REFERENCES "pl-locations",
 actor_id bigint NOT NULL REFERENCES "pl-wy-users",
 reason text NOT NULL CHECK(length(trim(reason)) BETWEEN 1 AND 500),
 patient_total bigint NOT NULL CHECK(patient_total>=0),
 institution_total bigint NOT NULL CHECK(institution_total>=0),
 effective_at timestamptz NOT NULL,
 UNIQUE(id,sale_id)
);
CREATE INDEX ON "pl-returns"(location_id,id);
ALTER TABLE "pl-sale-lines" ADD CONSTRAINT "pl-sale-line-parent" UNIQUE(id,sale_id);
CREATE TABLE "pl-return-lines" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 return_id bigint NOT NULL, sale_id bigint NOT NULL,
 sale_line_id bigint NOT NULL, lot_id bigint NOT NULL,
 quantity bigint NOT NULL CHECK(quantity>0),
 cumulative_quantity bigint NOT NULL CHECK(cumulative_quantity>=quantity),
 patient_total bigint NOT NULL CHECK(patient_total>=0),
 institution_total bigint NOT NULL CHECK(institution_total>=0),
 FOREIGN KEY(return_id,sale_id) REFERENCES "pl-returns"(id,sale_id),
 FOREIGN KEY(sale_line_id,sale_id) REFERENCES "pl-sale-lines"(id,sale_id),
 FOREIGN KEY(sale_line_id,lot_id) REFERENCES "pl-sale-allocations"(line_id,lot_id),
 UNIQUE(return_id,sale_line_id,lot_id)
);
CREATE INDEX ON "pl-return-lines"(sale_line_id,lot_id);
CREATE TABLE "pl-controlled-returns" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 return_line_id bigint NOT NULL UNIQUE REFERENCES "pl-return-lines",
 ledger_id bigint NOT NULL REFERENCES "pl-controlled-ledger",
 quantity bigint NOT NULL CHECK(quantity>0)
);
CREATE TABLE "pl-quarantine-items" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 event_id bigint NOT NULL UNIQUE REFERENCES "pl-stock-events",
 return_line_id bigint UNIQUE REFERENCES "pl-return-lines",
 location_id bigint NOT NULL REFERENCES "pl-locations",
 lot_id bigint NOT NULL REFERENCES "pl-lots",
 quantity bigint NOT NULL CHECK(quantity>0)
);
CREATE INDEX ON "pl-quarantine-items"(location_id,id);
CREATE TABLE "pl-quarantine-decisions" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 item_id bigint NOT NULL REFERENCES "pl-quarantine-items",
 actor_id bigint NOT NULL REFERENCES "pl-wy-users",
 action text NOT NULL CHECK(action IN ('release','destroy')),
 quantity bigint NOT NULL CHECK(quantity>0),
 sealed boolean NOT NULL,
 reason text NOT NULL CHECK(length(trim(reason)) BETWEEN 1 AND 500),
 effective_at timestamptz NOT NULL,
 CHECK(action<>'release' OR sealed)
);
CREATE INDEX ON "pl-quarantine-decisions"(item_id,id);
-- New counteraccount; existing rows are neither rewritten nor backfilled.
ALTER TABLE "pl-stock-balances" DROP CONSTRAINT "pl-stock-balances_account_check";
ALTER TABLE "pl-stock-balances" ADD CHECK(account IN ('physical','quarantine','supply','sold','destroyed') OR account ~ '^transit:[0-9]+$');
GRANT SELECT,INSERT ON "pl-returns","pl-return-lines","pl-controlled-returns","pl-quarantine-items","pl-quarantine-decisions" TO eczanet_runtime;
-- Locks only. Sales and quarantine sources remain immutable.
GRANT UPDATE(id) ON "pl-sales","pl-quarantine-items" TO eczanet_runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eczanet_runtime;
-- +goose Down
SELECT 1;
