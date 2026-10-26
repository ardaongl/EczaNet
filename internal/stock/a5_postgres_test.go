package stock

import (
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func a5Run(t *testing.T, f *fixture) string {
	t.Helper()
	var d string
	if e := f.owner.QueryRow(f.ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date::text`).Scan(&d); e != nil {
		t.Fatal(e)
	}
	out := f.exec("suggestion.calculate", "", Input{LocationID: f.depot, AsOf: d})
	t.Log("run", out)
	return out["id"].(string)
}
func TestA5SetSQLOracleAndSummary(t *testing.T) {
	f := a3Fixture(t)
	p := f.product("A5-oracle")
	if _, e := f.owner.Exec(f.ctx, `UPDATE "pl-products" SET pack_size=1,blister_size=0 WHERE id=$1`, p); e != nil {
		t.Fatal(e)
	}
	f.receive(p, "A5", 200)
	f.deliver(p, 13)
	// Controlled historical fixture in its own new product; existing sales are untouched.
	if _, e := f.owner.Exec(f.ctx, `UPDATE "pl-product-history" SET opened_on=(clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date-90 WHERE product_id=$1`, p); e != nil {
		t.Fatal(e)
	}
	if _, e := f.owner.Exec(f.ctx, `INSERT INTO "pl-suggestion-policy"(product_id,location_id,version,minimum) VALUES($1,$2,'oracle-v1',14)`, p, f.branch); e != nil {
		t.Fatal(e)
	}
	run := a5Run(t, f)
	var physical, risk, effective, suggested string
	if e := f.owner.QueryRow(f.ctx, `SELECT physical::text,risk::text,effective::text,suggested::text FROM "pl-order-suggestions" WHERE run_id=$1 AND product_id=$2 AND location_id=$3`, run, p, f.branch).Scan(&physical, &risk, &effective, &suggested); e != nil {
		t.Fatal(e)
	}
	if physical != "13" || risk != "0" || effective != "13" || suggested != "1" {
		t.Fatal(physical, risk, effective, suggested)
	}
	var n, want int64
	if e := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-order-suggestions" WHERE run_id=$1`, run).Scan(&n); e != nil {
		t.Fatal(e)
	}
	_ = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM "pl-products" WHERE active)*(SELECT count(*) FROM "pl-locations" WHERE kind='branch')`).Scan(&want)
	if n != want {
		t.Fatal(n, want)
	}
	// Independent multi-lot oracle at fractional rates, exact expiry/day boundaries, zero sales and wrong accounts.
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	var d string
	_ = tx.QueryRow(f.ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date::text`).Scan(&d)
	if _, e = tx.Exec(f.ctx, `UPDATE "pl-stock-balances" SET quantity=0 WHERE location_id=$1 AND lot_id IN(SELECT id FROM "pl-lots" WHERE product_id=$2)`, f.branch, p); e != nil {
		t.Fatal(e)
	}
	// Deferred balance triggers roll back this synthetic read-only oracle fixture at the end.
	if _, e = tx.Exec(f.ctx, `INSERT INTO "pl-daily-sales" VALUES($1,$2,$3::date-1,180,0)`, f.branch, p, d); e != nil {
		t.Fatal(e)
	}
	lots := []ReferenceLot{{1, 10, 2, true}, {2, 3, 100, true}}
	for _, l := range lots {
		var lot string
		e = tx.QueryRow(f.ctx, `INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on) VALUES($1,1,$2,$3::date+$4::int-1) RETURNING id::text`, p, fmt.Sprint(l.ID), d, l.Days).Scan(&lot)
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec(f.ctx, `INSERT INTO "pl-stock-balances" VALUES($1,$2,'physical',$3)`, f.branch, lot, l.Quantity)
		if e != nil {
			t.Fatal(e)
		}
	}
	_, _, ref, err := ReferenceStock(big.NewRat(2, 1), 7, lots)
	if err != nil {
		t.Fatal(err)
	}
	e = tx.QueryRow(f.ctx, `SELECT physical::text,risk::text,effective::text,suggested::text FROM "pl-calculate-suggestions"($1) WHERE product_id=$2 AND location_id=$3`, d, p, f.branch).Scan(&physical, &risk, &effective, &suggested)
	if e != nil || physical != "13" || risk != "6" || effective != ref.String() || suggested != "7" {
		t.Fatal(physical, risk, effective, suggested, e)
	}
}
func TestA5SummaryOriginalDayAndRebuild(t *testing.T) {
	f := a3Fixture(t)
	p := f.product("A5-sales")
	f.receive(p, "A5", 30)
	f.deliver(p, 20)
	sale := f.exec("sale.create", "", f.order(p, 10))["id"].(string)
	var line, lot string
	if e := f.owner.QueryRow(f.ctx, `SELECT l.id::text,a.lot_id::text FROM "pl-sale-lines" l JOIN "pl-sale-allocations" a ON a.line_id=l.id WHERE l.sale_id=$1`, sale).Scan(&line, &lot); e != nil {
		t.Fatal(e)
	}
	ret := Input{LocationID: f.branch, Reason: "A5 original day", Return: &ReturnInput{SaleID: sale, SaleLineID: line, LotID: lot, Quantity: "4"}}
	ret.Return.Quote = countExec(t, f, f.pharmacist, "return.quote", "", ret)["quote"].(string)
	countExec(t, f, f.pharmacist, "return.create", "", ret)
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	// Temporary original-day fixture is rolled back; no historical price invariant is changed.
	if _, e := tx.Exec(f.ctx, `UPDATE "pl-sales" SET effective_at=effective_at-interval '1 day' WHERE id=$1`, sale); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e := tx.Exec(f.ctx, `SELECT "pl-refresh-daily-sales"()`); e != nil {
			t.Fatal(e)
		}
	}
	var gross, returned string
	var days int
	e := tx.QueryRow(f.ctx, `SELECT sum(gross)::text,sum(returned)::text,count(*) FROM "pl-daily-sales" WHERE product_id=$1 AND location_id=$2 AND day=(clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date-1`, p, f.branch).Scan(&gross, &returned, &days)
	if e != nil || gross != "10" || returned != "4" || days != 1 {
		t.Fatal(gross, returned, days, e)
	}
}
func TestA5ConsumptionRaceScopeAndStale(t *testing.T) {
	f := a3Fixture(t)
	p := f.product("A5-consume")
	_, e := f.owner.Exec(f.ctx, `INSERT INTO "pl-suggestion-policy"(product_id,location_id,version,minimum) VALUES($1,1,'consume-v1',50)`, p)
	if e != nil {
		t.Fatal(e)
	}
	run := a5Run(t, f)
	var id string
	_ = f.owner.QueryRow(f.ctx, `SELECT id::text FROM "pl-order-suggestions" WHERE run_id=$1 AND product_id=$2 AND location_id=1`, run, p).Scan(&id)
	_, e = f.svc.Execute(f.ctx, "6", "suggestion.need", id, f.prefix+"wrong", "test", Input{LocationID: "2", Reason: "wrong branch"})
	assertCode(t, e, "FORBIDDEN")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.svc.Execute(f.ctx, "4", "suggestion.need", id, f.prefix+strconv.Itoa(i), "test", Input{LocationID: "1", Reason: "A5 approved suggestion"})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if IsBusiness(err) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
	page, e := f.svc.Read(f.ctx, "4", "suggestions", "1", run, "", 200)
	if e != nil || len(page.Items) == 0 {
		t.Fatal(e)
	}
	// Any real input mutation forces an explicit new run, never silent stale reconfirmation.
	f.receive(p, "changed", 1)
	_, e = f.svc.Execute(f.ctx, "4", "suggestion.need", id, f.prefix+"stale", "test", Input{LocationID: "1", Reason: "stale"})
	assertCode(t, e, "STALE_SUGGESTION")
	for _, actor := range []string{"2", "5"} {
		_, e = f.svc.Read(f.ctx, actor, "report-expiry", "1", "", "", 50)
		if e == nil {
			t.Fatal("scope", actor)
		}
	}

}

