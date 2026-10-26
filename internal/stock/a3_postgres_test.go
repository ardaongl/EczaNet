package stock

import (
	"context"
	"eczanet/tests/dbtest"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Retained, unique A3 fixtures exercise an accumulated main database and fresh DB alike.
// Only the fixture's newly created products/patients/RX are ever changed by tests.
func a3Fixture(t *testing.T) *fixture {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUNTIME_URL") == "" {
		t.Skip("real PostgreSQL required")
	}
	dbtest.Acquire(t, false)
	ctx := context.Background()
	owner, e := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := pgxpool.New(ctx, os.Getenv("TEST_RUNTIME_URL"))
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{t: t, ctx: ctx, owner: owner, runtime: runtime, svc: Service{Pool: runtime}, warehouse: "3", pharmacist: "1", depot: "21", branch: "1", prefix: "A3-" + fmt.Sprint(time.Now().UnixNano()) + "-"}
	t.Cleanup(func() { runtime.Close(); owner.Close() })
	return f
}
func (f *fixture) a3product(pack, dose, group string, stock ...int) string {
	p := Product{Barcode: "00" + fmt.Sprint(time.Now().UnixNano()), Name: f.prefix + pack + group, Dose: dose, Form: "tablet", BaseUnit: "tablet", PackSize: pack, BlisterSize: "0", SaleMultiple: "1", Ingredients: []string{"a3-demo"}, PrescriptionRequired: true, ControlledGroup: group, PackPrice: "20000"}
	id := f.exec("product.create", "", Input{LocationID: f.depot, Product: &p})["id"].(string)
	f.receive(id, "A3", 200)
	qty := 70
	if len(stock) > 0 {
		qty = stock[0]
	}
	f.deliver(id, qty)
	other := *f
	other.branch = "2"
	other.pharmacist = "6"
	other.deliver(id, qty)
	return id
}
func (f *fixture) rx(product, patient string, q int) (string, string) {
	p := PrescriptionInput{ExternalID: f.prefix + fmt.Sprint(time.Now().UnixNano()), PatientID: patient, BirthDate: "1990-01-01", IssuedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond), Lines: []PrescriptionLineInput{{product, strconv.Itoa(q)}}}
	out := f.exec("prescription.create", "", Input{LocationID: f.branch, Prescription: &p})
	id := out["id"].(string)
	line := out["lines"].([]any)[0].(map[string]any)["id"].(string)
	f.t.Logf("RX=%s line=%s patient=%s product=%s", id, line, patient, product)
	return id, line
}
func (f *fixture) rxOrder(rx, line, product, qty, loc, actor string) Input {
	in := Input{LocationID: loc, PrescriptionID: rx, Dispense: []DispenseItem{{line, product, qty, "unit"}}}
	r, e := f.svc.Execute(f.ctx, actor, "sale.quote", "", "", "a3-test", in)
	if e != nil {
		f.t.Fatal("quote", e)
	}
	var out map[string]any
	_ = json.Unmarshal(r.Body, &out)
	in.Quote = out["quote"].(string)
	return in
}
func assertCode(t *testing.T, e error, code string) {
	t.Helper()
	var b *Error
	if !errors.As(e, &b) || b.Code != code {
		t.Fatalf("want %s got %v", code, e)
	}
}
func (f *fixture) rxSnapshot(rx string) string {
	var result string
	e := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object('stock',(SELECT jsonb_agg(to_jsonb(b) ORDER BY b.location_id,b.lot_id,b.account) FROM "pl-stock-balances" b JOIN "pl-lots" lot ON lot.id=b.lot_id JOIN "pl-products" p ON p.id=lot.product_id WHERE p.name LIKE $2),'stock_entries',(SELECT count(*) FROM "pl-stock-entries" e JOIN "pl-lots" lot ON lot.id=e.lot_id JOIN "pl-products" p ON p.id=lot.product_id WHERE p.name LIKE $2),'audit',(SELECT count(*) FROM "pl-audit-events" WHERE new_value->'input'->>'prescription_id'=$1::text),'pending',(SELECT count(*) FROM "pl-idempotency" WHERE key LIKE $2),'owner',r.owner_id,'fulfilled',(SELECT jsonb_agg(fulfilled_base ORDER BY id) FROM "pl-prescription-lines" WHERE prescription_id=r.id),'sales',(SELECT count(*) FROM "pl-sales" WHERE prescription_id=r.id),'dispensations',(SELECT count(*) FROM "pl-dispensations" d JOIN "pl-prescription-lines" l ON l.id=d.prescription_line_id WHERE l.prescription_id=r.id),'ledger',(SELECT count(*) FROM "pl-controlled-ledger" WHERE prescription_id=r.id),'daily',(SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY group_name,business_day),'[]') FROM "pl-daily-controls" c WHERE patient_id=r.patient_id))::text FROM "pl-prescriptions" r WHERE r.id=$1::bigint`, rx, f.prefix+"%").Scan(&result)
	if e != nil {
		f.t.Fatal(e)
	}
	return result
}

func TestA3PostgresGlobalIdentityAndImport(t *testing.T) {
	f := a3Fixture(t)
	p := f.a3product("20", "500mg", "")
	in := Input{LocationID: "1", Prescription: &PrescriptionInput{ExternalID: f.prefix + "identity", PatientID: f.prefix + "H1", BirthDate: "1990-01-01", IssuedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond), Lines: []PrescriptionLineInput{{p, "30"}}}}
	a := f.exec("prescription.create", "", in)
	in.LocationID = "2"
	r, e := f.svc.Execute(f.ctx, "6", "prescription.create", "", f.prefix+"a3-other-branch-key", "test", in)
	if e != nil {
		t.Fatal(e)
	}
	var b map[string]any
	_ = json.Unmarshal(r.Body, &b)
	if a["id"] != b["id"] || a["owner_id"] != nil {
		t.Fatal("duplicate or premature ownership", a, b)
	}
	in.Prescription.Lines[0].Quantity = "31"
	_, e = f.svc.Execute(f.ctx, "6", "prescription.create", "", f.prefix+"a3-conflict-key", "test", in)
	assertCode(t, e, "PRESCRIPTION_CONFLICT")
	in.Prescription.ExternalID += "future"
	in.Prescription.IssuedAt = time.Now().Add(time.Hour)
	_, e = f.svc.Execute(f.ctx, "6", "prescription.create", "", f.prefix+"a3-future-key", "test", in)
	assertCode(t, e, "INVALID_INPUT")
}
func TestA3PostgresPartialEquivalentAndAtomicRollback(t *testing.T) {
	f := a3Fixture(t)
	p := f.a3product("30", "500mg", "")
	q := f.a3product("20", "0.5g", "")
	rx, line := f.rx(p, f.prefix+"H1", 30)
	in := f.rxOrder(rx, line, q, "10", "1", "1")
	sale := f.exec("sale.create", "", in)
	if sale["total"] != "9000" || sale["patient_total"] != "1800" {
		t.Fatal(sale)
	}
	in = f.rxOrder(rx, line, q, "15", "1", "1")
	f.exec("sale.create", "", in)
	var remaining int64
	if e := f.owner.QueryRow(f.ctx, `SELECT prescribed_base-fulfilled_base FROM "pl-prescription-lines" WHERE id=$1`, line).Scan(&remaining); e != nil || remaining != 5 {
		t.Fatal(remaining, e)
	}
	before := f.rxSnapshot(rx)
	bad := Input{LocationID: "2", PrescriptionID: rx, Dispense: []DispenseItem{{line, q, "1", "unit"}}}
	_, e := f.svc.Execute(f.ctx, "6", "sale.quote", "", "", "test", bad)
	assertCode(t, e, "PRESCRIPTION_BRANCH")
	bad.LocationID = "1"
	bad.Dispense[0].Quantity = "6"
	_, e = f.svc.Execute(f.ctx, "1", "sale.create", "", "a3-too-many-key", "test", bad)
	assertCode(t, e, "PRESCRIPTION_REMAINDER")
	if before != f.rxSnapshot(rx) {
		t.Fatal("rejected request changed RX")
	}
	in = f.rxOrder(rx, line, q, "5", "1", "1")
	f.exec("sale.create", "", in)
	_, e = f.svc.Execute(f.ctx, "1", "sale.create", "", "a3-complete-key", "test", in)
	assertCode(t, e, "PRESCRIPTION_REMAINDER")
	f.invariant()
	t.Log("partial30 ->10 ->15 ->remaining5 ->5 ->0; equivalent pack20 consumed base30")
}
func TestA3PostgresTwoBranchOwnershipRace(t *testing.T) {
	f := a3Fixture(t)
	p := f.a3product("30", "500mg", "")
	rx, line := f.rx(p, f.prefix+"H1", 30)
	a := f.rxOrder(rx, line, p, "10", "1", "1")
	b := f.rxOrder(rx, line, p, "10", "2", "6")
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, forbidden := 0, 0
	for i, in := range []Input{a, b} {
		wg.Add(1)
		go func(i int, in Input) {
			defer wg.Done()
			<-start
			actor := []string{"1", "6"}[i]
			_, e := f.svc.Execute(f.ctx, actor, "sale.create", "", fmt.Sprintf("%s-race-%d", f.prefix, i), "test", in)
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				success++
			} else {
				var x *Error
				if errors.As(e, &x) && x.Code == "PRESCRIPTION_BRANCH" {
					forbidden++
				} else {
					t.Error(e)
				}
			}
		}(i, in)
	}
	close(start)
	wg.Wait()
	if success != 1 || forbidden != 1 {
		t.Fatal(success, forbidden)
	}
	var owner string
	var fulfilled, sales int
	_ = f.owner.QueryRow(f.ctx, `SELECT owner_id::text,(SELECT sum(fulfilled_base) FROM "pl-prescription-lines" WHERE prescription_id=$1),(SELECT count(*) FROM "pl-sales" WHERE prescription_id=$1) FROM "pl-prescriptions" WHERE id=$1`, rx).Scan(&owner, &fulfilled, &sales)
	if fulfilled != 10 || sales != 1 {
		t.Fatal(owner, fulfilled, sales)
	}
	f.invariant()
	t.Logf("ownership race success=%d forbidden=%d owner=%s fulfilled=%d sales=%d", success, forbidden, owner, fulfilled, sales)
}
func TestA3PostgresPatientDailyRaceAndGroupedBasket(t *testing.T) {
	f := a3Fixture(t)
	p := f.a3product("20", "500mg", "CONTROL-TAB")
	patient := f.prefix + "H1"
	r1, l1 := f.rx(p, patient, 30)
	r2, l2 := f.rx(p, patient, 30)
	a := f.rxOrder(r1, l1, p, "6", "1", "1")
	b := f.rxOrder(r2, l2, p, "6", "2", "6")
	start := make(chan struct{})
	var wg sync.WaitGroup
	var count atomic.Int32
	var rejected atomic.Int32
	for i, in := range []Input{a, b} {
		wg.Add(1)
		go func(i int, in Input) {
			defer wg.Done()
			<-start
			_, e := f.svc.Execute(f.ctx, []string{"1", "6"}[i], "sale.create", "", fmt.Sprintf("%s-control-%d", f.prefix, i), "test", in)
			if e == nil {
				count.Add(1)
			} else {
				var x *Error
				if errors.As(e, &x) && x.Code == "DAILY_LIMIT" {
					rejected.Add(1)
				} else {
					t.Error(e)
				}
			}
		}(i, in)
	}
	close(start)
	wg.Wait()
	if count.Load() != 1 || rejected.Load() != 1 {
		t.Fatal(count.Load(), rejected.Load())
	}
	var used, ledger int
	_ = f.owner.QueryRow(f.ctx, `SELECT (SELECT sum(consumed) FROM "pl-daily-controls" WHERE patient_id=p.id),(SELECT sum(quantity) FROM "pl-controlled-ledger" WHERE patient_id=p.id) FROM "pl-patients" p WHERE external_id=$1`, patient).Scan(&used, &ledger)
	if used != 6 || ledger != 6 {
		t.Fatal(used, ledger)
	}
	equivalentProduct := f.a3product("30", "0.5g", "CONTROL-TAB")
	fresh, line := f.rx(p, f.prefix+"H2", 30)
	in := Input{LocationID: "1", PrescriptionID: fresh, Dispense: []DispenseItem{{line, p, "6", "unit"}, {line, equivalentProduct, "5", "unit"}}}
	before := f.rxSnapshot(fresh)
	_, e := f.svc.Execute(f.ctx, "1", "sale.quote", "", "", "test", in)
	assertCode(t, e, "DAILY_LIMIT")
	if before != f.rxSnapshot(fresh) {
		t.Fatal("quote wrote counters")
	}
	t.Logf("two RX/two branches: success=%d limit_reject=%d consumed=%d ledger=%d; basket6+5 rejected", count.Load(), rejected.Load(), used, ledger)
}
func TestA3PostgresRollbackAllEffectsAndReplay(t *testing.T) {
	f := a3Fixture(t)
	p := f.a3product("20", "500mg", "CONTROL-TAB")
	rx, line := f.rx(p, f.prefix+"H1", 30)
	in := f.rxOrder(rx, line, p, "6", "1", "1")
	before := f.rxSnapshot(rx)
	_, e := f.svc.once(f.ctx, "1", "sale.create", "", f.prefix+"inject", "test", in, func() error { return errors.New("injected before commit") })
	if e == nil {
		t.Fatal("injection missing")
	}
	if before != f.rxSnapshot(rx) {
		t.Fatal("rollback effects", before, f.rxSnapshot(rx))
	}
	bad := in
	bad.Quote = "stale"
	_, e = f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"stale", "test", bad)
	assertCode(t, e, "PRICE_CHANGED")
	if before != f.rxSnapshot(rx) {
		t.Fatal("price rejection effects")
	}
	a, e := f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"samekey", "test", in)
	if e != nil {
		t.Fatal(e)
	}
	b, e := f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"samekey", "test", in)
	if e != nil || string(a.Body) != string(b.Body) {
		t.Fatal("replay", e)
	}
	var n int
	_ = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-sales" WHERE prescription_id=$1`, rx).Scan(&n)
	if n != 1 {
		t.Fatal("double sale", n)
	}
	f.invariant()
	t.Log("before-commit fault rollback; PRICE_CHANGED rollback; replay same response and one sale", string(a.Body))
}
func TestA3PostgresInsufficientStockAndEquivalenceRejection(t *testing.T) {
	f := a3Fixture(t)
	p := f.a3product("20", "500mg", "")
	wrong := f.a3product("20", "250mg", "")
	rx, line := f.rx(p, f.prefix+"H1", 100)
	in := Input{LocationID: "1", PrescriptionID: rx, Dispense: []DispenseItem{{line, wrong, "2", "unit"}}}
	_, e := f.svc.Execute(f.ctx, "1", "sale.quote", "", "", "test", in)
	assertCode(t, e, "NOT_EQUIVALENT")
	in = f.rxOrder(rx, line, p, "71", "1", "1")
	before := f.rxSnapshot(rx)
	_, e = f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"stock", "test", in)
	assertCode(t, e, "INSUFFICIENT_STOCK")
	if before != f.rxSnapshot(rx) {
		t.Fatal("stock rejection effects")
	}
	normal := Input{LocationID: "1", Items: []Item{{p, "1", "unit"}}}
	_, e = f.svc.Execute(f.ctx, "1", "sale.quote", "", "", "test", normal)
	assertCode(t, e, "PRODUCT_NOT_ELIGIBLE")
	f.invariant()
}

