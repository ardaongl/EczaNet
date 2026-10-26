-- +goose Up
CREATE TABLE "pl-counts" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 location_id bigint NOT NULL REFERENCES "pl-locations", product_id bigint NOT NULL REFERENCES "pl-products",
 actor_id bigint NOT NULL REFERENCES "pl-wy-users",
 status text NOT NULL CHECK(status IN ('OPEN','COMPLETED','CANCELLED','EXPIRED')),
 expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX "pl-one-open-count" ON "pl-counts"(location_id,product_id) WHERE status='OPEN';
CREATE INDEX ON "pl-counts"(location_id,id);
CREATE TABLE "pl-count-lines" (
 count_id bigint NOT NULL REFERENCES "pl-counts", lot_id bigint NOT NULL REFERENCES "pl-lots",
 account text NOT NULL CHECK(account IN ('physical','quarantine')),
 expected bigint NOT NULL CHECK(expected>=0), PRIMARY KEY(count_id,lot_id,account)
);
CREATE TABLE "pl-count-results" (
 count_id bigint NOT NULL REFERENCES "pl-counts", lot_id bigint NOT NULL REFERENCES "pl-lots",
 account text NOT NULL CHECK(account IN ('physical','quarantine')),
 expected bigint NOT NULL CHECK(expected>=0), actual bigint NOT NULL CHECK(actual>=0),
 PRIMARY KEY(count_id,lot_id,account)
);
CREATE TABLE "pl-count-events" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, count_id bigint NOT NULL REFERENCES "pl-counts",
 status text NOT NULL, expires_at timestamptz NOT NULL,
 actor_id bigint REFERENCES "pl-wy-users", created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE "pl-quarantine-count-losses" (count_id bigint NOT NULL REFERENCES "pl-counts", item_id bigint NOT NULL REFERENCES "pl-quarantine-items", quantity bigint NOT NULL CHECK(quantity>0), PRIMARY KEY(count_id,item_id));
GRANT SELECT,INSERT ON "pl-quarantine-count-losses" TO eczanet_runtime;
-- +goose StatementBegin
CREATE FUNCTION "pl-count-history"() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO "pl-count-events"(count_id,status,expires_at,actor_id) VALUES(NEW.id,NEW.status,NEW.expires_at,nullif(current_setting('eczanet.actor',true),'')::bigint);
 RETURN NEW;
END $$;
CREATE TRIGGER "pl-count-history" AFTER INSERT OR UPDATE OF status,expires_at ON "pl-counts" FOR EACH ROW EXECUTE FUNCTION "pl-count-history"();
-- +goose StatementEnd
ALTER TABLE "pl-stock-balances" DROP CONSTRAINT "pl-stock-balances_account_check";
ALTER TABLE "pl-stock-balances" ADD CHECK(account IN ('physical','quarantine','supply','sold','destroyed','loss','count') OR account ~ '^transit:[0-9]+$');
ALTER TABLE "pl-stock-balances" DROP CONSTRAINT "pl-stock-balances_check";
ALTER TABLE "pl-stock-balances" ADD CHECK(account IN ('supply','sold','count') OR quantity>=0);
GRANT SELECT,INSERT ON "pl-counts","pl-count-lines","pl-count-results","pl-count-events" TO eczanet_runtime;
GRANT UPDATE(status,expires_at) ON "pl-counts" TO eczanet_runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eczanet_runtime;
-- +goose Down
SELECT 1;
