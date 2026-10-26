-- +goose Up
CREATE TABLE "pl-needs" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 location_id bigint NOT NULL REFERENCES "pl-locations", product_id bigint NOT NULL REFERENCES "pl-products",
 quantity bigint NOT NULL CHECK(quantity>0), status text NOT NULL CHECK(status IN ('DRAFT','APPROVED')),
 actor_id bigint NOT NULL REFERENCES "pl-wy-users", reason text NOT NULL CHECK(length(trim(reason)) BETWEEN 1 AND 500),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON "pl-needs"(location_id,id);
CREATE TABLE "pl-orders" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, location_id bigint NOT NULL REFERENCES "pl-locations",
 supplier_id bigint NOT NULL REFERENCES "pl-suppliers", actor_id bigint NOT NULL REFERENCES "pl-wy-users",
 status text NOT NULL CHECK(status IN ('DRAFT','APPROVED','PARTIALLY_RECEIVED','RECEIVED','CANCELLED','CLOSED')),
 reason text NOT NULL CHECK(length(trim(reason)) BETWEEN 1 AND 500), created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ON "pl-orders"(location_id,id);
CREATE TABLE "pl-order-lines" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, order_id bigint NOT NULL REFERENCES "pl-orders",
 product_id bigint NOT NULL REFERENCES "pl-products", quantity bigint NOT NULL CHECK(quantity>0),
 received bigint NOT NULL DEFAULT 0 CHECK(received>=0), cancelled bigint NOT NULL DEFAULT 0 CHECK(cancelled>=0),
 CHECK(received::numeric+cancelled<=quantity), UNIQUE(order_id,product_id), UNIQUE(id,order_id)
);
CREATE TABLE "pl-order-allocations" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, line_id bigint NOT NULL REFERENCES "pl-order-lines",
 need_id bigint NOT NULL UNIQUE REFERENCES "pl-needs", location_id bigint NOT NULL REFERENCES "pl-locations",
 quantity bigint NOT NULL CHECK(quantity>0), received bigint NOT NULL DEFAULT 0 CHECK(received>=0),
 cancelled bigint NOT NULL DEFAULT 0 CHECK(cancelled>=0), CHECK(received::numeric+cancelled<=quantity)
);
CREATE INDEX ON "pl-order-allocations"(line_id,id);
CREATE TABLE "pl-allocation-lots" (
 allocation_id bigint NOT NULL REFERENCES "pl-order-allocations", lot_id bigint NOT NULL REFERENCES "pl-lots",
 quantity bigint NOT NULL CHECK(quantity>0), shipped bigint NOT NULL DEFAULT 0 CHECK(shipped>=0),
 CHECK(shipped<=quantity), PRIMARY KEY(allocation_id,lot_id)
);
CREATE TABLE "pl-transfer-order-links" (
 transfer_id bigint PRIMARY KEY REFERENCES "pl-transfers", allocation_id bigint NOT NULL REFERENCES "pl-order-allocations"
);
CREATE TABLE "pl-transfer-allocations" (
 transfer_id bigint NOT NULL, lot_id bigint NOT NULL, allocation_id bigint NOT NULL REFERENCES "pl-order-allocations",
 quantity bigint NOT NULL CHECK(quantity>0), good bigint NOT NULL DEFAULT 0 CHECK(good>=0),
 damaged bigint NOT NULL DEFAULT 0 CHECK(damaged>=0), missing bigint NOT NULL DEFAULT 0 CHECK(missing>=0),
 CHECK(good::numeric+damaged+missing<=quantity),
 PRIMARY KEY(transfer_id,lot_id), FOREIGN KEY(transfer_id,lot_id) REFERENCES "pl-transfer-lines"(transfer_id,lot_id),
 FOREIGN KEY(allocation_id,lot_id) REFERENCES "pl-allocation-lots"(allocation_id,lot_id)
);
CREATE TABLE "pl-order-receipt-lines" (
 receipt_id bigint NOT NULL, lot_id bigint NOT NULL, line_id bigint NOT NULL REFERENCES "pl-order-lines",
 accepted bigint NOT NULL CHECK(accepted>=0), unsuitable bigint NOT NULL CHECK(unsuitable>=0),
 CHECK(accepted::numeric+unsuitable>0), PRIMARY KEY(receipt_id,lot_id),
 FOREIGN KEY(receipt_id,lot_id) REFERENCES "pl-goods-receipt-lines"(receipt_id,lot_id)
);
-- All columns here are disjoint stages, not additional physical stock.
CREATE VIEW "pl-procurement-pipeline" AS
 SELECT a.id AS allocation_id,l.order_id,o.location_id AS warehouse_id,a.location_id AS branch_id,l.product_id,
 CASE WHEN o.status IN ('APPROVED','PARTIALLY_RECEIVED') THEN a.quantity-a.received-a.cancelled ELSE 0 END AS expected,
 coalesce((SELECT sum(x.quantity-x.shipped) FROM "pl-allocation-lots" x WHERE x.allocation_id=a.id),0) AS at_warehouse,
 coalesce((SELECT sum(t.quantity-t.good-t.damaged-t.missing) FROM "pl-transfer-allocations" t WHERE t.allocation_id=a.id),0) AS in_transit,
 coalesce((SELECT sum(t.good) FROM "pl-transfer-allocations" t WHERE t.allocation_id=a.id),0) AS delivered,
 coalesce((SELECT sum(t.damaged+t.missing) FROM "pl-transfer-allocations" t WHERE t.allocation_id=a.id),0) AS exceptions,
 a.cancelled,a.quantity
 FROM "pl-order-allocations" a JOIN "pl-order-lines" l ON l.id=a.line_id JOIN "pl-orders" o ON o.id=l.order_id
 UNION ALL
 SELECT NULL,l.order_id,o.location_id,NULL,l.product_id,
 CASE WHEN o.status IN ('APPROVED','PARTIALLY_RECEIVED') THEN l.quantity-l.received-l.cancelled-coalesce((SELECT sum(a.quantity-a.received-a.cancelled) FROM "pl-order-allocations" a WHERE a.line_id=l.id),0) ELSE 0 END,
 0,0,0,0,0,0 FROM "pl-order-lines" l JOIN "pl-orders" o ON o.id=l.order_id;
GRANT SELECT,INSERT ON "pl-needs","pl-orders","pl-order-lines","pl-order-allocations","pl-allocation-lots","pl-transfer-order-links","pl-transfer-allocations","pl-order-receipt-lines" TO eczanet_runtime;
GRANT SELECT ON "pl-procurement-pipeline" TO eczanet_runtime;
GRANT UPDATE(status) ON "pl-needs","pl-orders" TO eczanet_runtime;
GRANT UPDATE(received,cancelled) ON "pl-order-lines","pl-order-allocations" TO eczanet_runtime;
GRANT UPDATE(quantity,shipped) ON "pl-allocation-lots" TO eczanet_runtime;
GRANT UPDATE(good,damaged,missing) ON "pl-transfer-allocations" TO eczanet_runtime;
-- Row-lock privileges, never mutable snapshot fields.
GRANT UPDATE(id) ON "pl-order-lines","pl-order-allocations" TO eczanet_runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eczanet_runtime;
-- +goose Down
SELECT 1;
