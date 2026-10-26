package stock

import (
	"context"
	"eczanet/tests/dbtest"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	t                                    *testing.T
	ctx                                  context.Context
	owner, runtime                       *pgxpool.Pool
	svc                                  Service
	warehouse, pharmacist, depot, branch string
	prefix                               string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUNTIME_URL") == "" {
		t.Skip("real PostgreSQL URLs required")
	}
	dbtest.Acquire(t, false)
	f := &fixture{t: t, ctx: context.Background(), prefix: fmt.Sprint(time.Now().UnixNano())}
	var e error
	f.owner, e = pgxpool.New(f.ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	config, e := pgxpool.ParseConfig(os.Getenv("TEST_RUNTIME_URL"))
	if e != nil {
		t.Fatal(e)
	}
	config.MaxConns = 20
	f.runtime, e = pgxpool.NewWithConfig(f.ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	f.svc = Service{Pool: f.runtime}
	for _, v := range []struct {
		name, kind string
		dest       *string
	}{{"D", "warehouse", &f.depot}, {"B", "branch", &f.branch}} {
		e = f.owner.QueryRow(f.ctx, `INSERT INTO "pl-locations"(name,kind) VALUES($1,$2) RETURNING id::text`, f.prefix+v.name, v.kind).Scan(v.dest)
		if e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range []struct {
		role, loc string
		dest      *string
	}{{"warehouse", f.depot, &f.warehouse}, {"pharmacist", f.branch, &f.pharmacist}} {
		e = f.owner.QueryRow(f.ctx, `INSERT INTO "pl-wy-users"(email,password_hash,role) VALUES($1,'$2-test-only',$2) RETURNING id::text`, f.prefix+v.role+"@invalid", v.role).Scan(v.dest)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.owner.Exec(f.ctx, `INSERT INTO "pl-user-locations" VALUES($1,$2)`, *v.dest, v.loc); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(func() {
		// This fixture owns only these two generated locations/users. Never truncate demo tables.
		tx, e := f.owner.Begin(f.ctx)
		if e != nil {
			t.Error(e)
			return
		}
		defer tx.Rollback(f.ctx)
		for _, q := range []string{
			`DELETE FROM "pl-sale-allocations" WHERE line_id IN (SELECT l.id FROM "pl-sale-lines" l JOIN "pl-sales" s ON s.id=l.sale_id WHERE s.location_id IN ($1,$2))`,
			`DELETE FROM "pl-sale-lines" WHERE sale_id IN (SELECT id FROM "pl-sales" WHERE location_id IN ($1,$2))`,
			`DELETE FROM "pl-sales" WHERE location_id IN ($1,$2)`,
			`DELETE FROM "pl-transfer-receipt-lines" WHERE transfer_id IN (SELECT id FROM "pl-transfers" WHERE source_id IN ($1,$2))`,
			`DELETE FROM "pl-transfer-receipts" WHERE transfer_id IN (SELECT id FROM "pl-transfers" WHERE source_id IN ($1,$2))`,
			`DELETE FROM "pl-transfer-lines" WHERE transfer_id IN (SELECT id FROM "pl-transfers" WHERE source_id IN ($1,$2))`,
			`DELETE FROM "pl-transfers" WHERE source_id IN ($1,$2)`,
			`DELETE FROM "pl-goods-receipt-lines" WHERE receipt_id IN (SELECT id FROM "pl-goods-receipts" WHERE location_id IN ($1,$2))`,
			`DELETE FROM "pl-goods-receipts" WHERE location_id IN ($1,$2)`,
			`WITH gone AS (DELETE FROM "pl-stock-entries" WHERE location_id IN ($1,$2) RETURNING event_id) DELETE FROM "pl-stock-events" WHERE id IN (SELECT event_id FROM gone)`,
			`DELETE FROM "pl-stock-balances" WHERE location_id IN ($1,$2)`,
			`DELETE FROM "pl-inventory-guards" WHERE location_id IN ($1,$2)`,
			`DELETE FROM "pl-idempotency" WHERE location_id IN ($1,$2)`,
		} {
			if _, e = tx.Exec(f.ctx, q, f.depot, f.branch); e != nil {
				t.Error("fixture cleanup", e)
				return
			}
		}
		for _, q := range []string{`DELETE FROM "pl-audit-events" WHERE actor_id IN ($1,$2)`, `DELETE FROM "pl-user-locations" WHERE user_id IN ($1,$2)`, `DELETE FROM "pl-wy-users" WHERE id IN ($1,$2)`} {
			if _, e = tx.Exec(f.ctx, q, f.warehouse, f.pharmacist); e != nil {
				t.Error(e)
				return
			}
		}
		if _, e = tx.Exec(f.ctx, `DELETE FROM "pl-locations" WHERE id IN ($1,$2)`, f.depot, f.branch); e != nil {
			t.Error(e)
			return
		}
		for _, q := range []string{`DELETE FROM "pl-lots" WHERE product_id IN (SELECT id FROM "pl-products" WHERE name LIKE $1)`, `DELETE FROM "pl-price-periods" WHERE product_id IN (SELECT id FROM "pl-products" WHERE name LIKE $1)`, `DELETE FROM "pl-product-ingredients" WHERE product_id IN (SELECT id FROM "pl-products" WHERE name LIKE $1)`, `DELETE FROM "pl-product-history" WHERE product_id IN (SELECT id FROM "pl-products" WHERE name LIKE $1)`, `DELETE FROM "pl-products" WHERE name LIKE $1`} {
			if _, e = tx.Exec(f.ctx, q, f.prefix+"%"); e != nil {
				t.Error(e)
				return
			}
		}
		if e = tx.Commit(f.ctx); e != nil {
			t.Error(e)
		}
		f.runtime.Close()
		f.owner.Close()
	})
	return f
}
func (f *fixture) exec(cmd, obj string, in Input) map[string]any {
	f.t.Helper()
	actor := f.warehouse
	if in.LocationID == f.branch {
		actor = f.pharmacist
	}
	r, e := f.svc.Execute(f.ctx, actor, cmd, obj, fmt.Sprint(time.Now().UnixNano()), "test-request", in)
	if e != nil {
		f.t.Fatalf("%s: %v", cmd, e)
	}
	var out map[string]any
	if e = json.Unmarshal(r.Body, &out); e != nil {
		f.t.Fatal(e)
	}
	return out
}
func (f *fixture) product(name string) string {
	p := Product{Barcode: "00" + fmt.Sprint(time.Now().UnixNano()), Name: f.prefix + name, Dose: "5mg", Form: "tablet", BaseUnit: "tablet", PackSize: "10", BlisterSize: "5", SaleMultiple: "1", Ingredients: []string{"demo"}, PackPrice: "1000"}
	return f.exec("product.create", "", Input{LocationID: f.depot, Product: &p})["id"].(string)
}
func (f *fixture) receive(product, lot string, q int) {
	f.exec("receipt.create", "", Input{LocationID: f.depot, SupplierID: "1", DocumentNo: fmt.Sprint(time.Now().UnixNano()), Reason: "test", Lines: []ReceiptLine{{product, lot, "2099-12-31", strconv.Itoa(q)}}})
}
func (f *fixture) deliver(product string, q int) {
	id := f.exec("transfer.create", "", Input{LocationID: f.depot, TargetID: f.branch, Items: []Item{{product, strconv.Itoa(q), "unit"}}})["id"].(string)
	f.exec("transfer.send", id, Input{LocationID: f.depot})
	f.exec("transfer.receive", id, Input{LocationID: f.branch})
}
func (f *fixture) order(product string, q int) Input {
	in := Input{LocationID: f.branch, Items: []Item{{product, strconv.Itoa(q), "unit"}}}
	in.Quote = f.exec("sale.quote", "", in)["quote"].(string)
	return in
}
func (f *fixture) invariant() {
	f.t.Helper()
	var n int
	e := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-stock-balances" b WHERE location_id IN ($1,$2) AND quantity<>(SELECT coalesce(sum(quantity),0) FROM "pl-stock-entries" e WHERE e.location_id=b.location_id AND e.lot_id=b.lot_id AND e.account=b.account)`, f.depot, f.branch).Scan(&n)
	if e != nil || n != 0 {
		f.t.Fatal("ledger mismatch", n, e)
	}
}
func TestPostgresJourney(t *testing.T) {
	f := setup(t)
	p := f.product("journey")
	f.receive(p, "E", 10)
	f.deliver(p, 6)
	out := f.exec("sale.create", "", f.order(p, 2))
	if out["total"] != "200" {
		t.Fatal(out)
	}
	var auditTotal, auditQty string
	if e := f.owner.QueryRow(f.ctx, `SELECT new_value->'result'->>'total',new_value->'input'->'items'->0->>'quantity' FROM "pl-audit-events" WHERE actor_id=$1 AND command='sale.create' ORDER BY id DESC LIMIT 1`, f.pharmacist).Scan(&auditTotal, &auditQty); e != nil || auditTotal != "200" || auditQty != "2" {
		t.Fatal("audit snapshot", auditTotal, auditQty, e)
	}

	var d, b int64
	e := f.owner.QueryRow(f.ctx, `SELECT coalesce(sum(quantity) FILTER(WHERE location_id=$1),0),coalesce(sum(quantity) FILTER(WHERE location_id=$2),0) FROM "pl-stock-balances" WHERE account='physical' AND lot_id IN (SELECT id FROM "pl-lots" WHERE product_id=$3)`, f.depot, f.branch, p).Scan(&d, &b)
	if e != nil || d != 4 || b != 4 {
		t.Fatal(d, b, e)
	}
	f.invariant()
}
func TestPostgresRace200(t *testing.T) {
	f := setup(t)
	p := f.product("race")
	f.receive(p, "E", 100)
	f.deliver(p, 100)
	in := f.order(p, 1)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 200)
	started := time.Now()
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-gate
			_, e := f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", fmt.Sprintf("race-key-%s-%d", f.prefix, n), "race", in)
			results <- e
		}(i)
	}
	close(gate)
	wg.Wait()
	close(results)
	ok, reject := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else {
			var b *Error
			if errors.As(e, &b) && b.Code == "INSUFFICIENT_STOCK" {
				reject++
			} else {
				t.Errorf("unexpected %v", e)
			}
		}
	}
	if ok != 100 || reject != 100 {
		t.Fatal(ok, reject)
	}
	f.invariant()
	t.Logf("200 concurrent: success=%d stock_reject=%d timeout/5xx=0 duration=%s", ok, reject, time.Since(started))
}
func TestPostgresSameKey200(t *testing.T) {
	f := setup(t)
	p := f.product("idem")
	f.receive(p, "E", 100)
	f.deliver(p, 100)
	in := f.order(p, 1)
	gate := make(chan struct{})
	out := make(chan Result, 200)
	errs := make(chan error, 200)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			r, e := f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "same-key-200", "same", in)
			out <- r
			errs <- e
		}()
	}
	close(gate)
	wg.Wait()
	close(out)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	body := ""
	for r := range out {
		if body == "" {
			body = string(r.Body)
		}
		if r.Status != 201 || string(r.Body) != body {
			t.Fatal("replay mismatch")
		}
	}
	in.Items[0].Quantity = "2"
	_, e := f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "same-key-200", "same", in)
	if e == nil {
		t.Fatal("body conflict")
	}
	var count int
	if e = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-sales" WHERE location_id=$1`, f.branch).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	f.invariant()
	t.Log("200 same key: one sale, identical status/body")
}
func TestPostgresFEFORollbackAndPermissions(t *testing.T) {
	f := setup(t)
	p := f.product("fefo")
	f.receive(p, "E", 4)
	f.receive(p, "L", 10)
	f.deliver(p, 14)
	// Equal expiry uses lot ID as deterministic tie-breaker.
	out := f.exec("sale.create", "", f.order(p, 6))
	b, _ := json.Marshal(out)
	t.Log(string(b))
	var a, bq int64
	e := f.owner.QueryRow(f.ctx, `SELECT sum(quantity) FILTER(WHERE l.number='E'),sum(quantity) FILTER(WHERE l.number='L') FROM "pl-sale-allocations" a JOIN "pl-lots" l ON l.id=a.lot_id WHERE l.product_id=$1`, p).Scan(&a, &bq)
	if e != nil || a != 4 || bq != 2 {
		t.Fatal(a, bq, e)
	}
	q := f.product("empty")
	in := Input{LocationID: f.branch, Items: []Item{{p, "1", "unit"}, {q, "1", "unit"}}}
	in.Quote = f.exec("sale.quote", "", in)["quote"].(string)
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "rollback-multi", "test", in)
	if e == nil {
		t.Fatal("multi product accepted")
	}
	// Fail after all writes, before commit: stock, sale, audit and idempotency must all roll back.
	in = f.order(p, 1)
	if e = normalize(&in); e != nil {
		t.Fatal(e)
	}
	before := f.effectCounts()
	_, e = f.svc.once(f.ctx, f.pharmacist, "sale.create", "", "injected-rollback", "test", in, func() error { return errors.New("test-only failure") })
	if e == nil {
		t.Fatal("failure hook")
	}
	if after := f.effectCounts(); before != after {
		t.Fatalf("rollback leaked effects: before=%v after=%v", before, after)
	}
	var count int
	_ = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-idempotency" WHERE key='injected-rollback' AND actor_id=$1`, f.pharmacist).Scan(&count)
	if count != 0 {
		t.Fatal("idempotency leaked")
	}
	for _, table := range []string{"pl-stock-events", "pl-stock-entries", "pl-audit-events"} {
		for _, op := range []string{"UPDATE", "DELETE"} {
			query := fmt.Sprintf(`DELETE FROM "%s" WHERE false`, table)
			if op == "UPDATE" {
				query = fmt.Sprintf(`UPDATE "%s" SET id=id WHERE false`, table)
			}
			if _, e = f.runtime.Exec(f.ctx, query); e == nil {
				t.Fatal("append-only permission", query)
			}
		}
	}
	f.invariant()
}
func TestPostgresConstraints(t *testing.T) {
	f := setup(t)
	p := f.product("constraint")
	f.receive(p, "E", 1)
	for _, query := range []string{`INSERT INTO "pl-stock-events"(kind,reference) VALUES('bad','empty')`, `UPDATE "pl-products" SET pack_size=20 WHERE id=` + p, `UPDATE "pl-lots" SET expires_on='2100-01-01' WHERE product_id=` + p, `DELETE FROM "pl-products" WHERE id=` + p, `UPDATE "pl-stock-balances" SET quantity=-1 WHERE account='physical' AND lot_id IN (SELECT id FROM "pl-lots" WHERE product_id=` + p + `)`} {
		if _, e := f.owner.Exec(f.ctx, query); e == nil {
			t.Fatal("constraint accepted", query)
		}
	}
	f.invariant()
}
func TestPostgresReceiptUniquenessAndRollback(t *testing.T) {
	f := setup(t)
	p := f.product("receipt")
	in := Input{LocationID: f.depot, SupplierID: "1", DocumentNo: f.prefix, Reason: "test", Lines: []ReceiptLine{{p, "E", "2099-12-31", "10"}}}
	first := f.exec("receipt.create", "", in)
	second := f.exec("receipt.create", "", in)
	if first["id"] != second["id"] {
		t.Fatal("duplicate document")
	}
	in.Lines[0].Quantity = "11"
	if _, e := f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", "document-conflict", "test", in); e == nil {
		t.Fatal("document conflict")
	}
	in.DocumentNo += "bad"
	in.Lines = append(in.Lines, ReceiptLine{p, "X", "2000-01-01", "1"})
	if _, e := f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", "multi-receipt-rollback", "test", in); e == nil {
		t.Fatal("past receipt accepted")
	}
	var count int
	_ = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-goods-receipts" WHERE document_no=$1`, in.DocumentNo).Scan(&count)
	if count != 0 {
		t.Fatal("partial receipt")
	}
	in.Lines = []ReceiptLine{{p, "E", "2098-12-31", "1"}}
	if _, e := f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", "lot-conflict-key", "test", in); e == nil {
		t.Fatal("lot expiry conflict")
	}
	f.invariant()
}
func TestPostgresExpiryAndPriceSnapshot(t *testing.T) {
	f := setup(t)
	p := f.product("expiry")
	f.receive(p, "E", 4)
	f.receive(p, "L", 10)
	f.deliver(p, 14)
	tx0, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx0.Rollback(f.ctx)
	if e = guards(f.ctx, tx0, f.branch, []string{p}); e != nil {
		t.Fatal(e)
	}
	var expired string
	if e = tx0.QueryRow(f.ctx, `INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on) VALUES($1,1,'X','2000-01-01') RETURNING id::text`, p).Scan(&expired); e != nil {
		t.Fatal(e)
	}
	if e = movement(f.ctx, tx0, "test-opening", f.prefix, f.branch, f.branch, expired, "supply", "physical", 4); e != nil {
		t.Fatal(e)
	}
	if e = tx0.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	in := f.order(p, 6)
	out := f.exec("sale.create", "", in)
	var used int
	e = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-sale-allocations" a JOIN "pl-lots" l ON l.id=a.lot_id WHERE l.product_id=$1 AND l.number='X'`, p).Scan(&used)
	if e != nil || used != 0 {
		t.Fatal("expired allocation", used, e)
	}
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(f.ctx, `UPDATE "pl-price-periods" SET valid_to=clock_timestamp() WHERE product_id=$1`, p)
	if e == nil {
		_, e = tx.Exec(f.ctx, `INSERT INTO "pl-price-periods"(product_id,valid_from,pack_price) VALUES($1,clock_timestamp(),2000)`, p)
	}
	if e != nil {
		tx.Rollback(f.ctx)
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "old-quote-reject", "test", in)
	var b *Error
	if !errors.As(e, &b) || b.Code != "PRICE_CHANGED" {
		t.Fatal(e)
	}
	var total string
	e = f.owner.QueryRow(f.ctx, `SELECT total::text FROM "pl-sales" WHERE id=$1`, out["id"]).Scan(&total)
	if e != nil || total != "600" {
		t.Fatal(total, e)
	}
	f.invariant()
}
func TestPostgresLostResponseAndRevokedReplay(t *testing.T) {
	f := setup(t)
	p := f.product("lost")
	f.receive(p, "E", 10)
	f.deliver(p, 10)
	in := f.order(p, 1)
	_, e := f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "response-lost-key", "test", in)
	if e != nil {
		t.Fatal(e)
	} // discard successful response, representing loss after commit
	r, e := f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "response-lost-key", "test", in)
	if e != nil || r.Status != 201 {
		t.Fatal(e)
	}
	if _, e = f.owner.Exec(f.ctx, `DELETE FROM "pl-user-locations" WHERE user_id=$1`, f.pharmacist); e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "response-lost-key", "test", in)
	var b *Error
	if !errors.As(e, &b) || b.Status != 403 {
		t.Fatal("revoked replay", e)
	}
	f.invariant()
}
func TestPostgresKeysetScope(t *testing.T) {
	f := setup(t)
	f.product("page1")
	f.product("page2")
	p, e := f.svc.Read(f.ctx, f.pharmacist, "products", "", f.prefix, "", 1)
	if e != nil || p.Next == "" || len(p.Items) != 1 {
		t.Fatal(p, e)
	}
	next, e := f.svc.Read(f.ctx, f.pharmacist, "products", "", f.prefix, p.Next, 1)
	if e != nil || len(next.Items) != 1 || string(p.Items[0]) == string(next.Items[0]) {
		t.Fatal(next, e)
	}
	if _, e = f.svc.Read(f.ctx, f.pharmacist, "products", "", "other", p.Next, 1); e == nil {
		t.Fatal("cursor scope")
	}
	if _, e = f.svc.Read(f.ctx, f.pharmacist, "products", "", f.prefix, "", 201); e == nil {
		t.Fatal("limit")
	}
}
func TestPostgresIncomingEarlyLotGuard(t *testing.T) {
	f := setup(t)
	p := f.product("incoming")
	f.receive(p, "L", 10)
	f.deliver(p, 10)
	in := f.order(p, 6)
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	if e = guards(f.ctx, tx, f.branch, []string{p}); e != nil {
		t.Fatal(e)
	}
	var lot string
	if e = tx.QueryRow(f.ctx, `INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on) VALUES($1,1,'E','2098-01-01') RETURNING id::text`, p).Scan(&lot); e != nil {
		t.Fatal(e)
	}
	if e = movement(f.ctx, tx, "test-incoming", f.prefix, f.branch, f.branch, lot, "supply", "physical", 4); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, e := f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "guard-incoming-key", "test", in)
		done <- e
	}()
	select {
	case e = <-done:
		t.Fatal("sale bypassed incoming guard", e)
	case <-time.After(100 * time.Millisecond):
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	var consumed int64
	e = f.owner.QueryRow(f.ctx, `SELECT coalesce(sum(quantity),0) FROM "pl-sale-allocations" WHERE lot_id=$1`, lot).Scan(&consumed)
	if e != nil || consumed != 4 {
		t.Fatal("early incoming lot skipped", consumed, e)
	}
	f.invariant()
}
func TestPostgresDeferredLedger(t *testing.T) {
	f := setup(t)
	p := f.product("ledger")
	f.receive(p, "E", 10)
	var lot string
	if e := f.owner.QueryRow(f.ctx, `SELECT id::text FROM "pl-lots" WHERE product_id=$1`, p).Scan(&lot); e != nil {
		t.Fatal(e)
	}
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	var event string
	if e = tx.QueryRow(f.ctx, `INSERT INTO "pl-stock-events"(kind,reference) VALUES('bad','unbalanced') RETURNING id::text`).Scan(&event); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `UPDATE "pl-stock-balances" SET quantity=quantity+1 WHERE location_id=$1 AND lot_id=$2 AND account='physical'`, f.depot, lot); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `INSERT INTO "pl-stock-entries"(event_id,location_id,lot_id,account,quantity) VALUES($1,$2,$3,'physical',1)`, event, f.depot, lot); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e == nil {
		t.Fatal("unbalanced event committed")
	}
	if _, e = f.owner.Exec(f.ctx, `UPDATE "pl-stock-balances" SET quantity=quantity+1 WHERE location_id=$1 AND lot_id=$2 AND account='physical'`, f.depot, lot); e == nil {
		t.Fatal("unreconciled balance committed")
	}
	f.invariant()
}
func TestPostgresNonPhysicalAndTransferStates(t *testing.T) {
	f := setup(t)
	p := f.product("transit")
	f.receive(p, "E", 10)
	id := f.exec("transfer.create", "", Input{LocationID: f.depot, TargetID: f.branch, Items: []Item{{p, "10", "unit"}}})["id"].(string)
	f.exec("transfer.send", id, Input{LocationID: f.depot})
	in := f.order(p, 1)
	if _, e := f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "transit-not-sale", "test", in); e == nil {
		t.Fatal("transit sold before receipt")
	}
	if _, e := f.svc.Execute(f.ctx, f.warehouse, "transfer.send", id, "second-send-key", "test", Input{LocationID: f.depot}); e == nil {
		t.Fatal("duplicate send")
	}
	if _, e := f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", id, "partial-reject-key", "test", Input{LocationID: f.branch, Items: []Item{{p, "1", "unit"}}}); e == nil {
		t.Fatal("partial accepted")
	}
	input := Input{LocationID: f.branch}
	r, e := f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", id, "receive-idem-key", "test", input)
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", id, "receive-idem-key", "test", input)
	if e != nil || string(r.Body) != string(again.Body) {
		t.Fatal("receive replay", e)
	}
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	if e = guards(f.ctx, tx, f.branch, []string{p}); e != nil {
		t.Fatal(e)
	}
	var lot string
	if e = tx.QueryRow(f.ctx, `SELECT id::text FROM "pl-lots" WHERE product_id=$1`, p).Scan(&lot); e != nil {
		t.Fatal(e)
	}
	if e = movement(f.ctx, tx, "test-quarantine", f.prefix, f.branch, f.branch, lot, "physical", "quarantine", 10); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "quarantine-not-sale", "test", in); e == nil {
		t.Fatal("quarantine sold")
	}
	if _, e = f.owner.Exec(f.ctx, `UPDATE "pl-products" SET prescription_required=true WHERE id=$1`, p); e != nil {
		t.Fatal(e)
	}
	in.Quote = ""
	if _, e = f.svc.Execute(f.ctx, f.pharmacist, "sale.quote", "", "", "test", in); e == nil {
		t.Fatal("prescription product normal quote")
	}
	f.invariant()
}
func TestRetryClasses(t *testing.T) {
	if !retryable(&pgconn.PgError{Code: "40P01"}) || !retryable(&pgconn.PgError{Code: "40001"}) || retryable(&pgconn.PgError{Code: "55P03"}) {
		t.Fatal("retry classification")
	}
	e := classified(&pgconn.PgError{Code: "55P03"})
	var b *Error
	if !errors.As(e, &b) || b.Status != 503 {
		t.Fatal("lock timeout misreported", e)
	}
}
func TestPostgresCatalogValidationAndSearch(t *testing.T) {
	f := setup(t)
	// Keep this regression meaningful even on a separate empty database: put
	// the target after more than 200 matches, never replace it with an old row.
	var matches int
	if e := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-products" p WHERE p.name ILIKE '%demo%' OR EXISTS(SELECT 1 FROM "pl-product-ingredients" pi JOIN "pl-ingredients" i ON i.id=pi.ingredient_id WHERE pi.product_id=p.id AND i.name ILIKE '%demo%')`).Scan(&matches); e != nil {
		t.Fatal(e)
	}
	for ; matches <= 200; matches++ {
		f.product(fmt.Sprintf("demo-padding-%d", matches))
	}
	p := f.product("catalog")
	var barcode string
	if e := f.owner.QueryRow(f.ctx, `SELECT barcode FROM "pl-products" WHERE id=$1`, p).Scan(&barcode); e != nil {
		t.Fatal(e)
	}
	for _, term := range []string{barcode, f.prefix + "catalog", "demo"} {
		found := false
		cursor, total, pages, ordinal := "", 0, 0, 0
		seen := map[string]bool{}
		for {
			page, e := f.svc.Read(f.ctx, f.pharmacist, "products", "", term, cursor, 200)
			if e != nil {
				t.Fatal(e)
			}
			pages++
			for _, b := range page.Items {
				total++
				var row Product
				if e = json.Unmarshal(b, &row); e != nil {
					t.Fatal(e)
				}
				if row.ID == p && row.Barcode == barcode {
					found = true
					ordinal = total
				}
			}
			if page.Next == "" {
				break
			}
			if seen[page.Next] {
				t.Fatal("repeated catalog cursor")
			}
			seen[page.Next] = true
			cursor = page.Next
		}
		if !found {
			t.Fatal("catalog search", term)
		}
		if term == "demo" && (total <= 200 || ordinal <= 200 || pages < 2) {
			t.Fatal("pagination fixture did not cross first page", total, ordinal, pages)
		}
		t.Logf("catalog search %q: matches=%d pages=%d target_position=%d", term, total, pages, ordinal)
	}
	for _, query := range []string{`UPDATE "pl-products" SET pack_size=0 WHERE id=` + p, `UPDATE "pl-products" SET sale_multiple=3 WHERE id=` + p, `UPDATE "pl-products" SET dose='-5mg' WHERE id=` + p, `INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on) VALUES(99999999,1,'orphan','2099-01-01')`, `INSERT INTO "pl-products"(barcode,name,dose,form,base_unit,pack_size,blister_size,sale_multiple) SELECT barcode,name,dose,form,base_unit,pack_size,blister_size,sale_multiple FROM "pl-products" WHERE id=` + p} {
		if _, e := f.owner.Exec(f.ctx, query); e == nil {
			t.Fatal("invalid DB value accepted", query)
		}
	}
}

