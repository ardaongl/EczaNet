package stock

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
)

func a4Procurement(t *testing.T) (*fixture, string, string, string) {
	f := a3Fixture(t)
	p := f.product("procurement")
	n := countExec(t, f, f.pharmacist, "need.create", "", Input{LocationID: f.branch, Reason: "Manuel sube ihtiyaci", Need: &NeedInput{p, "10"}})["id"].(string)
	countExec(t, f, "4", "need.approve", n, Input{LocationID: f.branch})
	o := f.exec("order.create", "", Input{LocationID: f.depot, SupplierID: "1", Reason: "Onayli ihtiyac", Order: &OrderInput{Lines: []OrderLineInput{{p, "10", []string{n}}}}})["id"].(string)
	var aid string
	if e := f.owner.QueryRow(f.ctx, `SELECT a.id::text FROM "pl-order-allocations" a JOIN "pl-order-lines" l ON l.id=a.line_id WHERE l.order_id=$1`, o).Scan(&aid); e != nil {
		t.Fatal(e)
	}
	f.exec("order.approve", o, Input{LocationID: f.depot})
	t.Logf("product=%s need=%s order=%s allocation=%s", p, n, o, aid)
	return f, p, o, aid
}
func orderReceipt(f *fixture, p, o, tag, q, mode string) Input {
	return Input{LocationID: f.depot, SupplierID: "1", DocumentNo: f.prefix + tag, Reason: "Gerekceli kabul", OrderID: o, ReceiptMode: mode, Lines: []ReceiptLine{{p, "ORDER-" + tag, "2099-12-31", q}}}
}
func pipeline(t *testing.T, f *fixture, aid string, want [5]int64) {
	t.Helper()
	var got [5]int64
	e := f.owner.QueryRow(f.ctx, `SELECT expected,at_warehouse::bigint,in_transit::bigint,delivered::bigint,exceptions::bigint FROM "pl-procurement-pipeline" WHERE allocation_id=$1`, aid).Scan(&got[0], &got[1], &got[2], &got[3], &got[4])
	if e != nil || got != want {
		t.Fatal("pipeline", got, want, e)
	}
}

func TestA4ProcurementJourneyAndAllocation(t *testing.T) {
	f, p, o, aid := a4Procurement(t)
	pipeline(t, f, aid, [5]int64{10, 0, 0, 0, 0})
	for _, r := range []struct {
		tag, q, mode string
		want         [5]int64
	}{{"GOOD4", "4", "", [5]int64{6, 4, 0, 0, 0}}, {"BAD3", "3", "quarantine", [5]int64{6, 4, 0, 0, 0}}, {"GOOD6", "6", "", [5]int64{0, 10, 0, 0, 0}}} {
		in := orderReceipt(f, p, o, r.tag, r.q, r.mode)
		a := f.exec("receipt.create", "", in)
		b := f.exec("receipt.create", "", in)
		if a["id"] != b["id"] {
			t.Fatal("document duplicate", a, b)
		}
		pipeline(t, f, aid, r.want)
	}
	free := Input{LocationID: f.depot, TargetID: "2", Items: []Item{{p, "1", "unit"}}}
	_, e := f.svc.Execute(f.ctx, f.warehouse, "transfer.create", "", f.prefix+"steal", "test", free)
	assertCode(t, e, "INSUFFICIENT_STOCK")
	tr := f.exec("transfer.create", "", Input{LocationID: f.depot, TargetID: f.branch, AllocationID: aid, Items: []Item{{p, "10", "unit"}}})["id"].(string)
	f.exec("transfer.send", tr, Input{LocationID: f.depot})
	pipeline(t, f, aid, [5]int64{0, 0, 10, 0, 0})
	f.exec("transfer.receive", tr, Input{LocationID: f.branch})
	pipeline(t, f, aid, [5]int64{0, 0, 0, 10, 0})
	for _, resource := range []string{"needs", "orders", "allocations"} {
		page, e := f.svc.Read(f.ctx, f.warehouse, resource, f.depot, "", "", 200)
		if e != nil || len(page.Items) == 0 {
			t.Fatal(resource, e)
		}
		t.Log(resource, "page", len(page.Items))
	}
	f.invariant()
}

