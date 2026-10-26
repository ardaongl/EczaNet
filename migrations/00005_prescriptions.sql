-- +goose Up
CREATE TABLE "pl-patients" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 external_id text NOT NULL UNIQUE CHECK(length(trim(external_id)) BETWEEN 1 AND 100),
 birth_date date NOT NULL
);
CREATE TABLE "pl-prescriptions" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 external_id text NOT NULL UNIQUE CHECK(length(trim(external_id)) BETWEEN 1 AND 100),
 patient_id bigint NOT NULL REFERENCES "pl-patients",
 issued_at timestamptz NOT NULL,
 report_from timestamptz, report_to timestamptz,
 owner_id bigint REFERENCES "pl-locations",
 digest text NOT NULL,
 CHECK((report_from IS NULL AND report_to IS NULL) OR (report_from IS NOT NULL AND report_to>report_from))
);
CREATE TABLE "pl-prescription-lines" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 prescription_id bigint NOT NULL REFERENCES "pl-prescriptions",
 prescribed_product_id bigint NOT NULL REFERENCES "pl-products",
 prescribed_base bigint NOT NULL CHECK(prescribed_base>0),
 fulfilled_base bigint NOT NULL DEFAULT 0 CHECK(fulfilled_base>=0 AND fulfilled_base<=prescribed_base),
 UNIQUE(prescription_id,prescribed_product_id)
);
CREATE INDEX ON "pl-prescription-lines"(prescription_id,id);
CREATE TABLE "pl-control-policies" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 group_name text NOT NULL, base_unit text NOT NULL,
 version text NOT NULL UNIQUE,
 valid_from timestamptz NOT NULL, valid_to timestamptz NOT NULL,
 daily_limit bigint NOT NULL CHECK(daily_limit>0), CHECK(valid_to>valid_from),
 UNIQUE(group_name,valid_from)
);
INSERT INTO "pl-control-policies"(group_name,base_unit,version,valid_from,valid_to,daily_limit)
 VALUES('CONTROL-TAB','tablet','control-tab-v1','2000-01-01','infinity',10);