func TestA5PipelineETAAndSingleCount(t *testing.T) {
	f, p, o, aid := a4Procurement(t)
	var d string
	_ = f.owner.QueryRow(f.ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date::text`).Scan(&d)
	// Central unallocated quantity has no branch, even with an otherwise matching product.
	f.exec("order.create", "", Input{LocationID: f.depot, SupplierID: "1", Reason: "central only", Order: &OrderInput{Lines: []OrderLineInput{{p, "20", nil}}}})
	check := func(want string) {
		t.Helper()
		var got string
		e := f.owner.QueryRow(f.ctx, `SELECT pending::text FROM "pl-calculate-suggestions"($1) WHERE product_id=$2 AND location_id=$3`, d, p, f.branch).Scan(&got)
		if e != nil || got != want {
			t.Fatal("pending", got, want, e)
		}
	}
	check("0") // Unknown ETA is excluded.
	f.exec("allocation.eta", aid, Input{LocationID: f.depot, ExpectedOn: d})
	check("10")
	f.exec("receipt.create", "", orderReceipt(f, p, o, "A5four", "4", ""))
	check("10")
	f.exec("receipt.create", "", orderReceipt(f, p, o, "A5six", "6", ""))
	check("10")
	tr := f.exec("transfer.create", "", Input{LocationID: f.depot, TargetID: f.branch, AllocationID: aid, Items: []Item{{p, "10", "unit"}}})["id"].(string)
	f.exec("transfer.send", tr, Input{LocationID: f.depot})
	check("10")
	f.exec("transfer.receive", tr, Input{LocationID: f.branch})
	check("0")
	f.invariant()
}

func TestA5OracleFractionalBoundaries(t *testing.T) {
	f := a3Fixture(t)
	p := f.product("A5-rational")
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	_, e = tx.Exec(f.ctx, `UPDATE "pl-products" SET pack_size=1,blister_size=0 WHERE id=$1`, p)
	if e != nil {
		t.Fatal(e)
	}
	var d string
	_ = tx.QueryRow(f.ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date::text`).Scan(&d)
	_, e = tx.Exec(f.ctx, `INSERT INTO "pl-suggestion-policy"(product_id,location_id,version) VALUES($1,$2,'oracle')`, p, f.branch)
	if e != nil {
		t.Fatal(e)
	}
	for i, c := range []struct {
		net    int64
		days   int
		season string
		min    int64
		lots   []ReferenceLot
	}{
		{900, 90, "1.2", 0, []ReferenceLot{{1, 20, 100, true}}},
		{30, 90, "1", 0, []ReferenceLot{{1, 2, 3, true}, {2, 4, 3, true}, {3, 2, 7, true}}},
		{1, 90, "1.3", 0, []ReferenceLot{{1, 3, 1, true}, {2, 3, 7, true}, {3, 3, 8, true}}},
		{0, 0, "1", 9, []ReferenceLot{{1, 5, 1, true}}},
		{1, 1, "1", 0, []ReferenceLot{{1, 2, 1, true}, {2, 8, 7, true}}},
		{89, 89, "1", 0, []ReferenceLot{{1, 10, 2, true}, {2, 3, 100, true}}},
	} {
		_, e = tx.Exec(f.ctx, `DELETE FROM "pl-stock-balances" WHERE location_id=$1 AND lot_id IN(SELECT id FROM "pl-lots" WHERE product_id=$2); DELETE FROM "pl-daily-sales" WHERE product_id=$2; UPDATE "pl-product-history" SET opened_on=$3::date-$4::int WHERE product_id=$2; UPDATE "pl-suggestion-policy" SET season=$5,minimum=$6 WHERE product_id=$2`, pgx.QueryExecModeSimpleProtocol, f.branch, p, d, c.days, c.season, c.min)
		if e != nil {
			t.Fatal(e)
		}
		if c.net > 0 {
			_, e = tx.Exec(f.ctx, `INSERT INTO "pl-daily-sales" VALUES($1,$2,$3::date-1,$4,0)`, f.branch, p, d, c.net)
			if e != nil {
				t.Fatal(e)
			}
		}
		for _, l := range c.lots {
			var id string
			e = tx.QueryRow(f.ctx, `INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on) VALUES($1,1,$2,$3::date+$4::int-1) RETURNING id::text`, p, fmt.Sprintf("case-%d-%d", i, l.ID), d, l.Days).Scan(&id)
			if e != nil {
				t.Fatal(e)
			}
			_, e = tx.Exec(f.ctx, `INSERT INTO "pl-stock-balances" VALUES($1,$2,'physical',$3)`, f.branch, id, l.Quantity)
			if e != nil {
				t.Fatal(e)
			}
		}
		rate, _, _, e := ReferenceDemand(c.net, c.days, 7, c.season, c.min, 1, 0, 0)
		if e != nil {
			t.Fatal(e)
		}
		phy, risk, eff, e := ReferenceStock(rate, 7, c.lots)
		if e != nil {
			t.Fatal(e)
		}
		_, target, want, e := ReferenceDemand(c.net, c.days, 7, c.season, c.min, 1, eff.Int64(), 0)
		if e != nil {
			t.Fatal(e)
		}
		var got [5]string
		e = tx.QueryRow(f.ctx, `SELECT target::text,physical::text,risk::text,effective::text,suggested::text FROM "pl-calculate-suggestions"($1) WHERE product_id=$2 AND location_id=$3`, d, p, f.branch).Scan(&got[0], &got[1], &got[2], &got[3], &got[4])
		expect := [5]string{target.String(), phy.String(), risk.String(), eff.String(), want.String()}
		if e != nil || got != expect {
			t.Fatal(i, got, expect, e)
		}
		e = tx.QueryRow(f.ctx, `SELECT target::text,physical::text,risk::text,effective::text,suggested::text FROM (`+suggestionCalculationSQL+`) actual WHERE product_id=$1 AND location_id=$3`, p, d, f.branch).Scan(&got[0], &got[1], &got[2], &got[3], &got[4])
		if e != nil || got != expect {
			t.Fatal("embedded production oracle", i, got, expect, e)
		}
	}
}