func TestA4ProcurementRolesRemainderAndRollback(t *testing.T) {
	f, p, o, aid := a4Procurement(t)
	in := orderReceipt(f, p, o, "OVER", "11", "")
	_, e := f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", f.prefix+"over", "test", in)
	assertCode(t, e, "ORDER_QUANTITY_EXCEEDED")
	pipeline(t, f, aid, [5]int64{10, 0, 0, 0, 0})
	in = orderReceipt(f, p, o, "FAULT", "4", "")
	_, e = f.svc.once(f.ctx, f.warehouse, "receipt.create", "", f.prefix+"fault", "test", in, func() error { return errors.New("rollback injection") })
	if e == nil {
		t.Fatal("expected failure")
	}
	pipeline(t, f, aid, [5]int64{10, 0, 0, 0, 0})
	var n int
	if e = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-goods-receipts" WHERE document_no=$1`, in.DocumentNo).Scan(&n); e != nil || n != 0 {
		t.Fatal("receipt rollback", n, e)
	}
	for _, actor := range []string{f.pharmacist, "2", "4", "5"} {
		_, e = f.svc.Execute(f.ctx, actor, "order.approve", o, f.prefix+"role"+actor, "test", Input{LocationID: f.branch})
		assertCode(t, e, "FORBIDDEN")
	}
	need := countExec(t, f, f.pharmacist, "need.create", "", Input{LocationID: f.branch, Reason: "Onaysiz", Need: &NeedInput{p, "2"}})["id"].(string)
	_, e = f.svc.Execute(f.ctx, f.warehouse, "order.create", "", f.prefix+"draft-need", "test", Input{LocationID: f.depot, SupplierID: "1", Reason: "test", Order: &OrderInput{Lines: []OrderLineInput{{p, "2", []string{need}}}}})
	assertCode(t, e, "NEED_UNAVAILABLE")
	_, e = f.svc.Execute(f.ctx, "4", "need.approve", need, f.prefix+"foreign-need", "test", Input{LocationID: "2"})
	assertCode(t, e, "FORBIDDEN")
	countExec(t, f, "4", "need.approve", need, Input{LocationID: f.branch})
	_, e = f.svc.Execute(f.ctx, f.warehouse, "order.create", "", f.prefix+"over-allocation", "test", Input{LocationID: f.depot, SupplierID: "1", Reason: "test", Order: &OrderInput{Lines: []OrderLineInput{{p, "1", []string{need}}}}})
	assertCode(t, e, "ALLOCATION_EXCEEDED")
	f.exec("receipt.create", "", orderReceipt(f, p, o, "PARTIAL", "4", ""))
	f.exec("order.cancel", o, Input{LocationID: f.depot, Reason: "Acik kalan6 iptal"})
	pipeline(t, f, aid, [5]int64{0, 4, 0, 0, 0})
	in = orderReceipt(f, p, o, "AFTERCANCEL", "1", "")
	_, e = f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", f.prefix+"aftercancel", "test", in)
	assertCode(t, e, "INVALID_STATE")
	f.invariant()
}

