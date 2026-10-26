-- +goose Up
CREATE TABLE "pl-products" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 barcode text NOT NULL UNIQUE CHECK(barcode ~ '^[0-9]{4,40}$'),
 name text NOT NULL CHECK(length(trim(name)) BETWEEN 1 AND 200),
 dose text NOT NULL CHECK(length(trim(dose)) BETWEEN 1 AND 100),
 form text NOT NULL CHECK(length(trim(form)) BETWEEN 1 AND 100),
 base_unit text NOT NULL CHECK(base_unit IN ('tablet','ml','adet')),
 pack_size bigint NOT NULL CHECK(pack_size BETWEEN 1 AND 1000000000),
 blister_size bigint NOT NULL DEFAULT 0 CHECK(blister_size>=0 AND blister_size<=pack_size),
 sale_multiple bigint NOT NULL DEFAULT 1 CHECK(sale_multiple>0 AND pack_size%sale_multiple=0 AND (blister_size=0 OR blister_size%sale_multiple=0)),
 prescription_required boolean NOT NULL DEFAULT false,
 controlled_group text NOT NULL DEFAULT '', active boolean NOT NULL DEFAULT true
);
CREATE TABLE "pl-ingredients" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name text NOT NULL UNIQUE CHECK(name=lower(trim(name)) AND name<>''));
CREATE TABLE "pl-product-ingredients" (product_id bigint REFERENCES "pl-products", ingredient_id bigint REFERENCES "pl-ingredients", PRIMARY KEY(product_id,ingredient_id));
CREATE TABLE "pl-suppliers" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name text NOT NULL UNIQUE);
INSERT INTO "pl-suppliers"(name) VALUES ('Kurgusal Demo Tedarikci');
CREATE TABLE "pl-lots" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 product_id bigint NOT NULL REFERENCES "pl-products", supplier_id bigint NOT NULL REFERENCES "pl-suppliers",
 number text NOT NULL CHECK(length(trim(number)) BETWEEN 1 AND 100), expires_on date NOT NULL,
 UNIQUE(product_id,supplier_id,number), UNIQUE(id,product_id)
);
CREATE INDEX ON "pl-lots"(product_id,expires_on,id);
CREATE TABLE "pl-inventory-guards" (location_id bigint REFERENCES "pl-locations", product_id bigint REFERENCES "pl-products", PRIMARY KEY(location_id,product_id));
-- account: physical, quarantine, transit:<transfer id>, supply, sold. Only counteraccounts may be negative.
CREATE TABLE "pl-stock-balances" (
 location_id bigint NOT NULL REFERENCES "pl-locations", lot_id bigint NOT NULL REFERENCES "pl-lots", account text NOT NULL,
 quantity bigint NOT NULL DEFAULT 0, PRIMARY KEY(location_id,lot_id,account),
 CHECK(account IN ('physical','quarantine','supply','sold') OR account ~ '^transit:[0-9]+$'),
 CHECK(account IN ('supply','sold') OR quantity>=0)
);
CREATE TABLE "pl-stock-events" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, kind text NOT NULL, reference text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE TABLE "pl-stock-entries" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, event_id bigint NOT NULL REFERENCES "pl-stock-events",
 location_id bigint NOT NULL REFERENCES "pl-locations", lot_id bigint NOT NULL REFERENCES "pl-lots", account text NOT NULL,
 quantity bigint NOT NULL CHECK(quantity<>0),
 FOREIGN KEY(location_id,lot_id,account) REFERENCES "pl-stock-balances"(location_id,lot_id,account)
);
CREATE INDEX ON "pl-stock-entries"(location_id,lot_id,account,id);
-- +goose StatementBegin
CREATE FUNCTION "pl-check-event"() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE eid bigint;
BEGIN
 IF TG_TABLE_NAME='pl-stock-events' THEN eid := NEW.id; ELSE eid := NEW.event_id; END IF;
 IF NOT EXISTS(SELECT 1 FROM "pl-stock-entries" WHERE event_id=eid) OR EXISTS(SELECT lot_id FROM "pl-stock-entries" WHERE event_id=eid GROUP BY lot_id HAVING sum(quantity)<>0) THEN
 RAISE EXCEPTION 'unbalanced or empty stock event' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER "pl-event-balanced" AFTER INSERT ON "pl-stock-events" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "pl-check-event"();
CREATE CONSTRAINT TRIGGER "pl-entry-balanced" AFTER INSERT ON "pl-stock-entries" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "pl-check-event"();
CREATE FUNCTION "pl-check-balance"() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual numeric; expected bigint;
BEGIN
 SELECT quantity INTO expected FROM "pl-stock-balances" WHERE location_id=NEW.location_id AND lot_id=NEW.lot_id AND account=NEW.account;
 SELECT coalesce(sum(quantity),0) INTO actual FROM "pl-stock-entries" WHERE location_id=NEW.location_id AND lot_id=NEW.lot_id AND account=NEW.account;
 IF expected<>actual THEN RAISE EXCEPTION 'balance ledger mismatch' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER "pl-balance-reconciled" AFTER INSERT OR UPDATE ON "pl-stock-balances" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "pl-check-balance"();