func TestA3PostgresPricePeriodsSnapshotAndAccess(t *testing.T) {
	f := a3Fixture(t)
	p := f.a3product("1", "500mg", "")
	rx, line := f.rx(p, f.prefix+"H1", 5)
	in := f.rxOrder(rx, line, p, "1", "1", "1")
	first := f.exec("sale.create", "", in)
	boundary := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	price := Input{LocationID: "1", Price: &PriceInput{ProductID: p, ValidFrom: boundary, PackPrice: "30000"}}
	if _, e := f.svc.Execute(f.ctx, "1", "price.create", "", f.prefix+"unauthorized", "test", price); e == nil {
		t.Fatal("pharmacist changed price")
	}
	if _, e := f.svc.Execute(f.ctx, "5", "price.create", "", f.prefix+"future", "test", price); e != nil {
		t.Fatal(e)
	}
	old := f.svc
	f.svc.clock = func(context.Context, Tx) (time.Time, error) { return boundary.Add(-time.Microsecond), nil }
	before := f.rxOrder(rx, line, p, "1", "1", "1")
	if before.Quote != in.Quote {
		t.Fatal("price changed early")
	}
	f.svc.clock = func(context.Context, Tx) (time.Time, error) { return boundary, nil }
	snapshot := f.rxSnapshot(rx)
	_, e := f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"price-stale", "test", before)
	assertCode(t, e, "PRICE_CHANGED")
	if snapshot != f.rxSnapshot(rx) {
		t.Fatal("price rollback")
	}
	next := f.rxOrder(rx, line, p, "1", "1", "1")
	second := f.exec("sale.create", "", next)
	if first["total"] != "17600" || first["patient_total"] != "3520" || first["institution_total"] != "14080" || second["total"] != "25800" {
		t.Fatal(first, second)
	}
	var frozen string
	if e = f.owner.QueryRow(f.ctx, `SELECT pack_price::text FROM "pl-sale-lines" WHERE sale_id=$1`, first["id"]).Scan(&frozen); e != nil || frozen != "20000" {
		t.Fatal("old snapshot changed", frozen, e)
	}
	_, e = f.owner.Exec(f.ctx, `INSERT INTO "pl-price-periods"(product_id,valid_from,pack_price) VALUES($1,$2,1)`, p, boundary)
	if e == nil {
		t.Fatal("overlap accepted")
	}
	_, e = f.runtime.Exec(f.ctx, `UPDATE "pl-sale-lines" SET snapshot='{}' WHERE sale_id=$1`, first["id"])
	if e == nil {
		t.Fatal("runtime edited snapshot")
	}
	normal := f.rxOrder(rx, line, p, "1", "1", "1")
	_, e = f.svc.Execute(f.ctx, "2", "sale.create", "", f.prefix+"technician", "test", normal)
	assertCode(t, e, "FORBIDDEN")
	if _, e = f.svc.Read(f.ctx, "2", "controlled-ledger", "1", "", "", 10); e == nil {
		t.Fatal("technician read ledger")
	}
	if _, e = f.svc.Read(f.ctx, "5", "prescriptions", "1", "", "", 10); e == nil {
		t.Fatal("accountant read patient")
	}
	f.svc = old
	f.invariant()
	t.Log("price interval before/at:20000/30000; snapshots17600/25800; pharmacist/technician/accountant restrictions")
}