func (f *fixture) effectCounts() [6]int {
	f.t.Helper()
	var out [6]int
	for i, q := range []string{`SELECT count(*) FROM "pl-sales" WHERE location_id=$1`, `SELECT count(*) FROM "pl-sale-lines" WHERE sale_id IN (SELECT id FROM "pl-sales" WHERE location_id=$1)`, `SELECT count(*) FROM "pl-sale-allocations" WHERE line_id IN (SELECT l.id FROM "pl-sale-lines" l JOIN "pl-sales" s ON s.id=l.sale_id WHERE s.location_id=$1)`, `SELECT count(*) FROM "pl-stock-entries" WHERE location_id=$1`, `SELECT count(*) FROM "pl-audit-events" WHERE actor_id IN (SELECT user_id FROM "pl-user-locations" WHERE location_id=$1)`, `SELECT count(*) FROM "pl-idempotency" WHERE location_id=$1`} {
		if e := f.owner.QueryRow(f.ctx, q, f.branch).Scan(&out[i]); e != nil {
			f.t.Fatal(e)
		}
	}
	return out
}
func TestPostgresLockTimeoutIsBusyAndRetrySafe(t *testing.T) {
	f := setup(t)
	p := f.product("busy")
	f.receive(p, "E", 10)
	f.deliver(p, 10)
	in := f.order(p, 1)
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	if e = guards(f.ctx, tx, f.branch, []string{p}); e != nil {
		t.Fatal(e)
	}
	svc := f.svc
	svc.LockTimeout = 50 * time.Millisecond
	before := f.effectCounts()
	_, e = svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "lock-timeout-key", "test", in)
	var b *Error
	if !errors.As(e, &b) || b.Status != 503 {
		t.Fatal("busy must not be stock reject", e)
	}
	if after := f.effectCounts(); after != before {
		t.Fatal("timeout leaked effects", before, after)
	}
	if e = tx.Rollback(f.ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Execute(f.ctx, f.pharmacist, "sale.create", "", "lock-timeout-key", "test", in); e != nil {
		t.Fatal("same key retry after rollback", e)
	}
	f.invariant()
}
