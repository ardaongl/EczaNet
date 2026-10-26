-- +goose Up
CREATE INDEX ON "pl-allocation-lots"(lot_id,allocation_id);
CREATE INDEX ON "pl-transfer-allocations"(allocation_id,lot_id);
CREATE INDEX ON "pl-order-receipt-lines"(line_id,receipt_id);
-- +goose StatementBegin
CREATE FUNCTION "pl-check-procurement"() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE lid bigint;
BEGIN
 IF TG_TABLE_NAME='pl-order-lines' THEN lid:=NEW.id;
 ELSIF TG_TABLE_NAME='pl-order-allocations' THEN lid:=NEW.line_id;
 ELSE SELECT line_id INTO lid FROM "pl-order-allocations" WHERE id=NEW.allocation_id;
 END IF;
 IF EXISTS(SELECT 1 FROM "pl-order-lines" l WHERE l.id=lid AND
   (l.quantity<(SELECT coalesce(sum(quantity),0) FROM "pl-order-allocations" WHERE line_id=lid)
   OR l.received<(SELECT coalesce(sum(received),0) FROM "pl-order-allocations" WHERE line_id=lid)
   OR l.cancelled<(SELECT coalesce(sum(cancelled),0) FROM "pl-order-allocations" WHERE line_id=lid)))
 OR EXISTS(SELECT 1 FROM "pl-order-allocations" a JOIN "pl-needs" n ON n.id=a.need_id JOIN "pl-order-lines" l ON l.id=a.line_id WHERE a.line_id=lid AND
   (a.quantity<>n.quantity OR a.location_id<>n.location_id OR l.product_id<>n.product_id OR n.status<>'APPROVED'
    OR a.received<>(SELECT coalesce(sum(quantity),0) FROM "pl-allocation-lots" WHERE allocation_id=a.id)))
 OR EXISTS(SELECT 1 FROM "pl-allocation-lots" x JOIN "pl-order-allocations" a ON a.id=x.allocation_id WHERE a.line_id=lid AND
   x.shipped<>(SELECT coalesce(sum(quantity),0) FROM "pl-transfer-allocations" t WHERE t.allocation_id=x.allocation_id AND t.lot_id=x.lot_id))
 THEN RAISE EXCEPTION 'procurement conservation' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER "pl-order-conservation" AFTER INSERT OR UPDATE ON "pl-order-lines" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "pl-check-procurement"();
CREATE CONSTRAINT TRIGGER "pl-allocation-conservation" AFTER INSERT OR UPDATE ON "pl-order-allocations" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "pl-check-procurement"();
CREATE CONSTRAINT TRIGGER "pl-allocation-lot-conservation" AFTER INSERT OR UPDATE ON "pl-allocation-lots" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "pl-check-procurement"();
CREATE CONSTRAINT TRIGGER "pl-shipment-conservation" AFTER INSERT OR UPDATE ON "pl-transfer-allocations" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION "pl-check-procurement"();
-- +goose StatementEnd
-- +goose Down
SELECT 1;