func TestA4ProcurementReceiptCancelAndReceiptRaces(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		f, p, o, aid := a4Procurement(t)
		gate := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-gate
				cmd, obj, in := "receipt.create", "", orderReceipt(f, p, o, "RACE"+strconv.Itoa(i), "6", "")
				if cancel {
					in = orderReceipt(f, p, o, "RACE"+strconv.Itoa(i), "10", "")
				}
				if cancel && i == 1 {
					cmd, obj, in = "order.cancel", o, Input{LocationID: f.depot, Reason: "Iptal yarisi"}
				}
				_, e := f.svc.Execute(f.ctx, f.warehouse, cmd, obj, f.prefix+"race"+strconv.Itoa(i), "test", in)
				results <- e
			}(i)
		}
		close(gate)
		wg.Wait()
		close(results)
		success := 0
		for e := range results {
			if e == nil {
				success++
			} else if cancel {
				assertCode(t, e, "INVALID_STATE")
			} else {
				assertCode(t, e, "ORDER_QUANTITY_EXCEEDED")
			}
		}
		if success != 1 {
			t.Fatal("race", success)
		}
		if !cancel {
			pipeline(t, f, aid, [5]int64{4, 6, 0, 0, 0})
		}
		f.invariant()
	}
	f, p, o, aid := a4Procurement(t)
	receipt := orderReceipt(f, p, o, "COUNT-RECEIPT", "4", "")
	gate := make(chan struct{})
	var wg sync.WaitGroup
	var countID string
	var receiptErr, startErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-gate
		_, receiptErr = f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", f.prefix+"receipt-count-race", "test", receipt)
	}()
	go func() {
		defer wg.Done()
		<-gate
		r, e := f.svc.Execute(f.ctx, f.warehouse, "count.start", "", f.prefix+"count-receipt-race", "test", Input{LocationID: f.depot, Count: &CountInput{ProductID: p}})
		startErr = e
		if e == nil {
			var v map[string]any
			_ = json.Unmarshal(r.Body, &v)
			countID = v["id"].(string)
		}
	}()
	close(gate)
	wg.Wait()
	if startErr != nil {
		t.Fatal(startErr)
	}
	if receiptErr != nil {
		assertCode(t, receiptErr, "COUNT_IN_PROGRESS")
	}
	t.Cleanup(func() {
		_, _ = f.owner.Exec(f.ctx, `UPDATE "pl-counts" SET status='CANCELLED' WHERE id=$1 AND status='OPEN'`, countID)
	})
	var expected, physical int64
	e := f.owner.QueryRow(f.ctx, `SELECT (SELECT coalesce(sum(expected),0)::bigint FROM "pl-count-lines" WHERE count_id=$1 AND account='physical'),(SELECT coalesce(sum(b.quantity),0)::bigint FROM "pl-stock-balances" b JOIN "pl-lots" l ON l.id=b.lot_id WHERE b.location_id=$2 AND l.product_id=$3 AND account='physical')`, countID, f.depot, p).Scan(&expected, &physical)
	if e != nil || expected != physical || (physical != 0 && physical != 4) {
		t.Fatal("count/receipt snapshot race", expected, physical, e)
	}
	f.exec("count.cancel", countID, Input{LocationID: f.depot, Reason: "Kabul yarisi bitti"})
	if receiptErr != nil {
		f.exec("receipt.create", "", receipt)
	}
	same := orderReceipt(f, p, o, "SAME-DOC-RACE", "2", "")
	gate = make(chan struct{})
	results := make(chan Result, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			r, e := f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", f.prefix+"same-doc-race"+strconv.Itoa(i), "test", same)
			results <- r
			failures <- e
		}(i)
	}
	close(gate)
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first string
	for r := range results {
		var v map[string]any
		_ = json.Unmarshal(r.Body, &v)
		if first != "" && first != v["id"] {
			t.Fatal("same invoice generated twice")
		}
		first = v["id"].(string)
	}
	pipeline(t, f, aid, [5]int64{4, 6, 0, 0, 0})
	f.invariant()
}

