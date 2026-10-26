package stock

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

func a4Transfer(t *testing.T) (*fixture, string, string) {
	f := a3Fixture(t)
	p := f.product("a4-transfer")
	f.receive(p, "A4-TRANSFER", 10)
	id := f.exec("transfer.create", "", Input{LocationID: f.depot, TargetID: f.branch, Items: []Item{{p, "10", "unit"}}})["id"].(string)
	f.exec("transfer.send", id, Input{LocationID: f.depot})
	var lot string
	if e := f.owner.QueryRow(f.ctx, `SELECT lot_id::text FROM "pl-transfer-lines" WHERE transfer_id=$1`, id).Scan(&lot); e != nil {
		t.Fatal(e)
	}
	t.Logf("transfer=%s lot=%s product=%s", id, lot, p)
	return f, id, lot
}

func TestA4PostgresPartialFinalTransfer(t *testing.T) {
	f, id, lot := a4Transfer(t)
	in := Input{LocationID: f.branch, Delivery: &DeliveryInput{Lines: []DeliveryLine{{lot, "4", "0", "0"}}}}
	out := f.exec("transfer.receive", id, in)
	if out["status"] != "PARTIALLY_RECEIVED" {
		t.Fatal(out)
	}
	in.Delivery.Final = true
	in.Reason = "Nihai kontrolde bir hasar bir eksik"
	in.Delivery.Lines = []DeliveryLine{{lot, "4", "1", "1"}}
	key := f.prefix + "a4-final"
	a, e := f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", id, key, "test", in)
	if e != nil {
		t.Fatal(e)
	}
	b, e := f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", id, key, "replay", in)
	if e != nil || string(a.Body) != string(b.Body) {
		t.Fatal(e)
	}
	_, e = f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", id, key+"another", "test", in)
	assertCode(t, e, "INVALID_STATE")
	var physical, quarantine, loss, transit int64
	e = f.owner.QueryRow(f.ctx, `SELECT coalesce(sum(quantity) FILTER(WHERE location_id=$1 AND account='physical'),0),coalesce(sum(quantity) FILTER(WHERE location_id=$1 AND account='quarantine'),0),coalesce(sum(quantity) FILTER(WHERE location_id=$2 AND account='loss'),0),coalesce(sum(quantity) FILTER(WHERE account=$4),0) FROM "pl-stock-balances" WHERE lot_id=$3`, f.branch, f.depot, lot, "transit:"+id).Scan(&physical, &quarantine, &loss, &transit)
	if e != nil || physical != 8 || quarantine != 1 || loss != 1 || transit != 0 {
		t.Fatal(physical, quarantine, loss, transit, e)
	}
	var item string
	if e = f.owner.QueryRow(f.ctx, `SELECT id::text FROM "pl-quarantine-items" WHERE lot_id=$1 AND location_id=$2`, lot, f.branch).Scan(&item); e != nil {
		t.Fatal(e)
	}
	f.exec("quarantine.decide", item, Input{LocationID: f.branch, Reason: "Hasar dogrulandi", Decision: &DecisionInput{Action: "destroy", Quantity: "1"}})
	f.invariant()
}

func TestA4PostgresTransferRaceBoundsAndExpiry(t *testing.T) {
	f, id, lot := a4Transfer(t)
	in := Input{LocationID: f.branch, Delivery: &DeliveryInput{Lines: []DeliveryLine{{lot, "6", "0", "0"}}}}
	gate := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			_, e := f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", id, f.prefix+"receive"+strconv.Itoa(i), "test", in)
			results <- e
		}(i)
	}
	close(gate)
	wg.Wait()
	close(results)
	success, reject := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else {
			assertCode(t, e, "TRANSFER_QUANTITY_EXCEEDED")
			reject++
		}
	}
	if success != 1 || reject != 1 {
		t.Fatal(success, reject)
	}
	in.Delivery.Final = true
	in.Delivery.Lines = []DeliveryLine{{lot, "1", "0", "0"}}
	_, e := f.svc.Execute(f.ctx, f.pharmacist, "transfer.receive", id, f.prefix+"incomplete", "test", in)
	assertCode(t, e, "FINAL_REMAINDER_REQUIRED")
	in.Delivery.Lines = []DeliveryLine{{lot, "4", "0", "0"}}
	expired := f.svc
	expired.clock = func(context.Context, Tx) (time.Time, error) { return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), nil }
	if _, e = expired.Execute(f.ctx, f.pharmacist, "transfer.receive", id, f.prefix+"expired-final", "test", in); e != nil {
		t.Fatal(e)
	}
	var q int64
	if e = f.owner.QueryRow(f.ctx, `SELECT quantity FROM "pl-stock-balances" WHERE location_id=$1 AND lot_id=$2 AND account='quarantine'`, f.branch, lot).Scan(&q); e != nil || q != 4 {
		t.Fatal(q, e)
	}
	_, e = f.svc.Execute(f.ctx, f.warehouse, "transfer.cancel", id, f.prefix+"sent-cancel", "test", Input{LocationID: f.depot, Reason: "test"})
	assertCode(t, e, "INVALID_STATE")
	f.invariant()
}
