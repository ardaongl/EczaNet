package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestA4RefundCumulative(t *testing.T) {
	for n, want := range []int64{334, 332, 334} {
		p, e := RefundDelta(200, 3, int64(n), 1)
		if e != nil {
			t.Fatal(e)
		}
		i, e := RefundDelta(800, 3, int64(n), 1)
		if e != nil || p+i != want {
			t.Fatal(n, p, i, e)
		}
	}
	p, e := RefundDelta(math.MaxInt64, math.MaxInt64, 0, math.MaxInt64)
	if e != nil || p != math.MaxInt64 {
		t.Fatal(p, e)
	}
	for _, v := range [][4]int64{{-1, 3, 0, 1}, {100, 0, 0, 1}, {100, 3, -1, 1}, {100, 3, 3, 1}, {100, 3, 2, 2}} {
		if _, e := RefundDelta(v[0], v[1], v[2], v[3]); e == nil {
			t.Fatal(v)
		}
	}
}

func a4Sale(t *testing.T) (*fixture, Input, string) {
	f := a3Fixture(t)
	f.prefix = "A4-" + fmt.Sprint(time.Now().UnixNano()) + "-"
	product := Product{Barcode: "00" + fmt.Sprint(time.Now().UnixNano()), Name: f.prefix + "return", Dose: "500mg", Form: "tablet", BaseUnit: "tablet", PackSize: "27", BlisterSize: "0", SaleMultiple: "1", Ingredients: []string{"a4"}, PrescriptionRequired: true, ControlledGroup: "CONTROL-TAB", PackPrice: "10000"}
	p := f.exec("product.create", "", Input{LocationID: f.depot, Product: &product})["id"].(string)
	f.receive(p, "A4", 10)
	f.deliver(p, 3)
	rx, line := f.rx(p, f.prefix+"patient", 3)
	sale := f.exec("sale.create", "", f.rxOrder(rx, line, p, "3", f.branch, f.pharmacist))["id"].(string)
	var saleLine, lot string
	if e := f.owner.QueryRow(f.ctx, `SELECT l.id::text,a.lot_id::text FROM "pl-sale-lines" l JOIN "pl-sale-allocations" a ON a.line_id=l.id WHERE l.sale_id=$1`, sale).Scan(&saleLine, &lot); e != nil {
		t.Fatal(e)
	}
	in := Input{LocationID: f.branch, Reason: "Muhurlu fiziksel iade", Return: &ReturnInput{SaleID: sale, SaleLineID: saleLine, LotID: lot, Quantity: "1"}}
	t.Logf("sale=%s sale_line=%s lot=%s product=%s rx=%s", sale, saleLine, lot, p, rx)
	return f, in, p
}