func TestA5ReportsExactTotalsAndScope(t *testing.T) {
	f := a3Fixture(t)
	if e := f.owner.QueryRow(f.ctx, `INSERT INTO "pl-locations"(name,kind) VALUES($1,'branch') RETURNING id::text`, f.prefix+"reports").Scan(&f.branch); e != nil {
		t.Fatal(e)
	}
	if e := f.owner.QueryRow(f.ctx, `INSERT INTO "pl-wy-users"(email,password_hash,role) VALUES($1,'$2-test-only','pharmacist') RETURNING id::text`, strings.ToLower(f.prefix+"reports@invalid")).Scan(&f.pharmacist); e != nil {
		t.Fatal(e)
	}
	if _, e := f.owner.Exec(f.ctx, `INSERT INTO "pl-user-locations" VALUES($1,$2)`, f.pharmacist, f.branch); e != nil {
		t.Fatal(e)
	}
	p := f.product("report-money")
	f.receive(p, "normal", 20)
	f.deliver(p, 10)
	sale := f.exec("sale.create", "", f.order(p, 5))["id"].(string)
	var line, lot string
	_ = f.owner.QueryRow(f.ctx, `SELECT l.id::text,a.lot_id::text FROM "pl-sale-lines" l JOIN "pl-sale-allocations" a ON a.line_id=l.id WHERE l.sale_id=$1`, sale).Scan(&line, &lot)
	in := Input{LocationID: f.branch, Reason: "report refund", Return: &ReturnInput{SaleID: sale, SaleLineID: line, LotID: lot, Quantity: "2"}}
	in.Return.Quote = f.exec("return.quote", "", in)["quote"].(string)
	f.exec("return.create", "", in)
	reconciled, err := f.svc.Read(f.ctx, f.pharmacist, "report-reconciliation", f.branch, "", "", 200)
	if err != nil || len(reconciled.Items) != 1 {
		t.Fatal(reconciled, err)
	}
	var reconciliation struct {
		Accounts []struct {
			Difference string `json:"difference"`
		} `json:"accounts"`
	}
	_ = json.Unmarshal(reconciled.Items[0], &reconciliation)
	for _, a := range reconciliation.Accounts {
		if a.Difference != "0" {
			t.Fatal(reconciliation)
		}
	}
	page, e := f.svc.Read(f.ctx, f.pharmacist, "report-finance", f.branch, "", "", 200)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	var row map[string]any
	_ = json.Unmarshal(page.Items[0], &row)
	for k, want := range map[string]string{"gross": "500", "patient": "500", "institution": "0", "returned": "200", "net": "300", "sale_count": "1"} {
		if row[k] != want {
			t.Fatal(k, row[k], want)
		}
	}
	_, e = f.svc.Read(f.ctx, "1", "report-finance", f.branch, "", "", 50)
	assertCode(t, e, "FORBIDDEN")
	page, e = f.svc.Read(f.ctx, "5", "report-finance", "", "", "", 200)
	if e != nil || len(page.Items) == 0 {
		t.Fatal("accountant finance", e)
	}
	_, e = f.svc.Read(f.ctx, "5", "controlled-ledger", f.branch, "", "", 50)
	assertCode(t, e, "FORBIDDEN")
	// An exact near-expiry fixture, including its quarantine distinction.
	near := f.product("report-expiry")
	var d string
	_ = f.owner.QueryRow(f.ctx, `SELECT ((clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date+30)::text`).Scan(&d)
	f.exec("receipt.create", "", Input{LocationID: f.depot, SupplierID: "1", DocumentNo: f.prefix + "near", Reason: "near expiry", Lines: []ReceiptLine{{near, "NEAR", d, "10"}}})
	f.deliver(near, 8)
	page, e = f.svc.Read(f.ctx, f.pharmacist, "report-expiry", f.branch, "", "", 200)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	_ = json.Unmarshal(page.Items[0], &row)
	if row["product_id"] != near {
		t.Fatal(row)
	}
	if _, e = f.owner.Exec(f.ctx, `INSERT INTO "pl-suggestion-policy"(product_id,location_id,version,minimum) VALUES($1,$2,'critical-v1',9)`, near, f.branch); e != nil {
		t.Fatal(e)
	}
	run := a5Run(t, f)
	page, e = f.svc.Read(f.ctx, f.pharmacist, "report-critical", f.branch, run, "", 200)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	_ = json.Unmarshal(page.Items[0], &row)
	if row["product_id"] != near || row["effective"] != "8" || row["minimum"] != "9" {
		t.Fatal(row)
	}
}

