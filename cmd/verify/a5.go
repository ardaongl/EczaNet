package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func (v *runner) runA5() {
	prefix := fmt.Sprintf("VERIFY-A5-%d", time.Now().UnixNano())
	w, wc := v.a3Account("depo")
	m, mc := v.a3Account("mudur")
	defer func() {
		v.failed = v.failed || w.failed || m.failed
		w.call("POST", "/api/v1/auth/logout", nil, wc, 204)
		m.call("POST", "/api/v1/auth/logout", nil, mc, 204)
	}()
	p := w.postA2("/products", map[string]any{"location_id": "21", "product": map[string]any{"barcode": fmt.Sprint(time.Now().UnixNano()), "name": prefix, "dose": "5mg", "form": "tablet", "base_unit": "tablet", "pack_size": "1", "blister_size": "0", "sale_multiple": "1", "ingredients": []string{"a5-verify"}, "pack_price": "100"}}, wc, 201)
	product := str(p, "id")
	m.postA2("/suggestion-policies", map[string]any{"location_id": "1", "suggestion_policy": map[string]any{"product_id": product, "version": prefix, "lead_days": 3, "review_days": 2, "safety_days": 2, "season": "1", "minimum": "14"}}, mc, 201)
	d := time.Now().In(time.FixedZone("Istanbul", 3*60*60)).Format("2006-01-02")
	run := w.postA2("/suggestion-runs", map[string]any{"location_id": "21", "as_of": d}, wc, 201)
	rows := m.a4Rows("/suggestions?location_id=1&q=" + str(run, "id"))
	var selected map[string]any
	for _, r := range rows {
		if str(r, "product_id") == product {
			selected = r
		}
	}
	if selected == nil || str(selected, "target") != "14" || str(selected, "suggested") != "14" {
		v.failed = true
		fmt.Println("FAIL F-6 suggestion fixture")
		return
	}
	id := str(selected, "id")
	need := m.postA2("/suggestions/"+id+"/need", map[string]string{"location_id": "1", "reason": "A5 explicit approval"}, mc, 201)
	m.postA2("/suggestions/"+id+"/need", map[string]string{"location_id": "1", "reason": "A5 explicit approval"}, mc, 409)
	m.postA2("/needs/"+str(need, "id")+"/approve", map[string]string{"location_id": "1"}, mc, 201)
	order := w.postA2("/orders", map[string]any{"location_id": "21", "supplier_id": "1", "reason": "A5 suggestion source", "order": map[string]any{"lines": []map[string]any{{"product_id": product, "quantity": "14", "needs": []string{str(need, "id")}}}}}, wc, 201)
	w.postA2("/orders/"+str(order, "id")+"/approve", map[string]string{"location_id": "21"}, wc, 201)
	// Shipment expectation is explicit. Unknown ETA must not reduce demand.
	var allocation string
	for _, r := range w.a4Rows("/allocations?location_id=21") {
		if str(r, "order_id") == str(order, "id") {
			allocation = str(r, "id")
		}
	}
	w.postA2("/allocations/"+allocation+"/eta", map[string]string{"location_id": "21", "expected_on": d}, wc, 201)
	run = w.postA2("/suggestion-runs", map[string]string{"location_id": "21", "as_of": d}, wc, 201)
	for _, r := range m.a4Rows("/suggestions?location_id=1&q=" + str(run, "id")) {
		if str(r, "product_id") == product && (str(r, "pending") != "14" || str(r, "suggested") != "0") {
			v.failed = true
		}
	}
	w.postA2("/orders/"+str(order, "id")+"/cancel", map[string]string{"location_id": "21", "reason": "A5 remaining cancellation"}, wc, 201)
	v.reportFixtures(w, wc, m, mc, product, prefix, d)
	for _, resource := range []string{"expiry", "critical", "transfer-differences", "finance", "reconciliation"} {
		m.call("GET", "/api/v1/reports/"+resource+"?location_id=1", nil, "", 200)
	}
	if !w.failed && !m.failed && !v.failed {
		fmt.Println("PASS F-6 suggestion -> need approval -> supplier order; pending once; cancellation")
		fmt.Println("PASS F-7 exact expiry/critical/finance/controlled API fixtures; rational oracle is separately tested")
	}
	if v.full {
		return
	}
	benchmark := os.Getenv("A5_BENCHMARK_API_URL")
	if benchmark == "" {
		fmt.Println("EKSIK T-5 full 5000x20 benchmark API required (A5_BENCHMARK_API_URL)")
		v.failed = true
		return
	}
	b := *v
	b.base = benchmark
	b.failed = false
	bw, bc := b.a3Account("depo")
	for iteration := 1; iteration <= 6; iteration++ {
		start := time.Now()
		r := bw.postA2("/suggestion-runs", map[string]string{"location_id": "21", "as_of": "2026-10-25"}, bc, 201)
		duration := time.Since(start).Seconds() * 1000
		elapsed, ok := r["calculation_ms"].(float64)
		if str(r, "rows") != "100000" || !ok || !bw.hasCommit || elapsed+bw.commitMS >= 5000 {
			fmt.Println("FAIL T-5 iteration", iteration, r)
			v.failed = true
		} else {
			fmt.Printf("PASS T-5 iteration=%d rows=100000 calculation_ms=%.3f commit_ms=%.3f calculation_commit_ms=%.3f preparation_ms=%v API_total_ms=%.3f\n", iteration, elapsed, bw.commitMS, elapsed+bw.commitMS, r["preparation_ms"], duration)
		}
	}
	v.failed = v.failed || bw.failed

	bw.call("POST", "/api/v1/auth/logout", nil, bc, 204)
}