func TestA4ProcurementReservedRaceCountAndDeterminism(t *testing.T) {
	f, p, o, aid := a4Procurement(t)
	f.exec("receipt.create", "", orderReceipt(f, p, o, "RESERVED", "4", ""))
	tr := f.exec("transfer.create", "", Input{LocationID: f.depot, TargetID: f.branch, AllocationID: aid, Items: []Item{{p, "4", "unit"}}})["id"].(string)
	gate := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-gate
		_, e := f.svc.Execute(f.ctx, f.warehouse, "transfer.send", tr, f.prefix+"reserved-send", "test", Input{LocationID: f.depot})
		results <- e
	}()
	go func() {
		defer wg.Done()
		<-gate
		_, e := f.svc.Execute(f.ctx, f.warehouse, "transfer.create", "", f.prefix+"free-send", "test", Input{LocationID: f.depot, TargetID: "2", Items: []Item{{p, "1", "unit"}}})
		results <- e
	}()
	close(gate)
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else {
			assertCode(t, e, "INSUFFICIENT_STOCK")
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	pipeline(t, f, aid, [5]int64{6, 0, 4, 0, 0})
	f.exec("receipt.create", "", orderReceipt(f, p, o, "RESERVED2", "6", ""))
	var lot string
	if e := f.owner.QueryRow(f.ctx, `SELECT lot_id::text FROM "pl-allocation-lots" WHERE allocation_id=$1 AND quantity>shipped ORDER BY lot_id LIMIT 1`, aid).Scan(&lot); e != nil {
		t.Fatal(e)
	}
	count := f.exec("count.start", "", Input{LocationID: f.depot, Count: &CountInput{ProductID: p}})["id"].(string)
	t.Cleanup(func() {
		_, _ = f.owner.Exec(f.ctx, `UPDATE "pl-counts" SET status='CANCELLED' WHERE id=$1 AND status='OPEN'`, count)
	})
	rows, e := f.owner.Query(f.ctx, `SELECT lot_id::text,account,expected::text FROM "pl-count-lines" WHERE count_id=$1 ORDER BY lot_id,account`, count)
	if e != nil {
		t.Fatal(e)
	}
	lines := []CountLine{}
	for rows.Next() {
		var l CountLine
		if e = rows.Scan(&l.LotID, &l.Account, &l.Actual); e != nil {
			t.Fatal(e)
		}
		if l.LotID == lot && l.Account == "physical" {
			l.Actual = "0"
		}
		lines = append(lines, l)
	}
	rows.Close()
	_, e = f.svc.Execute(f.ctx, f.warehouse, "count.finalize", count, f.prefix+"reserved-count", "test", Input{LocationID: f.depot, Reason: "Sayim fiziksel eksik", Count: &CountInput{ProductID: p, Lines: lines}})
	assertCode(t, e, "ALLOCATION_PROTECTED")
	f.exec("count.cancel", count, Input{LocationID: f.depot, Reason: "Tahsis korunumu testi kapandi"})
	// A receipt with a new key but identical business document is still a single effect.
	in := orderReceipt(f, p, o, "RESERVED2", "6", "")
	in.Reason = "farkli icerik"
	_, e = f.svc.Execute(f.ctx, f.warehouse, "receipt.create", "", f.prefix+"conflict", "test", in)
	assertCode(t, e, "DOCUMENT_CONFLICT")
	f.invariant()
}

