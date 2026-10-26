package stock

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func a4CountFixture(t *testing.T) (*fixture, string, string, string) {
	f := a3Fixture(t)
	p := f.product("a4-count")
	f.receive(p, "A4-COUNT", 20)
	f.deliver(p, 10)
	var manager, lot string
	if e := f.owner.QueryRow(f.ctx, `INSERT INTO "pl-wy-users"(email,password_hash,role) VALUES($1,'$2-test','manager') RETURNING id::text`, strings.ToLower(f.prefix)+"manager@invalid").Scan(&manager); e != nil {
		t.Fatal(e)
	}
	if _, e := f.owner.Exec(f.ctx, `INSERT INTO "pl-user-locations" VALUES($1,$2)`, manager, f.branch); e != nil {
		t.Fatal(e)
	}
	if e := f.owner.QueryRow(f.ctx, `SELECT id::text FROM "pl-lots" WHERE product_id=$1`, p).Scan(&lot); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, e := f.owner.Exec(f.ctx, `UPDATE "pl-counts" SET status='CANCELLED' WHERE product_id=$1 AND status='OPEN'`, p)
		if e != nil {
			t.Error(e)
		}
	})
	return f, p, manager, lot
}

func countExec(t *testing.T, f *fixture, actor, cmd, id string, in Input) map[string]any {
	t.Helper()
	r, e := f.svc.Execute(f.ctx, actor, cmd, id, fmt.Sprint(time.Now().UnixNano()), "count-test", in)
	if e != nil {
		t.Fatal(cmd, e)
	}
	out := map[string]any{}
	if e = json.Unmarshal(r.Body, &out); e != nil {
		t.Fatal(e)
	}
	return out
}