CREATE CONSTRAINT TRIGGER "pl-entry-reconciled" AFTER INSERT ON "pl-stock-entries" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "pl-check-balance"();
CREATE FUNCTION "pl-immutable-lot"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'lot identity immutable' USING ERRCODE='23514'; END $$;
CREATE TRIGGER "pl-lot-immutable" BEFORE UPDATE ON "pl-lots" FOR EACH ROW EXECUTE FUNCTION "pl-immutable-lot"();
CREATE FUNCTION "pl-protect-units"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF (NEW.base_unit,NEW.pack_size,NEW.blister_size,NEW.sale_multiple) IS DISTINCT FROM (OLD.base_unit,OLD.pack_size,OLD.blister_size,OLD.sale_multiple) AND EXISTS(SELECT 1 FROM "pl-lots" WHERE product_id=OLD.id) THEN RAISE EXCEPTION 'used conversion immutable' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
CREATE TRIGGER "pl-units-protected" BEFORE UPDATE ON "pl-products" FOR EACH ROW EXECUTE FUNCTION "pl-protect-units"();
-- +goose StatementEnd
CREATE TABLE "pl-audit-events" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, actor_id bigint NOT NULL REFERENCES "pl-wy-users", command text NOT NULL, object_id text NOT NULL, request_id text NOT NULL, old_value jsonb NOT NULL DEFAULT '{}', new_value jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE TABLE "pl-idempotency" (actor_id bigint REFERENCES "pl-wy-users", command text NOT NULL, location_id bigint NOT NULL REFERENCES "pl-locations", key text NOT NULL CHECK(length(key) BETWEEN 8 AND 128), digest text NOT NULL, status integer, result jsonb, PRIMARY KEY(actor_id,command,location_id,key), CHECK((status IS NULL)=(result IS NULL)));
CREATE TABLE "pl-price-periods" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, product_id bigint NOT NULL REFERENCES "pl-products", valid_from timestamptz NOT NULL, valid_to timestamptz NOT NULL DEFAULT 'infinity', pack_price bigint NOT NULL CHECK(pack_price>0), CHECK(valid_to>valid_from));
-- +goose StatementBegin
CREATE FUNCTION "pl-price-overlap"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM 1 FROM "pl-products" WHERE id=NEW.product_id FOR UPDATE;
 IF EXISTS(SELECT 1 FROM "pl-price-periods" WHERE product_id=NEW.product_id AND id<>NEW.id AND tstzrange(valid_from,valid_to,'[)') && tstzrange(NEW.valid_from,NEW.valid_to,'[)')) THEN RAISE EXCEPTION 'overlapping price' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
CREATE TRIGGER "pl-price-no-overlap" BEFORE INSERT OR UPDATE ON "pl-price-periods" FOR EACH ROW EXECUTE FUNCTION "pl-price-overlap"();
-- +goose StatementEnd
CREATE TABLE "pl-goods-receipts" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, supplier_id bigint NOT NULL REFERENCES "pl-suppliers", document_no text NOT NULL CHECK(length(trim(document_no)) BETWEEN 1 AND 100), location_id bigint NOT NULL REFERENCES "pl-locations", reason text NOT NULL CHECK(length(trim(reason)) BETWEEN 1 AND 500), digest text NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), UNIQUE(supplier_id,document_no));
CREATE TABLE "pl-goods-receipt-lines" (receipt_id bigint REFERENCES "pl-goods-receipts", lot_id bigint REFERENCES "pl-lots", quantity bigint NOT NULL CHECK(quantity>0), PRIMARY KEY(receipt_id,lot_id));
CREATE TABLE "pl-transfers" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, source_id bigint NOT NULL REFERENCES "pl-locations", target_id bigint NOT NULL REFERENCES "pl-locations", status text NOT NULL CHECK(status IN ('DRAFT','SENT','CLOSED')), created_at timestamptz NOT NULL DEFAULT clock_timestamp(), CHECK(source_id<>target_id));
CREATE TABLE "pl-transfer-lines" (transfer_id bigint REFERENCES "pl-transfers", lot_id bigint REFERENCES "pl-lots", quantity bigint NOT NULL CHECK(quantity>0), PRIMARY KEY(transfer_id,lot_id));
CREATE TABLE "pl-sales" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, location_id bigint NOT NULL REFERENCES "pl-locations", actor_id bigint NOT NULL REFERENCES "pl-wy-users", total bigint NOT NULL CHECK(total>0), effective_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp());
CREATE INDEX ON "pl-sales"(location_id,id);
CREATE TABLE "pl-sale-lines" (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, sale_id bigint NOT NULL REFERENCES "pl-sales", product_id bigint NOT NULL REFERENCES "pl-products", quantity bigint NOT NULL CHECK(quantity>0), pack_size bigint NOT NULL, blister_size bigint NOT NULL, base_unit text NOT NULL, price_id bigint NOT NULL REFERENCES "pl-price-periods", pack_price bigint NOT NULL, total bigint NOT NULL, policy text NOT NULL DEFAULT 'A2-normal-v1');
CREATE TABLE "pl-sale-allocations" (line_id bigint REFERENCES "pl-sale-lines", lot_id bigint REFERENCES "pl-lots", quantity bigint NOT NULL CHECK(quantity>0), PRIMARY KEY(line_id,lot_id));
GRANT SELECT,INSERT ON "pl-products", "pl-ingredients", "pl-product-ingredients", "pl-lots", "pl-inventory-guards", "pl-stock-balances", "pl-stock-events", "pl-stock-entries", "pl-audit-events", "pl-idempotency", "pl-price-periods", "pl-goods-receipts", "pl-goods-receipt-lines", "pl-transfers", "pl-transfer-lines", "pl-sales", "pl-sale-lines", "pl-sale-allocations" TO eczanet_runtime;
GRANT SELECT ON "pl-suppliers" TO eczanet_runtime;
GRANT UPDATE(active) ON "pl-products" TO eczanet_runtime;
GRANT UPDATE(quantity) ON "pl-stock-balances" TO eczanet_runtime;
GRANT UPDATE(status,result) ON "pl-idempotency" TO eczanet_runtime;
GRANT UPDATE(status) ON "pl-transfers" TO eczanet_runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eczanet_runtime;
-- +goose Down
-- Forward-only business migration: destructive reset is intentionally not an automatic rollback.
SELECT 1;