// Report acceptance compares actual API data to new, precisely known documents.
func (v *runner) reportFixtures(w *runner, wc string, m *runner, mc, product, prefix, day string) {
	critical := false
	for _, r := range m.a4Rows("/reports/critical?location_id=1") {
		if str(r, "product_id") == product && str(r, "minimum") == "14" && str(r, "physical") == "0" {
			critical = true
		}
	}
	v.expectA4(critical, "F-7 exact critical product minimum14 physical0")
	a, ac := v.a3Account("seed.yildiz")
	defer func() { a.call("POST", "/api/v1/auth/logout", nil, ac, 204); v.failed = v.failed || a.failed }()
	expiry := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	w.postA2("/goods-receipts", map[string]any{"location_id": "21", "supplier_id": "1", "document_no": prefix + "-report", "reason": "Exact report fixture", "lines": []any{map[string]string{"product_id": product, "lot": prefix, "expires_on": expiry, "quantity": "3"}}}, wc, 201)
	draft := w.postA2("/transfers", map[string]any{"location_id": "21", "target_id": "1", "items": []any{map[string]string{"product_id": product, "quantity": "3", "unit": "unit"}}}, wc, 201)
	tid := str(draft, "id")
	w.postA2("/transfers/"+tid+"/send", map[string]string{"location_id": "21"}, wc, 200)
	a.postA2("/transfers/"+tid+"/receive", map[string]string{"location_id": "1"}, ac, 200)
	found := false
	for _, r := range m.a4Rows("/reports/expiry?location_id=1") {
		if str(r, "product_id") == product {
			for _, raw := range r["balances"].([]any) {
				b := raw.(map[string]any)
				if str(b, "location_id") == "1" && str(b, "account") == "physical" && str(b, "quantity") == "3" {
					found = true
				}
			}
		}
	}
	v.expectA4(found, "F-7 exact expiry fixture physical3")
	sum := func() int64 {
		var total int64
		for _, r := range m.a4Rows("/reports/finance?location_id=1") {
			if strings.HasPrefix(str(r, "day"), day) {
				n, e := strconv.ParseInt(str(r, "gross"), 10, 64)
				if e != nil {
					v.failed = true
				}
				total += n
			}
		}
		return total
	}
	before := sum()
	body := map[string]any{"location_id": "1", "items": []any{map[string]string{"product_id": product, "quantity": "1", "unit": "unit"}}}
	quote := a.postA2("/sales/quote", body, ac, 200)
	body["quote"] = quote["quote"]
	sale := a.postA2("/sales", body, ac, 201)
	v.expectA4(str(sale, "total") == "100" && sum()-before == 100, "F-7 finance API delta100 exact kurus")
	ledger := false
	for _, r := range a.a4Rows("/controlled-ledger?location_id=1") {
		if str(r, "sale_line_id") == v.controlledLine && v.controlledLine != "" {
			rs, _ := r["returns"].([]any)
			ledger = str(r, "quantity") == "3" && len(rs) == 3
		}
	}
	v.expectA4(ledger, "F-7 exact original controlled3 and three return references")
}
