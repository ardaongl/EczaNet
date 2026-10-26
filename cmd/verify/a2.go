package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"
)

func (v *runner) loginA2(account string) string {
	prep := token(v.call("GET", "/api/v1/auth/csrf", nil, "", 200))
	return token(v.call("POST", "/api/v1/auth/login", map[string]string{"email": account + "@ddo-case.invalid", "password": v.password}, prep, 200))
}
func (v *runner) postA2(path string, body any, csrf string, status int) map[string]any {
	v.key = fmt.Sprintf("verify-%d", time.Now().UnixNano())
	return v.call("POST", "/api/v1"+path, body, csrf, status)
}
func (v *runner) runA2() {
	started := time.Now()
	csrf := v.loginA2("depo")
	fixture := fmt.Sprint(time.Now().UnixNano())
	p := v.postA2("/products", map[string]any{"location_id": "21", "product": map[string]any{"barcode": "00" + fixture, "name": "VERIFY-A2-" + fixture, "dose": "5mg", "form": "tablet", "base_unit": "tablet", "pack_size": "10", "blister_size": "5", "sale_multiple": "1", "ingredients": []string{"verify-demo"}, "pack_price": "1000"}}, csrf, 201)
	id, _ := p["id"].(string)
	if id == "" {
		v.failed = true
		return
	}
	catalog := v.call("GET", "/api/v1/products?q=00"+fixture, nil, "", 200)
	found := false
	for _, raw := range catalog["items"].([]any) {
		row := raw.(map[string]any)
		if str(row, "id") == id && str(row, "barcode") == "00"+fixture && str(row, "pack_size") == "10" {
			found = true
		}
	}
	if !found {
		fmt.Println("FAIL F-1 persisted barcode/catalog fields")
		v.failed = true
	}
	v.postA2("/goods-receipts", map[string]any{"location_id": "21", "supplier_id": "1", "document_no": fixture, "reason": "Isolated A2 verification fixture", "lines": []any{map[string]string{"product_id": id, "lot": "E", "expires_on": "2098-12-31", "quantity": "4"}, map[string]string{"product_id": id, "lot": "L", "expires_on": "2099-12-31", "quantity": "110"}}}, csrf, 201)
	draft := v.postA2("/transfers", map[string]any{"location_id": "21", "target_id": "1", "items": []any{map[string]string{"product_id": id, "quantity": "114", "unit": "unit"}}}, csrf, 201)
	tid, _ := draft["id"].(string)
	v.postA2("/transfers/"+tid+"/send", map[string]string{"location_id": "21"}, csrf, 200)
	v.call("POST", "/api/v1/auth/logout", nil, csrf, 204)
	csrf = v.loginA2("seed.yildiz")
	v.postA2("/transfers/"+tid+"/receive", map[string]string{"location_id": "1"}, csrf, 200)
	order := map[string]any{"location_id": "1", "items": []any{map[string]string{"product_id": id, "quantity": "6", "unit": "unit"}}}
	quote := v.postA2("/sales/quote", order, csrf, 200)
	order["quote"] = quote["quote"]
	sale := v.postA2("/sales", order, csrf, 201)
	allocationsOK := false
	if lines, ok := sale["lines"].([]any); ok && len(lines) == 1 {
		if a, ok := lines[0].(map[string]any)["allocations"].([]any); ok && len(a) == 2 {
			allocationsOK = a[0].(map[string]any)["quantity"] == "4" && a[1].(map[string]any)["quantity"] == "2"
		}
	}
	if !allocationsOK || sale["total"] != "600" {
		fmt.Println("FAIL T-1 FEFO 4+2 / total600")
		v.failed = true
	} else {
		fmt.Println("PASS F-1 catalog leading-zero / F-2 basic receipt-transfer / F-4 normal sale / T-1 FEFO4+2 (partial F-2/F-4)")
	}
	order = map[string]any{"location_id": "1", "items": []any{map[string]string{"product_id": id, "quantity": "1", "unit": "unit"}}}
	quote = v.postA2("/sales/quote", order, csrf, 200)
	order["quote"] = quote["quote"]
	// 108 remain: consume 8 first, leaving exactly 100 for the independent-key race.
	eight := map[string]any{"location_id": "1", "items": []any{map[string]string{"product_id": id, "quantity": "8", "unit": "unit"}}}
	q := v.postA2("/sales/quote", eight, csrf, 200)
	eight["quote"] = q["quote"]
	v.postA2("/sales", eight, csrf, 201)
	if !v.raceA2(order, csrf, false, 100, 100) {
		v.failed = true
	}
	// Refill a separate 10-unit lot using the same real journey, before same-key race.
	v.call("POST", "/api/v1/auth/logout", nil, csrf, 204)
	csrf = v.loginA2("depo")
	v.postA2("/goods-receipts", map[string]any{"location_id": "21", "supplier_id": "1", "document_no": fixture + "-refill", "reason": "Same-key verification", "lines": []any{map[string]string{"product_id": id, "lot": "R", "expires_on": "2099-12-31", "quantity": "10"}}}, csrf, 201)
	draft = v.postA2("/transfers", map[string]any{"location_id": "21", "target_id": "1", "items": []any{map[string]string{"product_id": id, "quantity": "10", "unit": "unit"}}}, csrf, 201)
	tid, _ = draft["id"].(string)
	v.postA2("/transfers/"+tid+"/send", map[string]string{"location_id": "21"}, csrf, 200)
	v.call("POST", "/api/v1/auth/logout", nil, csrf, 204)
	csrf = v.loginA2("seed.yildiz")
	v.postA2("/transfers/"+tid+"/receive", map[string]string{"location_id": "1"}, csrf, 200)
	if !v.raceA2(order, csrf, true, 200, 0) {
		v.failed = true
	}
	if v.reconcileA2(id) {
		fmt.Println("PASS A2 INVARIANTS: branch account balances=ledger sums, final physical9")
	} else {
		fmt.Println("FAIL A2 INVARIANTS")
		v.failed = true
	}
	v.call("POST", "/api/v1/auth/logout", nil, csrf, 204)
	fmt.Printf("A2 fixture product=%s barcode=00%s elapsed=%s (retained for inspection)\n", id, fixture, time.Since(started))
}
func (v *runner) raceA2(body any, csrf string, same bool, wantSuccess, wantReject int) bool {
	b, _ := json.Marshal(body)
	type result struct {
		status int
		body   []byte
		err    error
		ms     float64
	}
	out := make(chan result, 200)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	prefix := fmt.Sprint(time.Now().UnixNano())
	start := time.Now()
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			req, _ := http.NewRequest("POST", v.base+"/api/v1/sales", bytes.NewReader(b))
			req.Header.Set("Origin", v.origin)
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Content-Type", "application/json")
			key := prefix + "-" + strconv.Itoa(i)
			if same {
				key = prefix + "-same"
			}
			req.Header.Set("Idempotency-Key", key)
			requestStart := time.Now()
			r, e := v.client.Do(req)
			if e != nil {
				out <- result{err: e}
				return
			}
			defer r.Body.Close()
			data, e := io.ReadAll(r.Body)
			if r.Header.Get("X-DDO-Stamp") != "a33906" {
				e = fmt.Errorf("stamp")
			}
			out <- result{r.StatusCode, data, e, float64(time.Since(requestStart).Microseconds()) / 1000}
		}(i)
	}
	close(gate)
	wg.Wait()
	close(out)
	success, reject, unexpected := 0, 0, 0
	first := ""
	latencies := []float64{}
	for r := range out {
		latencies = append(latencies, r.ms)
		if r.err != nil {
			unexpected++
			continue
		}
		if r.status == 201 {
			success++
			if same {
				if first == "" {
					first = string(r.body)
				}
				if first != string(r.body) {
					unexpected++
				}
			}
		} else if r.status == 409 && bytes.Contains(r.body, []byte("INSUFFICIENT_STOCK")) {
			reject++
		} else {
			unexpected++
		}
	}
	ok := success == wantSuccess && reject == wantReject && unexpected == 0
	label := "FAIL"
	if ok {
		label = "PASS"
	}
	fmt.Printf("%s T-1/API-03 HTTP200 same_key=%t success=%d stock_reject=%d unexpected/timeout/5xx=%d duration=%s\n", label, same, success, reject, unexpected, time.Since(start))
	sort.Float64s(latencies)
	if len(latencies) == 200 {
		fmt.Printf("LOAD contention VU=200 requests=200 same_key=%t p50_ms=%.3f p95_ms=%.3f p99_ms=%.3f expected_reject=%d unexpected=%d\n", same, latencies[99], latencies[189], latencies[197], reject, unexpected)
	}
	return ok
}
func (v *runner) reconcileA2(product string) bool {
	lots := map[string]map[string]string{}
	cursor := ""
	for {
		page := v.call("GET", "/api/v1/inventory?location_id=1&limit=200&cursor="+url.QueryEscape(cursor), nil, "", 200)
		rows, _ := page["items"].([]any)
		for _, value := range rows {
			row := value.(map[string]any)
			if row["product_id"] == product {
				balances := map[string]string{}
				for a, q := range row["balances"].(map[string]any) {
					balances[a] = q.(string)
				}
				lots[row["id"].(string)] = balances
			}
		}
		cursor, _ = page["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}
	sums := map[string]map[string]int64{}
	cursor = ""
	for {
		page := v.call("GET", "/api/v1/inventory/movements?location_id=1&limit=200&cursor="+url.QueryEscape(cursor), nil, "", 200)
		rows, _ := page["items"].([]any)
		for _, value := range rows {
			row := value.(map[string]any)
			lot := row["lot_id"].(string)
			if _, ok := lots[lot]; !ok {
				continue
			}
			if sums[lot] == nil {
				sums[lot] = map[string]int64{}
			}
			q, e := strconv.ParseInt(row["quantity"].(string), 10, 64)
			if e != nil {
				return false
			}
			sums[lot][row["account"].(string)] += q
		}
		cursor, _ = page["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}
	if len(lots) == 0 {
		return false
	}
	physical := int64(0)
	for lot, balances := range lots {
		for account, q := range balances {
			want, e := strconv.ParseInt(q, 10, 64)
			if e != nil || sums[lot][account] != want {
				return false
			}
			if account == "physical" {
				physical += want
			}
		}
	}
	return physical == 9
}