// A held real guard forces the sale to wait. The private clock changes only after
// candidate-day acquisition, and the final read occurs after the guard is released.
func TestA3PostgresEffectiveAtAfterWaitAndDayRetry(t *testing.T) {
	for _, scenario := range []string{"expiry", "price", "midnight", "skt"} {
		t.Run(scenario, func(t *testing.T) {
			f := a3Fixture(t)
			p := f.a3product("20", "500mg", "CONTROL-TAB")
			rx, line := f.rx(p, f.prefix+"H1", 30)
			var issued time.Time
			if e := f.owner.QueryRow(f.ctx, `SELECT issued_at FROM "pl-prescriptions" WHERE id=$1`, rx).Scan(&issued); e != nil {
				t.Fatal(e)
			}
			after := issued.Add(168 * time.Hour)
			if scenario != "expiry" {
				day := time.Now().In(istanbul)
				after = time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, istanbul)
			}
			before := after.Add(-time.Microsecond)
			if scenario == "price" {
				after = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
				before = after.Add(-time.Microsecond)
				_, e := f.svc.Execute(f.ctx, "5", "price.create", "", f.prefix+"boundary", "test", Input{LocationID: "1", Price: &PriceInput{ProductID: p, ValidFrom: after, PackPrice: "30000"}})
				if e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "skt" {
				// A separate newly created lot expires on candidate day; existing immutable lots stay untouched.
				// Shift the far-future clock to just after 2099-12-31 while RX is imported now;
				// expiry is extended only by a new RX fixture issued at a valid historical time below.
				f.exec("receipt.create", "", Input{LocationID: "21", SupplierID: "1", DocumentNo: f.prefix + "today", Reason: "clock SKT fixture", Lines: []ReceiptLine{{p, "TODAY", businessDay(before), "10"}}})
				f.deliver(p, 10) // FEFO sends the TODAY lot before the existing 2099 lot.
			}
			var ticks atomic.Int32
			var shifted atomic.Bool
			candidate := make(chan struct{}, 1)
			f.svc.clock = func(context.Context, Tx) (time.Time, error) {
				ticks.Add(1)
				if shifted.Load() {
					return after, nil
				}
				select {
				case candidate <- struct{}{}:
				default:
				}
				return before, nil
			}
			in := f.rxOrder(rx, line, p, "1", "1", "1")
			// Drain quote's candidate notification before waiting for the real command.
			select {
			case <-candidate:
			default:
			}
			holder, e := f.owner.Begin(f.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer holder.Rollback(f.ctx)
			if _, e = holder.Exec(f.ctx, `SELECT 1 FROM "pl-inventory-guards" WHERE location_id=1 AND product_id=$1 FOR UPDATE`, p); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			var response Result
			go func() {
				var e error
				response, e = f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"wait", "test", in)
				done <- e
			}()
			select {
			case <-candidate:
			case <-time.After(2 * time.Second):
				t.Fatal("candidate clock not reached")
			}
			// DB evidence, rather than a sleep, confirms an actual guard lock wait.
			var holderPID int
			if e = holder.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(time.Second)
			waiting := false
			for time.Now().Before(deadline) {
				var n int
				if e = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid))`, holderPID).Scan(&n); e != nil {
					t.Fatal(e)
				}
				if n > 0 {
					waiting = true
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !waiting {
				t.Fatal("sale did not wait on real guard")
			}
			shifted.Store(true)
			if e = holder.Commit(f.ctx); e != nil {
				t.Fatal(e)
			}
			e = <-done
			if scenario == "expiry" {
				assertCode(t, e, "PRESCRIPTION_EXPIRED")
			} else if scenario == "price" {
				assertCode(t, e, "PRICE_CHANGED")
			} else {
				if e != nil {
					t.Fatal(e)
				}
				var out map[string]any
				_ = json.Unmarshal(response.Body, &out)
				var day string
				var effective time.Time
				if e = f.owner.QueryRow(f.ctx, `SELECT business_day::text,effective_at FROM "pl-controlled-ledger" WHERE prescription_id=$1`, rx).Scan(&day, &effective); e != nil || day != businessDay(after) || !effective.Equal(after) {
					t.Fatal(day, effective, e)
				}
				if scenario == "skt" {
					var todayAlloc int
					_ = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-sale-allocations" a JOIN "pl-lots" l ON l.id=a.lot_id JOIN "pl-sale-lines" s ON s.id=a.line_id WHERE s.sale_id=$1 AND l.number='TODAY'`, out["id"]).Scan(&todayAlloc)
					if todayAlloc != 0 {
						t.Fatal("expired lot selected")
					}
				}
				if ticks.Load() < 6 {
					t.Fatal("midnight did not retry", ticks.Load())
				}
			}
			f.invariant()
			t.Logf("scenario=%s actual_guard_wait=true clock_reads=%d final=%s result=%v", scenario, ticks.Load(), after.Format(time.RFC3339Nano), e)
		})
	}
}