func a4Rights(f *fixture, sale string) string {
	var out string
	e := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object('rx',to_jsonb(r),'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM "pl-prescription-lines" l WHERE prescription_id=r.id),'daily',(SELECT jsonb_agg(to_jsonb(c) ORDER BY business_day,group_name) FROM "pl-daily-controls" c WHERE c.patient_id=r.patient_id),'ledger',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM "pl-controlled-ledger" c WHERE c.prescription_id=r.id))::text FROM "pl-prescriptions" r JOIN "pl-sales" s ON s.prescription_id=r.id WHERE s.id=$1`, sale).Scan(&out)
	if e != nil {
		f.t.Fatal(e)
	}
	return out
}

func TestA4PostgresReturnSharesQuarantineAndRights(t *testing.T) {
	f, in, p := a4Sale(t)
	before := a4Rights(f, in.Return.SaleID)
	// A future publication changes the current price table but never the sale snapshot.
	var accountant string
	if e := f.owner.QueryRow(f.ctx, `SELECT id::text FROM "pl-wy-users" WHERE role='accountant' LIMIT 1`).Scan(&accountant); e != nil {
		t.Fatal(e)
	}
	_, e := f.svc.Execute(f.ctx, accountant, "price.create", "", f.prefix+"price", "test", Input{LocationID: f.branch, Price: &PriceInput{ProductID: p, ValidFrom: time.Now().UTC().Add(time.Second), PackPrice: "270000"}})
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(1100 * time.Millisecond) // Publication is now effective; historical refund stays unchanged.
	for n, want := range []string{"334", "332", "334"} {
		quote := f.exec("return.quote", "", in)
		if quote["total"] != want {
			t.Fatal(quote)
		}
		key := f.prefix + strconv.Itoa(n)
		a, e := f.svc.Execute(f.ctx, f.pharmacist, "return.create", "", key, "test", in)
		if e != nil {
			t.Fatal(e)
		}
		b, e := f.svc.Execute(f.ctx, f.pharmacist, "return.create", "", key, "replay", in)
		if e != nil || a.Status != b.Status || string(a.Body) != string(b.Body) {
			t.Fatal(a, b, e)
		}
		var result map[string]string
		_ = json.Unmarshal(a.Body, &result)
		if result["total"] != want {
			t.Fatal(result)
		}
		t.Logf("return=%s quarantine=%s refund=%s", result["id"], result["quarantine_id"], result["total"])
	}
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "return.create", "", f.prefix+"over", "test", in)
	assertCode(t, e, "RETURN_QUANTITY_EXCEEDED")
	if a4Rights(f, in.Return.SaleID) != before {
		t.Fatal("return reopened RX/daily rights or rewrote original ledger")
	}
	var patient, institution, controlled int64
	e = f.owner.QueryRow(f.ctx, `SELECT sum(l.patient_total)::bigint,sum(l.institution_total)::bigint,(SELECT sum(c.quantity)::bigint FROM "pl-controlled-returns" c JOIN "pl-return-lines" rl ON rl.id=c.return_line_id WHERE rl.sale_id=$1) FROM "pl-return-lines" l WHERE sale_id=$1`, in.Return.SaleID).Scan(&patient, &institution, &controlled)
	if e != nil || patient != 200 || institution != 800 || controlled != 3 {
		t.Fatal(patient, institution, controlled, e)
	}
	var physical, quarantine int64
	e = f.owner.QueryRow(f.ctx, `SELECT coalesce(sum(quantity) FILTER(WHERE account='physical'),0),coalesce(sum(quantity) FILTER(WHERE account='quarantine'),0) FROM "pl-stock-balances" WHERE location_id=$1 AND lot_id=$2`, in.LocationID, in.Return.LotID).Scan(&physical, &quarantine)
	if e != nil || physical != 0 || quarantine != 3 {
		t.Fatal(physical, quarantine, e)
	}
	// No sellable units even with a new prescription.
	rx, line := f.rx(p, f.prefix+"second-patient", 1)
	attempt := f.rxOrder(rx, line, p, "1", f.branch, f.pharmacist)
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "sale.create", "", f.prefix+"quarantine-sale", "test", attempt)
	assertCode(t, e, "INSUFFICIENT_STOCK")
	f.invariant()
}

func TestA4PostgresParallelReturnAndRollback(t *testing.T) {
	f, in, _ := a4Sale(t)
	in.Return.Quantity = "2"
	gate := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			_, e := f.svc.Execute(f.ctx, f.pharmacist, "return.create", "", f.prefix+"race"+strconv.Itoa(i), "test", in)
			results <- e
		}(i)
	}
	close(gate)
	wg.Wait()
	close(results)
	success, rejected := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else {
			assertCode(t, e, "RETURN_QUANTITY_EXCEEDED")
			rejected++
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatal(success, rejected)
	}
	in.Return.Quantity = "1"
	var before string
	query := `SELECT jsonb_build_object('balances',(SELECT jsonb_agg(to_jsonb(b) ORDER BY account) FROM "pl-stock-balances" b WHERE location_id=$1 AND lot_id=$2),'returns',(SELECT count(*) FROM "pl-return-lines" WHERE sale_line_id=$3),'items',(SELECT count(*) FROM "pl-quarantine-items" WHERE location_id=$1 AND lot_id=$2),'controls',(SELECT count(*) FROM "pl-controlled-returns" c JOIN "pl-return-lines" l ON l.id=c.return_line_id WHERE l.sale_line_id=$3),'events',(SELECT count(*) FROM "pl-stock-entries" WHERE location_id=$1 AND lot_id=$2))::text`
	if e := f.owner.QueryRow(f.ctx, query, in.LocationID, in.Return.LotID, in.Return.SaleLineID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	_, e := f.svc.once(f.ctx, f.pharmacist, "return.create", "", f.prefix+"rollback", "test", in, func() error { return errors.New("fault before commit") })
	if e == nil {
		t.Fatal("expected rollback")
	}
	var after string
	if e = f.owner.QueryRow(f.ctx, query, in.LocationID, in.Return.LotID, in.Return.SaleLineID).Scan(&after); e != nil || before != after {
		t.Fatal(before, after, e)
	}
	var keys int
	if e = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-idempotency" WHERE key=$1`, f.prefix+"rollback").Scan(&keys); e != nil || keys != 0 {
		t.Fatal(keys, e)
	}
	f.exec("return.create", "", in)
	f.invariant()
}

