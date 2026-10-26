-- +goose Up
-- Run-product snapshots are immutable. Their ordinary run/product FKs protect
-- both dimensions. Results validate membership as a set, once per statement.
-- Immutability makes the membership proof stable across MVCC snapshots/races;
-- no parent can be removed while a concurrent result is being inserted.
-- +goose StatementBegin
CREATE FUNCTION "pl-immutable-suggestion-products"() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'Suggestion product snapshots are immutable'
 USING ERRCODE='23503',CONSTRAINT='pl-suggestion-products-immutable';
END $$;
CREATE TRIGGER "pl-suggestion-products-immutable"
 BEFORE UPDATE OR DELETE OR TRUNCATE ON "pl-suggestion-run-products"
 FOR EACH STATEMENT EXECUTE FUNCTION "pl-immutable-suggestion-products"();
CREATE FUNCTION "pl-check-suggestion-membership"() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM new_suggestions n
 LEFT JOIN "pl-suggestion-run-products" p ON p.run_id=n.run_id AND p.product_id=n.product_id
 WHERE p.run_id IS NULL) THEN
  RAISE EXCEPTION 'Suggestion run/product is outside its immutable scope'
  USING ERRCODE='23503',CONSTRAINT='pl-suggestion-product-membership';
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER "pl-suggestion-membership-insert" AFTER INSERT ON "pl-order-suggestions"
 REFERENCING NEW TABLE AS new_suggestions FOR EACH STATEMENT EXECUTE FUNCTION "pl-check-suggestion-membership"();
CREATE TRIGGER "pl-suggestion-membership-update" AFTER UPDATE ON "pl-order-suggestions"
 REFERENCING NEW TABLE AS new_suggestions FOR EACH STATEMENT EXECUTE FUNCTION "pl-check-suggestion-membership"();
-- Install guards before removing the redundant row trigger. Existing FK was valid.
ALTER TABLE "pl-order-suggestions" DROP CONSTRAINT "pl-suggestion-product-scalar";
-- +goose StatementEnd
-- +goose Down
SELECT 1;
