package stock

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

func TestA5RunScopeForeignKeys(t *testing.T) {
	f := a3Fixture(t)
	product := f.product("scope-integrity")
	run := a5Run(t, f)
	// Immutable mappings plus statement membership protect each result pair.
	// Ordinary parent FKs still protect the actual run and product.
	cases := []struct {
		query, constraint string
		args              []any
	}{
		{`DELETE FROM "pl-suggestion-run-products" WHERE run_id=$1 AND product_id=$2`, "pl-suggestion-products-immutable", []any{run, product}},
		{`UPDATE "pl-suggestion-run-products" SET product_id=9223372036854775807 WHERE run_id=$1 AND product_id=$2`, "", []any{run, product}},
		{`INSERT INTO "pl-suggestion-run-products" VALUES(9223372036854775807,$1)`, "pl-suggestion-run-products_run_id_fkey", []any{product}},
		{`INSERT INTO "pl-suggestion-run-products" VALUES($1,9223372036854775807)`, "pl-suggestion-run-products_product_id_fkey", []any{run}},
	}
	for _, c := range cases {
		tx, e := f.owner.Begin(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec(f.ctx, c.query, c.args...)
		_ = tx.Rollback(f.ctx)
		var pgerr *pgconn.PgError
		if !errors.As(e, &pgerr) || pgerr.Code != "23503" || (c.constraint != "" && pgerr.ConstraintName != c.constraint) {
			t.Fatalf("hard FK not enforced: %v", e)
		}
	}
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(f.ctx, `UPDATE "pl-order-suggestions" SET product_scope_key='forged' WHERE run_id=$1 AND product_id=$2`, run, product)
	_ = tx.Rollback(f.ctx)
	var generated *pgconn.PgError
	if !errors.As(e, &generated) || generated.Code != "428C9" {
		t.Fatal("generated pair key must not be writable", e)
	}
	// A product created after this snapshot is real, but is not a member of this run.
	late := f.product("late-scope")
	tx, e = f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(f.ctx, `UPDATE "pl-order-suggestions" SET product_id=$2 WHERE run_id=$1 AND product_id=$3`, run, late, product)
	_ = tx.Rollback(f.ctx)
	var membership *pgconn.PgError
	if !errors.As(e, &membership) || membership.Code != "23503" || membership.ConstraintName != "pl-suggestion-product-membership" {
		t.Fatal("set membership UPDATE", e)
	}
	tx, e = f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(f.ctx, `TRUNCATE "pl-suggestion-run-products"`)
	_ = tx.Rollback(f.ctx)
	if !errors.As(e, &membership) || membership.Code != "23503" {
		t.Fatal("immutable parent TRUNCATE", e)
	}
	tx, e = f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(f.ctx, `INSERT INTO "pl-order-suggestions"(run_id,location_id,product_id,observed_days,gross,returned,rate,horizon,season,minimum,target,physical,risk,effective,pending,excluded_pending,unknown_lot,suggested,boxes,return_quantity,defaults,history_unknown,policy_version) SELECT run_id,location_id,$2,observed_days,gross,returned,rate,horizon,season,minimum,target,physical,risk,effective,pending,excluded_pending,unknown_lot,suggested,boxes,return_quantity,defaults,history_unknown,policy_version FROM "pl-order-suggestions" WHERE run_id=$1 AND product_id=$3 LIMIT 1`, run, late, product)
	_ = tx.Rollback(f.ctx)
	if !errors.As(e, &membership) || membership.Code != "23503" || membership.ConstraintName != "pl-suggestion-product-membership" {
		t.Fatal("set membership INSERT", e)
	}
	_, e = f.runtime.Exec(f.ctx, `DELETE FROM "pl-suggestion-run-products" WHERE run_id=$1`, run)
	if !errors.As(e, &membership) || membership.Code != "42501" {
		t.Fatal("runtime scope mutation ACL", e)
	}
	var scopes, locations, results int64
	if e := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM "pl-suggestion-run-products" WHERE run_id=$1),(SELECT count(*) FROM "pl-suggestion-run-locations" WHERE run_id=$1),(SELECT count(*) FROM "pl-order-suggestions" WHERE run_id=$1)`, run).Scan(&scopes, &locations, &results); e != nil || scopes*locations != results {
		t.Fatal(scopes, locations, results, e)
	}
}
