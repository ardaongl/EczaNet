-- +goose Up
ALTER TABLE "pl-transfers" DROP CONSTRAINT "pl-transfers_status_check";
ALTER TABLE "pl-transfers" ADD CHECK(status IN ('DRAFT','SENT','PARTIALLY_RECEIVED','CLOSED','CANCELLED'));
CREATE TABLE "pl-transfer-receipts" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 transfer_id bigint NOT NULL REFERENCES "pl-transfers",
 actor_id bigint NOT NULL REFERENCES "pl-wy-users",
 final boolean NOT NULL,
 reason text NOT NULL CHECK(length(reason)<=500),
 effective_at timestamptz NOT NULL,
 UNIQUE(id,transfer_id)
);
CREATE INDEX ON "pl-transfer-receipts"(transfer_id,id);
CREATE TABLE "pl-transfer-receipt-lines" (
 receipt_id bigint NOT NULL, transfer_id bigint NOT NULL, lot_id bigint NOT NULL,
 good bigint NOT NULL CHECK(good>=0), damaged bigint NOT NULL CHECK(damaged>=0), missing bigint NOT NULL CHECK(missing>=0),
 CHECK(good::numeric+damaged+missing>0),
 FOREIGN KEY(receipt_id,transfer_id) REFERENCES "pl-transfer-receipts"(id,transfer_id),
 FOREIGN KEY(transfer_id,lot_id) REFERENCES "pl-transfer-lines"(transfer_id,lot_id),
 PRIMARY KEY(receipt_id,lot_id)
);
ALTER TABLE "pl-stock-balances" DROP CONSTRAINT "pl-stock-balances_account_check";
ALTER TABLE "pl-stock-balances" ADD CHECK(account IN ('physical','quarantine','supply','sold','destroyed','loss') OR account ~ '^transit:[0-9]+$');
GRANT SELECT,INSERT ON "pl-transfer-receipts","pl-transfer-receipt-lines" TO eczanet_runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eczanet_runtime;
-- +goose Down
SELECT 1;
