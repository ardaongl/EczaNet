-- +goose Up
CREATE TABLE "pl-feature-seeds" (version text PRIMARY KEY, demo_date date NOT NULL, demo_seed text NOT NULL);
-- Current demo has exactly one controlled measurement and exact 1/1 tablet consumption.
ALTER TABLE "pl-products" ADD CONSTRAINT "pl-controlled-unit" CHECK(controlled_group='' OR (controlled_group='CONTROL-TAB' AND base_unit='tablet'));
-- Existing A2 snapshots remain NULL; new snapshots are write-once through INSERT.
-- Closing a live price interval is the only runtime UPDATE permitted on pricing data.
-- +goose StatementBegin
CREATE FUNCTION "pl-price-history-protected"() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_user='eczanet_runtime' AND (NEW.valid_to<=clock_timestamp() OR NEW.valid_to>OLD.valid_to) THEN
  RAISE EXCEPTION 'cannot rewrite historical price interval' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER "pl-price-history-protected" BEFORE UPDATE ON "pl-price-periods" FOR EACH ROW EXECUTE FUNCTION "pl-price-history-protected"();
-- +goose StatementEnd
-- +goose Down
SELECT 1;