CREATE TABLE "pl-daily-controls" (
 patient_id bigint REFERENCES "pl-patients", group_name text,
 business_day date, consumed bigint NOT NULL DEFAULT 0 CHECK(consumed>=0),
 PRIMARY KEY(patient_id,group_name,business_day)
);
ALTER TABLE "pl-sales" ADD COLUMN prescription_id bigint REFERENCES "pl-prescriptions";
ALTER TABLE "pl-sales" ADD COLUMN patient_total bigint;
ALTER TABLE "pl-sales" ADD COLUMN institution_total bigint;
ALTER TABLE "pl-sale-lines" ADD COLUMN snapshot jsonb;
CREATE TABLE "pl-dispensations" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 prescription_line_id bigint NOT NULL REFERENCES "pl-prescription-lines",
 sale_line_id bigint NOT NULL REFERENCES "pl-sale-lines",
 quantity bigint NOT NULL CHECK(quantity>0), UNIQUE(prescription_line_id,sale_line_id)
);
CREATE TABLE "pl-controlled-ledger" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 patient_id bigint NOT NULL REFERENCES "pl-patients",
 prescription_id bigint NOT NULL REFERENCES "pl-prescriptions",
 sale_line_id bigint NOT NULL REFERENCES "pl-sale-lines",
 product_id bigint NOT NULL REFERENCES "pl-products", lot_id bigint NOT NULL REFERENCES "pl-lots",
 quantity bigint NOT NULL CHECK(quantity>0),
 group_name text NOT NULL, policy_id bigint NOT NULL REFERENCES "pl-control-policies",
 location_id bigint NOT NULL REFERENCES "pl-locations", actor_id bigint NOT NULL REFERENCES "pl-wy-users",
 business_day date NOT NULL, effective_at timestamptz NOT NULL,
 UNIQUE(sale_line_id,lot_id)
);
CREATE INDEX ON "pl-controlled-ledger"(location_id,id);
ALTER TABLE "pl-sales" ADD CONSTRAINT "pl-sale-shares" CHECK(patient_total IS NULL OR (patient_total>=0 AND institution_total>=0 AND patient_total+institution_total=total));
CREATE TABLE "pl-pricing-policies" (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 version text NOT NULL UNIQUE, valid_from timestamptz NOT NULL, valid_to timestamptz NOT NULL,
 CHECK(valid_to>valid_from)
);
INSERT INTO "pl-pricing-policies"(version,valid_from,valid_to) VALUES('demo-rx-v1','2000-01-01','infinity');
GRANT SELECT ON "pl-pricing-policies" TO eczanet_runtime;
-- +goose StatementBegin
CREATE FUNCTION "pl-protect-prescription"() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='pl-prescriptions' THEN
  IF (NEW.external_id,NEW.patient_id,NEW.issued_at,NEW.report_from,NEW.report_to,NEW.digest) IS DISTINCT FROM (OLD.external_id,OLD.patient_id,OLD.issued_at,OLD.report_from,OLD.report_to,OLD.digest)
   OR (OLD.owner_id IS NOT NULL AND NEW.owner_id IS DISTINCT FROM OLD.owner_id) THEN
   RAISE EXCEPTION 'prescription immutable' USING ERRCODE='23514'; END IF;
 ELSE
  IF (NEW.prescription_id,NEW.prescribed_product_id,NEW.prescribed_base) IS DISTINCT FROM (OLD.prescription_id,OLD.prescribed_product_id,OLD.prescribed_base)
   OR NEW.fulfilled_base<OLD.fulfilled_base THEN RAISE EXCEPTION 'prescription line immutable' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE FUNCTION "pl-policy-overlap"() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(33907);
 IF TG_TABLE_NAME='pl-pricing-policies' THEN
  IF EXISTS(SELECT 1 FROM "pl-pricing-policies" WHERE id<>NEW.id AND tstzrange(valid_from,valid_to,'[)') && tstzrange(NEW.valid_from,NEW.valid_to,'[)')) THEN RAISE EXCEPTION 'pricing policy overlap' USING ERRCODE='23514'; END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM "pl-control-policies" WHERE group_name=NEW.group_name AND id<>NEW.id AND tstzrange(valid_from,valid_to,'[)') && tstzrange(NEW.valid_from,NEW.valid_to,'[)')) THEN RAISE EXCEPTION 'control policy overlap' USING ERRCODE='23514'; END IF;
 END IF;RETURN NEW;
END $$;
CREATE TRIGGER "pl-pricing-policy-overlap" BEFORE INSERT OR UPDATE ON "pl-pricing-policies" FOR EACH ROW EXECUTE FUNCTION "pl-policy-overlap"();
CREATE TRIGGER "pl-control-policy-overlap" BEFORE INSERT OR UPDATE ON "pl-control-policies" FOR EACH ROW EXECUTE FUNCTION "pl-policy-overlap"();
CREATE TRIGGER "pl-rx-protected" BEFORE UPDATE ON "pl-prescriptions" FOR EACH ROW EXECUTE FUNCTION "pl-protect-prescription"();
CREATE TRIGGER "pl-rx-line-protected" BEFORE UPDATE ON "pl-prescription-lines" FOR EACH ROW EXECUTE FUNCTION "pl-protect-prescription"();
-- +goose StatementEnd
GRANT SELECT,INSERT ON "pl-patients","pl-prescriptions","pl-prescription-lines","pl-daily-controls","pl-dispensations","pl-controlled-ledger" TO eczanet_runtime;
GRANT SELECT ON "pl-control-policies" TO eczanet_runtime;
GRANT UPDATE(owner_id) ON "pl-prescriptions" TO eczanet_runtime;
GRANT UPDATE(fulfilled_base) ON "pl-prescription-lines" TO eczanet_runtime;
GRANT UPDATE(consumed) ON "pl-daily-controls" TO eczanet_runtime;
-- Row-lock privilege; no business field can be changed through these grants.
GRANT UPDATE(id) ON "pl-prescriptions","pl-prescription-lines","pl-products" TO eczanet_runtime;
GRANT UPDATE(valid_to) ON "pl-price-periods" TO eczanet_runtime;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO eczanet_runtime;
-- +goose Down
SELECT 1;