func TestA4ProcurementDeterministicSharesDuplicateNeedAndScope(t *testing.T) {
	f, p, o, aid := a4Procurement(t)
	// A second order may not consume the same approved need under a different key.
	var need string
	if e := f.owner.QueryRow(f.ctx, `SELECT need_id::text FROM "pl-order-allocations" WHERE id=$1`, aid).Scan(&need); e != nil {
		t.Fatal(e)
	}
	_, e := f.svc.Execute(f.ctx, f.warehouse, "order.create", "", f.prefix+"duplicate-need", "test", Input{LocationID: f.depot, SupplierID: "1", Reason: "test", Order: &OrderInput{Lines: []OrderLineInput{{p, "10", []string{need}}}}})
	assertCode(t, e, "NEED_UNAVAILABLE")
	// Branch2 cannot read branch1's order/allocation projections.
	for _, resource := range []string{"orders", "allocations", "needs"} {
		page, e := f.svc.Read(f.ctx, "6", resource, "2", "", "", 200)
		if e != nil {
			t.Fatal(e)
		}
		for {
			foreign := o
			if resource == "needs" {
				foreign = need
			}
			if resource == "allocations" {
				foreign = aid
			}
			for _, row := range page.Items {
				var v map[string]any
				if json.Unmarshal(row, &v) != nil {
					t.Fatal("invalid result")
				}
				if v["id"] == foreign {
					t.Fatal("foreign branch document leaked", resource, foreign)
				}
			}
			if page.Next == "" {
				break
			}
			page, e = f.svc.Read(f.ctx, "6", resource, "2", "", page.Next, 200)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	f.exec("order.cancel", o, Input{LocationID: f.depot, Reason: "Yeni iki subeli fixture"})
	var manager2 string
	if e = f.owner.QueryRow(f.ctx, `INSERT INTO "pl-wy-users"(email,password_hash,role) VALUES($1,'$2-test','manager') RETURNING id::text`, "manager"+p+"@invalid").Scan(&manager2); e != nil {
		t.Fatal(e)
	}
	if _, e = f.owner.Exec(f.ctx, `INSERT INTO "pl-user-locations" VALUES($1,2)`, manager2); e != nil {
		t.Fatal(e)
	}
	n1 := countExec(t, f, "4", "need.create", "", Input{LocationID: f.branch, Reason: "Birinci sube", Need: &NeedInput{p, "6"}})["id"].(string)
	n2 := countExec(t, f, manager2, "need.create", "", Input{LocationID: "2", Reason: "Ikinci sube", Need: &NeedInput{p, "4"}})["id"].(string)
	countExec(t, f, "4", "need.approve", n1, Input{LocationID: f.branch})
	countExec(t, f, manager2, "need.approve", n2, Input{LocationID: "2"})
	o = f.exec("order.create", "", Input{LocationID: f.depot, SupplierID: "1", Reason: "Iki subeye deterministik tahsis", Order: &OrderInput{Lines: []OrderLineInput{{p, "10", []string{n2, n1}}}}})["id"].(string)
	f.exec("order.approve", o, Input{LocationID: f.depot})
	f.exec("receipt.create", "", orderReceipt(f, p, o, "FIRST4", "4", ""))
	var r1, r2 int64
	query := `SELECT coalesce(sum(a.received) FILTER(WHERE a.location_id=1),0),coalesce(sum(a.received) FILTER(WHERE a.location_id=2),0) FROM "pl-order-allocations" a JOIN "pl-order-lines" l ON l.id=a.line_id WHERE l.order_id=$1`
	if e = f.owner.QueryRow(f.ctx, query, o).Scan(&r1, &r2); e != nil || r1 != 4 || r2 != 0 {
		t.Fatal(r1, r2, e)
	}
	f.exec("receipt.create", "", orderReceipt(f, p, o, "NEXT6", "6", ""))
	if e = f.owner.QueryRow(f.ctx, query, o).Scan(&r1, &r2); e != nil || r1 != 6 || r2 != 4 {
		t.Fatal(r1, r2, e)
	}
	central := f.exec("order.create", "", Input{LocationID: f.depot, SupplierID: "1", Reason: "Merkezin tahsissiz20 ihtiyaci", Order: &OrderInput{Lines: []OrderLineInput{{p, "20", nil}}}})["id"].(string)
	f.exec("order.approve", central, Input{LocationID: f.depot})
	var centralExpected, branches int64
	e = f.owner.QueryRow(f.ctx, `SELECT coalesce(sum(expected) FILTER(WHERE branch_id IS NULL),0),coalesce(sum(expected) FILTER(WHERE branch_id IS NOT NULL),0) FROM "pl-procurement-pipeline" WHERE order_id=$1`, central).Scan(&centralExpected, &branches)
	if e != nil || centralExpected != 20 || branches != 0 {
		t.Fatal("central supply duplicated to branches", centralExpected, branches, e)
	}
	f.exec("order.cancel", central, Input{LocationID: f.depot, Reason: "Merkez bekleyen iptal"})
	if e = f.owner.QueryRow(f.ctx, `SELECT expected FROM "pl-procurement-pipeline" WHERE order_id=$1 AND branch_id IS NULL`, central).Scan(&centralExpected); e != nil || centralExpected != 0 {
		t.Fatal("cancelled central expected", centralExpected, e)
	}
	for _, q := range []string{`UPDATE "pl-order-receipt-lines" SET accepted=0 WHERE false`, `DELETE FROM "pl-transfer-order-links" WHERE false`, `UPDATE "pl-order-lines" SET quantity=1 WHERE false`, `UPDATE "pl-order-allocations" SET quantity=1 WHERE false`} {
		if _, e = f.runtime.Exec(f.ctx, q); e == nil {
			t.Fatal("runtime historical rewrite permitted", q)
		}
	}
	f.invariant()
}