func TestA3HistoricalClockIsMigratorOnlyAndRetryBounded(t *testing.T) {
	f := a3Fixture(t)
	if _, e := HistoricalSeedService(f.ctx, f.runtime, time.Now().Add(-time.Hour)); e == nil {
		t.Fatal("runtime can backdate")
	}
	if _, e := HistoricalSeedService(f.ctx, f.owner, time.Now().Add(time.Hour)); e == nil {
		t.Fatal("future seed accepted")
	}
	if _, e := HistoricalSeedService(f.ctx, f.owner, time.Now().Add(-time.Hour)); e != nil {
		t.Fatal(e)
	}
	p := f.a3product("20", "500mg", "CONTROL-TAB")
	rx, line := f.rx(p, f.prefix+"H1", 30)
	in := f.rxOrder(rx, line, p, "1", "1", "1")
	before := f.rxSnapshot(rx)
	var calls atomic.Int32
	now := time.Now()
	f.svc.clock = func(context.Context, Tx) (time.Time, error) {
		n := calls.Add(1)
		if n%2 == 1 {
			return now, nil
		}
		return now.Add(24 * time.Hour), nil
	}
	_, e := f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"retry-limit", "test", in)
	assertCode(t, e, "UNAVAILABLE")
	if calls.Load() != 6 || f.rxSnapshot(rx) != before {
		t.Fatal("retry budget or rollback", calls.Load())
	}
	t.Log("day retry bounded to3 complete transactions/6clock reads, all effects rolled back; runtime historical adapter403")
}
func TestA3PostgresMissingPriceAndControlGroupBypass(t *testing.T) {
	f := a3Fixture(t)
	p := f.a3product("20", "500mg", "CONTROL-TAB", 3)
	q := f.a3product("20", "500mg", "")
	rx, line := f.rx(p, f.prefix+"H1", 30)
	in := Input{LocationID: "1", PrescriptionID: rx, Dispense: []DispenseItem{{line, q, "6", "unit"}}}
	before := f.rxSnapshot(rx)
	_, e := f.svc.Execute(f.ctx, "1", "sale.quote", "", "", "test", in)
	assertCode(t, e, "NOT_EQUIVALENT")
	stockOrder := f.rxOrder(rx, line, p, "6", "1", "1")
	_, e = f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"stock-rollback", "test", stockOrder)
	assertCode(t, e, "INSUFFICIENT_STOCK")
	if f.rxSnapshot(rx) != before {
		t.Fatal("controlled stock rejection effects")
	}
	if _, e = f.owner.Exec(f.ctx, `DELETE FROM "pl-price-periods" WHERE product_id=$1`, p); e != nil {
		t.Fatal(e)
	}
	in.Dispense[0].ProductID = p
	_, e = f.svc.Execute(f.ctx, "1", "sale.create", "", f.prefix+"price-missing", "test", in)
	assertCode(t, e, "PRICE_MISSING")
	if f.rxSnapshot(rx) != before {
		t.Fatal("missing price changed ownership/counters")
	}
	f.invariant()
}