func TestA5PolicyVersionsRemainImmutable(t *testing.T) {
	f := a3Fixture(t)
	p := f.product("policy-history")
	input := Input{LocationID: "1", SuggestionPolicy: &SuggestionPolicyInput{ProductID: p, Version: f.prefix + "v1", Lead: 3, Review: 2, Safety: 2, Season: "1.2", Minimum: "14"}}
	countExec(t, f, "4", "suggestion.policy", "", input)
	input.SuggestionPolicy.Version = f.prefix + "v2"
	input.SuggestionPolicy.Minimum = "15"
	countExec(t, f, "4", "suggestion.policy", "", input)
	input.SuggestionPolicy.Version = f.prefix + "v1"
	_, e := f.svc.Execute(f.ctx, "4", "suggestion.policy", "", f.prefix+"old-version", "test", input)
	assertCode(t, e, "POLICY_VERSION_CONFLICT")
	input.SuggestionPolicy.Version = f.prefix + "v3"
	input.SuggestionPolicy.Season = "0.00001"
	_, e = f.svc.Execute(f.ctx, "4", "suggestion.policy", "", f.prefix+"precision", "test", input)
	assertCode(t, e, "INVALID_INPUT")
	input.SuggestionPolicy.Season = "1"
	_, e = f.svc.Execute(f.ctx, "2", "suggestion.policy", "", f.prefix+"role", "test", input)
	assertCode(t, e, "FORBIDDEN")
	var versions int
	if e = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-suggestion-policy-history" WHERE product_id=$1`, p).Scan(&versions); e != nil || versions != 2 {
		t.Fatal(versions, e)
	}
}
