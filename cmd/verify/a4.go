package main

import (
	"fmt"
	"net/url"
	"time"
)

func (v *runner) a4Rows(path string) []map[string]any {
	out := []map[string]any{}
	cursor := ""
	seen := map[string]bool{}
	for {
		if seen[cursor] {
			v.failed = true
			return out
		}
		seen[cursor] = true
		page := v.call("GET", "/api/v1"+path+"&limit=200&cursor="+url.QueryEscape(cursor), nil, "", 200)
		rows, _ := page["items"].([]any)
		for _, r := range rows {
			if row, ok := r.(map[string]any); ok {
				out = append(out, row)
			}
		}
		cursor = str(page, "next_cursor")
		if cursor == "" {
			return out
		}
	}
}
func findID(rows []map[string]any, id string) map[string]any {
	for _, r := range rows {
		if str(r, "id") == id {
			return r
		}
	}
	return nil
}
func (v *runner) expectA4(ok bool, message string) {
	if !ok {
		fmt.Println("FAIL A4", message)
		v.failed = true
	}
}

func (v *runner) runA4() {
	start := time.Now()
	prefix := fmt.Sprintf("VERIFY-A4-%d", start.UnixNano())
	w, wc := v.a3Account("depo")
	a, ac := v.a3Account("seed.yildiz")
	m, mc := v.a3Account("mudur")
	defer func() {
		for _, p := range []struct {
			r *runner
			c string
		}{{w, wc}, {a, ac}, {m, mc}} {
			p.r.call("POST", "/api/v1/auth/logout", nil, p.c, 204)
			if p.r.failed {
				v.failed = true
			}
		}
	}()
	product := w.postA2("/products", map[string]any{"location_id": "21", "product": map[string]any{"barcode": fmt.Sprint("00", time.Now().UnixNano()), "name": prefix, "dose": "500mg", "form": "tablet", "base_unit": "tablet", "pack_size": "27", "blister_size": "0", "sale_multiple": "1", "ingredients": []string{"a4-verify"}, "prescription_required": true, "controlled_group": "CONTROL-TAB", "pack_price": "10000"}}, wc, 201)
	p := str(product, "id")
	if p == "" {
		v.failed = true
		return
	}
	need := a.postA2("/needs", map[string]any{"location_id": "1", "reason": "A4 manual need", "need": map[string]string{"product_id": p, "quantity": "10"}}, ac, 201)
	n := str(need, "id")
	m.postA2("/needs/"+n+"/approve", map[string]string{"location_id": "1"}, mc, 201)
	order := w.postA2("/orders", map[string]any{"location_id": "21", "supplier_id": "1", "reason": "A4 consolidated order", "order": map[string]any{"lines": []any{map[string]any{"product_id": p, "quantity": "10", "needs": []string{n}}}}}, wc, 201)
	oid := str(order, "id")
	w.postA2("/orders/"+oid+"/approve", map[string]string{"location_id": "21"}, wc, 201)
	for _, r := range []struct{ tag, q, mode, want string }{{"good4", "4", "", "6"}, {"bad3", "3", "quarantine", "6"}, {"good6", "6", "", "0"}} {
		body := map[string]any{"location_id": "21", "supplier_id": "1", "order_id": oid, "document_no": prefix + "-" + r.tag, "reason": "A4 appropriate/unsuitable receipt", "lines": []any{map[string]string{"product_id": p, "lot": prefix, "expires_on": "2099-12-31", "quantity": r.q}}}
		if r.mode != "" {
			body["receipt_mode"] = r.mode
		}
		first := w.postA2("/goods-receipts", body, wc, 201)
		again := w.postA2("/goods-receipts", body, wc, 201)
		v.expectA4(str(first, "id") == str(again, "id"), "duplicate business receipt")
		row := findID(w.a4Rows("/orders?location_id=21&q="+oid), oid)
		ls, _ := row["lines"].([]any)
		if len(ls) != 1 {
			v.expectA4(false, "order lines")
			return
		}
		v.expectA4(str(ls[0].(map[string]any), "remaining") == r.want, "10/4/3bad/6 remainder "+r.want)
	}
	allocations := w.a4Rows("/allocations?location_id=21")
	var alloc map[string]any
	for _, r := range allocations {
		if str(r, "order_id") == oid {
			alloc = r
		}
	}
	aid := str(alloc, "id")
	v.expectA4(aid != "" && str(alloc, "at_warehouse") == "10", "warehouse allocation10")
	w.postA2("/transfers", map[string]any{"location_id": "21", "target_id": "2", "items": []any{map[string]string{"product_id": p, "quantity": "1", "unit": "unit"}}}, wc, 409)
	draft := w.postA2("/transfers", map[string]any{"location_id": "21", "target_id": "1", "allocation_id": aid, "items": []any{map[string]string{"product_id": p, "quantity": "10", "unit": "unit"}}}, wc, 201)
	tid := str(draft, "id")
	w.postA2("/transfers/"+tid+"/send", map[string]string{"location_id": "21"}, wc, 200)
	tr := findID(a.a4Rows("/transfers?location_id=1"), tid)
	tls, _ := tr["lines"].([]any)
	if len(tls) != 1 {
		v.expectA4(false, "transfer lot")
		return
	}
	lot := str(tls[0].(map[string]any), "lot_id")
	for _, d := range []struct {
		final              bool
		good, bad, missing string
	}{{false, "4", "0", "0"}, {true, "4", "1", "1"}} {
		a.postA2("/transfers/"+tid+"/receive", map[string]any{"location_id": "1", "reason": "A4 final variance", "delivery": map[string]any{"final": d.final, "lines": []any{map[string]string{"lot_id": lot, "good": d.good, "damaged": d.bad, "missing": d.missing}}}}, ac, 200)
	}
	// A genuine controlled sale has Q3, patient200 and institution800.
	rx := a.postA2("/prescriptions", map[string]any{"location_id": "1", "prescription": map[string]any{"external_id": prefix, "patient_external_id": prefix, "birth_date": "1990-01-01", "issued_at": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano), "lines": []any{map[string]string{"prescribed_product_id": p, "prescribed_base": "3"}}}}, ac, 201)
	rls, _ := rx["lines"].([]any)
	if len(rls) != 1 {
		v.expectA4(false, "rx line")
		return
	}
	saleBody := map[string]any{"location_id": "1", "prescription_id": str(rx, "id"), "dispense": []any{map[string]string{"prescription_line_id": str(rls[0].(map[string]any), "id"), "dispensed_product_id": p, "quantity": "3", "unit": "unit"}}}
	quote := a.postA2("/sales/quote", saleBody, ac, 200)
	saleBody["quote"] = quote["quote"]
	sale := a.postA2("/sales", saleBody, ac, 201)
	detail := a.call("GET", "/api/v1/sales/"+str(sale, "id"), nil, "", 200)
	sls, _ := detail["lines"].([]any)
	if len(sls) != 1 {
		v.expectA4(false, "sale detail")
		return
	}
	line := str(sls[0].(map[string]any), "id")
	v.controlledLine = line
	var firstQuarantine string
	for _, want := range []string{"334", "332", "334"} {
		body := map[string]any{"location_id": "1", "reason": "A4 sealed return", "return": map[string]string{"sale_id": str(sale, "id"), "sale_line_id": line, "lot_id": lot, "quantity": "1"}}
		ret := a.postA2("/returns", body, ac, 201)
		v.expectA4(str(ret, "total") == want, "refund "+want)
		key := a.key
		replay := a.call("POST", "/api/v1/returns", body, ac, 201)
		v.expectA4(key == a.key && str(ret, "id") == str(replay, "id"), "same-key return replay")
		if firstQuarantine == "" {
			firstQuarantine = str(ret, "quarantine_id")
		}
	}
	a.postA2("/quarantine/"+firstQuarantine+"/decide", map[string]any{"location_id": "1", "reason": "Sealed lot expiry checked", "decision": map[string]any{"action": "release", "quantity": "1", "sealed": true}}, ac, 201)
	count := m.postA2("/counts", map[string]any{"location_id": "1", "count": map[string]string{"product_id": p}}, mc, 201)
	cid := str(count, "id")
	defer func() {
		if cid != "" {
			m.postA2("/counts/"+cid+"/cancel", map[string]string{"location_id": "1", "reason": "Verify cleanup"}, mc, 201)
		}
	}()
	denied := a.postA2("/sales", saleBody, ac, 422)
	errBody, _ := denied["error"].(map[string]any)
	v.expectA4(str(errBody, "code") == "PRESCRIPTION_REMAINDER", "RX remains consumed after full return")
	rxAfter := findID(a.a4Rows("/prescriptions?location_id=1&q="+prefix), str(rx, "id"))
	afterLines, _ := rxAfter["lines"].([]any)
	v.expectA4(len(afterLines) == 1 && str(afterLines[0].(map[string]any), "remaining_base") == "0", "RX remainder remains0")
	for _, entry := range a.a4Rows("/controlled-ledger?location_id=1") {
		if str(entry, "sale_line_id") == line {
			returns, _ := entry["returns"].([]any)
			v.expectA4(str(entry, "quantity") == "3" && len(returns) == 3, "original controlled consumption and return references")
		}
	}
	rows := findID(m.a4Rows("/counts?location_id=1"), cid)
	cls, _ := rows["lines"].([]any)
	physical := []any{}
	for _, raw := range cls {
		l := raw.(map[string]any)
		actual := str(l, "expected")
		if str(l, "account") == "physical" {
			v.expectA4(actual == "6", "count expected6")
			actual = "4"
		}
		physical = append(physical, map[string]string{"lot_id": str(l, "lot_id"), "account": str(l, "account"), "actual": actual})
	}
	m.postA2("/counts/"+cid+"/finalize", map[string]any{"location_id": "1", "reason": "A4 count discrepancy minus2", "count": map[string]any{"product_id": p, "lines": physical}}, mc, 201)
	cid = ""
	final := findID(w.a4Rows("/allocations?location_id=21"), aid)
	v.expectA4(str(final, "expected") == "0" && str(final, "at_warehouse") == "0" && str(final, "in_transit") == "0" && str(final, "delivered") == "8" && str(final, "exceptions") == "2", "disjoint final allocation stages")
	if !w.failed && !a.failed && !m.failed && !v.failed {
		fmt.Println("PASS F-2/F-4/F-5/T-3/T-4 A4 API: order10->good4->bad3->good6 remaining6/6/0; reserved stock protected; transfer8/1/1/0; refunds334/332/334; count6->4; RX rights unchanged")
	}
	fmt.Printf("A4 fixture product=%s need=%s order=%s allocation=%s transfer=%s sale=%s duration=%s; F-6 manual procurement subcheck; suggestion check follows in A5/full mode\n", p, n, oid, aid, tid, str(sale, "id"), time.Since(start))
}