func TestA4PostgresCountFreezeFinalizeCancelExpiry(t *testing.T) {
	f, p, manager, lot := a4CountFixture(t)
	sale := f.order(p, 1)
	start := Input{LocationID: f.branch, Count: &CountInput{ProductID: p}}
	id := countExec(t, f, manager, "count.start", "", start)["id"].(string)
	_, e := f.svc.Execute(f.ctx, manager, "count.start", "", f.prefix+"duplicate-count", "test", start)
	assertCode(t, e, "COUNT_IN_PROGRESS")
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", f.prefix+"frozen-sale", "test", sale)
	assertCode(t, e, "COUNT_IN_PROGRESS")
	// Source or target scope freezes delivery as a whole, including source transit.
	tr := f.exec("transfer.create", "", Input{LocationID: f.depot, TargetID: f.branch, Items: []Item{{p, "1", "unit"}}})["id"].(string)
	f.exec("transfer.send", tr, Input{LocationID: f.depot})
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", tr, f.prefix+"frozen-delivery", "test", Input{LocationID: f.branch})
	assertCode(t, e, "COUNT_IN_PROGRESS")
	countExec(t, f, manager, "count.extend", id, Input{LocationID: f.branch, Reason: "Fiziksel kontrol suruyor"})
	final := Input{LocationID: f.branch, Reason: "Iki birim fiziksel eksik", Count: &CountInput{ProductID: p, Lines: []CountLine{{lot, "physical", "-1"}}}}
	_, e = f.svc.Execute(f.ctx, manager, "count.finalize", id, f.prefix+"negative", "test", final)
	assertCode(t, e, "INVALID_INPUT")
	final.Count.Lines[0].Actual = "8"
	key := f.prefix + "final"
	a, e := f.svc.Execute(f.ctx, manager, "count.finalize", id, key, "test", final)
	if e != nil {
		t.Fatal(e)
	}
	b, e := f.svc.Execute(f.ctx, manager, "count.finalize", id, key, "replay", final)
	if e != nil || string(a.Body) != string(b.Body) {
		t.Fatal(e)
	}
	_, e = f.svc.Execute(f.ctx, manager, "count.finalize", id, key+"new", "test", final)
	assertCode(t, e, "INVALID_STATE")
	var physical, counter int64
	e = f.owner.QueryRow(f.ctx, `SELECT coalesce(sum(quantity) FILTER(WHERE account='physical'),0),coalesce(sum(quantity) FILTER(WHERE account='count'),0) FROM "pl-stock-balances" WHERE location_id=$1 AND lot_id=$2`, f.branch, lot).Scan(&physical, &counter)
	if e != nil || physical != 8 || counter != 2 {
		t.Fatal(physical, counter, e)
	}
	id = countExec(t, f, manager, "count.start", "", start)["id"].(string)
	countExec(t, f, manager, "count.cancel", id, Input{LocationID: f.branch, Reason: "Sayim iptal"})
	id = countExec(t, f, manager, "count.start", "", start)["id"].(string)
	if _, e = f.owner.Exec(f.ctx, `UPDATE "pl-counts" SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.Execute(f.ctx, manager, "count.finalize", id, key+"expired", "test", final)
	assertCode(t, e, "INVALID_STATE")
	if e = f.svc.ExpireCounts(f.ctx); e != nil {
		t.Fatal(e)
	}
	var state string
	if e = f.owner.QueryRow(f.ctx, `SELECT status FROM "pl-counts" WHERE id=$1`, id).Scan(&state); e != nil || state != "EXPIRED" {
		t.Fatal(state, e)
	}
	f.exec("sale.create", "", f.order(p, 1))
	f.exec("transfer.receive", tr, Input{LocationID: f.branch})
	f.invariant()
}

func TestA4PostgresCountSaleRaceAndWarehouseFreeze(t *testing.T) {
	f, p, manager, _ := a4CountFixture(t)
	sale := f.order(p, 1)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	var saleErr, errorStart error
	var id string
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-gate
		_, saleErr = f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", f.prefix+"race-sale", "test", sale)
	}()
	go func() {
		defer wg.Done()
		<-gate
		r, e := f.svc.Execute(f.ctx, manager, "count.start", "", f.prefix+"race-count", "test", Input{LocationID: f.branch, Count: &CountInput{ProductID: p}})
		errorStart = e
		if e == nil {
			out := map[string]any{}
			_ = json.Unmarshal(r.Body, &out)
			id = out["id"].(string)
		}
	}()
	close(gate)
	wg.Wait()
	if errorStart != nil {
		t.Fatal(errorStart)
	}
	if saleErr != nil {
		assertCode(t, saleErr, "COUNT_IN_PROGRESS")
	}
	var expected, balance int64
	e := f.owner.QueryRow(f.ctx, `SELECT (SELECT sum(expected)::bigint FROM "pl-count-lines" WHERE count_id=$1),(SELECT sum(b.quantity)::bigint FROM "pl-stock-balances" b JOIN "pl-lots" l ON l.id=b.lot_id WHERE b.location_id=$2 AND l.product_id=$3 AND account='physical')`, id, f.branch, p).Scan(&expected, &balance)
	if e != nil || expected != balance || (balance != 9 && balance != 10) {
		t.Fatal(expected, balance, e)
	}
	countExec(t, f, manager, "count.cancel", id, Input{LocationID: f.branch, Reason: "Yaris testi kapandi"})
	start := Input{LocationID: f.depot, Count: &CountInput{ProductID: p}}
	id = countExec(t, f, f.warehouse, "count.start", "", start)["id"].(string)
	receipt := Input{LocationID: f.depot, SupplierID: "1", DocumentNo: f.prefix + "frozen", Reason: "test", Lines: []ReceiptLine{{p, "NEW", "2099-12-31", "1"}}}
	_, e = f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", f.prefix+"frozen-receipt", "test", receipt)
	assertCode(t, e, "COUNT_IN_PROGRESS")
	countExec(t, f, f.warehouse, "count.cancel", id, Input{LocationID: f.depot, Reason: "Depo testi kapandi"})
	f.exec("receipt.create", "", receipt)
	t.Log("race expected/balance=" + strconv.FormatInt(balance, 10))
	f.invariant()
}

func TestA4PostgresCountQuarantineSourceConservation(t *testing.T) {
	f, in, _ := a4Sale(t)
	in.Return.Quantity = "2"
	out := f.exec("return.create", "", in)
	item := out["quarantine_id"].(string)
	var manager string
	if e := f.owner.QueryRow(f.ctx, `INSERT INTO "pl-wy-users"(email,password_hash,role) VALUES($1,'$2-test','manager') RETURNING id::text`, strings.ToLower(f.prefix)+"count@invalid").Scan(&manager); e != nil {
		t.Fatal(e)
	}
	if _, e := f.owner.Exec(f.ctx, `INSERT INTO "pl-user-locations" VALUES($1,$2)`, manager, f.branch); e != nil {
		t.Fatal(e)
	}
	var product string
	if e := f.owner.QueryRow(f.ctx, `SELECT product_id::text FROM "pl-lots" WHERE id=$1`, in.Return.LotID).Scan(&product); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, _ = f.owner.Exec(f.ctx, `UPDATE "pl-counts" SET status='CANCELLED' WHERE product_id=$1 AND status='OPEN'`, product)
	})
	start := Input{LocationID: f.branch, Count: &CountInput{ProductID: product}}
	id := countExec(t, f, manager, "count.start", "", start)["id"].(string)
	final := Input{LocationID: f.branch, Reason: "Karantinada fiziksel eksik", Count: &CountInput{ProductID: product, Lines: []CountLine{{in.Return.LotID, "physical", "0"}, {in.Return.LotID, "quarantine", "1"}}}}
	countExec(t, f, manager, "count.finalize", id, final)
	d := Input{LocationID: f.branch, Reason: "Muhurlu", Decision: &DecisionInput{Action: "release", Quantity: "2", Sealed: true}}
	_, e := f.svc.Execute(f.ctx, f.pharmacist, "quarantine.decide", item, f.prefix+"lost-source", "test", d)
	assertCode(t, e, "QUARANTINE_QUANTITY_EXCEEDED")
	d.Decision.Quantity = "1"
	f.exec("quarantine.decide", item, d)
	id = countExec(t, f, manager, "count.start", "", start)["id"].(string)
	final.Count.Lines[0].Actual = "2"
	final.Count.Lines[1].Actual = "2"
	final.Reason = "Dogrulanmis lotta fazla fiziksel stok"
	countExec(t, f, manager, "count.finalize", id, final)
	var newItem string
	if e = f.owner.QueryRow(f.ctx, `SELECT id::text FROM "pl-quarantine-items" WHERE lot_id=$1 AND return_line_id IS NULL ORDER BY id DESC LIMIT 1`, in.Return.LotID).Scan(&newItem); e != nil {
		t.Fatal(e)
	}
	d.Decision.Action = "destroy"
	d.Decision.Quantity = "2"
	f.exec("quarantine.decide", newItem, d)
	f.invariant()
}