func TestA4PostgresQuarantineDecisionAndAuthorization(t *testing.T) {
	f, in, _ := a4Sale(t)
	out := f.exec("return.create", "", in)
	item := out["quarantine_id"].(string)
	d := Input{LocationID: f.branch, Reason: "Muhurlu lot/SKT kontrol edildi", Decision: &DecisionInput{Action: "release", Quantity: "1", Sealed: true}}
	_, e := f.svc.Execute(f.ctx, "6", "return.create", "", f.prefix+"foreign", "test", in)
	assertCode(t, e, "FORBIDDEN")
	foreignReturn := in
	foreignReturn.LocationID = "2"
	_, e = f.svc.Execute(f.ctx, "6", "return.create", "", f.prefix+"foreign-object", "test", foreignReturn)
	assertCode(t, e, "FORBIDDEN")
	_, e = f.svc.SaleDetail(f.ctx, "6", in.Return.SaleID)
	assertCode(t, e, "FORBIDDEN")
	_, e = f.svc.Read(f.ctx, "6", "controlled-ledger", f.branch, "", "", 200)
	assertCode(t, e, "FORBIDDEN")
	var transferID string
	if e = f.owner.QueryRow(f.ctx, `SELECT t.id::text FROM "pl-transfers" t JOIN "pl-transfer-lines" l ON l.transfer_id=t.id WHERE t.target_id=$1 AND l.lot_id=$2 ORDER BY t.id DESC LIMIT 1`, f.branch, in.Return.LotID).Scan(&transferID); e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.Execute(f.ctx, "6", "transfer.receive", transferID, f.prefix+"foreign-transfer", "test", Input{LocationID: "2"})
	assertCode(t, e, "FORBIDDEN")
	_, e = f.svc.Execute(f.ctx, "2", "return.create", "", f.prefix+"technician", "test", in)
	assertCode(t, e, "FORBIDDEN")
	// Any branch/warehouse source is checked before replay as well.
	foreign := d
	foreign.LocationID = "2"
	_, e = f.svc.Execute(f.ctx, "6", "quarantine.decide", item, f.prefix+"foreign-decision", "test", foreign)
	assertCode(t, e, "FORBIDDEN")
	d.Decision.Sealed = false
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "quarantine.decide", item, f.prefix+"seal", "test", d)
	assertCode(t, e, "SEAL_REQUIRED")
	d.Decision.Sealed = true
	expired := f.svc
	expired.clock = func(context.Context, Tx) (time.Time, error) { return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), nil }
	_, e = expired.Execute(f.ctx, f.pharmacist, "quarantine.decide", item, f.prefix+"expired", "test", d)
	assertCode(t, e, "EXPIRED_LOT")
	key := f.prefix + "release"
	a, e := f.svc.Execute(f.ctx, f.pharmacist, "quarantine.decide", item, key, "test", d)
	if e != nil {
		t.Fatal(e)
	}
	b, e := f.svc.Execute(f.ctx, f.pharmacist, "quarantine.decide", item, key, "replay", d)
	if e != nil || string(a.Body) != string(b.Body) {
		t.Fatal(e)
	}
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "quarantine.decide", item, f.prefix+"twice", "test", d)
	assertCode(t, e, "QUARANTINE_QUANTITY_EXCEEDED")
	out = f.exec("return.create", "", in)
	item = out["quarantine_id"].(string)
	d.Decision.Action = "destroy"
	f.exec("quarantine.decide", item, d)
	var physical, destroyed int64
	e = f.owner.QueryRow(f.ctx, `SELECT coalesce(sum(quantity) FILTER(WHERE account='physical'),0),coalesce(sum(quantity) FILTER(WHERE account='destroyed'),0) FROM "pl-stock-balances" WHERE location_id=$1 AND lot_id=$2`, f.branch, in.Return.LotID).Scan(&physical, &destroyed)
	if e != nil || physical != 1 || destroyed != 1 {
		t.Fatal(physical, destroyed, e)
	}
	if _, e = f.runtime.Exec(f.ctx, `UPDATE "pl-sale-lines" SET total=1 WHERE id=$1`, in.Return.SaleLineID); e == nil {
		t.Fatal("sale mutation permitted")
	}
	if _, e = f.runtime.Exec(f.ctx, `DELETE FROM "pl-return-lines" WHERE sale_line_id=$1`, in.Return.SaleLineID); e == nil {
		t.Fatal("return deletion permitted")
	}
	f.invariant()
}
