package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"sync"
	"time"
)

func (v *runner) a3Account(name string) (*runner, string) {
	jar, _ := cookiejar.New(nil)
	r := &runner{base: v.base, origin: v.origin, password: v.password, client: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
	return r, r.loginA2(name)
}
func str(m map[string]any, key string) string { s, _ := m[key].(string); return s }
func (v *runner) runA3() {
	started := time.Now()
	prefix := fmt.Sprintf("VERIFY-A3-%d", started.UnixNano())
	w, wcsrf := v.a3Account("depo")
	a, ac := v.a3Account("seed.yildiz")
	b, bc := v.a3Account("eczaci.b")
	tech, tc := v.a3Account("teknisyen")
	acc, cc := v.a3Account("muhasebe")
	defer func() {
		for _, p := range []struct {
			r *runner
			c string
		}{{w, wcsrf}, {a, ac}, {b, bc}, {tech, tc}, {acc, cc}} {
			p.r.call("POST", "/api/v1/auth/logout", nil, p.c, 204)
			if p.r.failed {
				v.failed = true
			}
		}
	}()
	ids := []string{}
	for n, pack := range []string{"30", "20", "20"} {
		group := ""
		if n == 2 {
			group = "CONTROL-TAB"
		}
		dose := "500mg"
		if n == 1 {
			dose = "0.5g"
		}
		p := w.postA2("/products", map[string]any{"location_id": "21", "product": map[string]any{"barcode": fmt.Sprintf("00%d%d", started.UnixNano(), n), "name": prefix + "-" + pack + group, "dose": dose, "form": "tablet", "base_unit": "tablet", "pack_size": pack, "blister_size": "5", "sale_multiple": "1", "ingredients": []string{"a3-verify"}, "prescription_required": true, "controlled_group": group, "pack_price": "20000"}}, wcsrf, 201)
		id := str(p, "id")
		if id == "" {
			v.failed = true
			return
		}
		ids = append(ids, id)
		w.postA2("/goods-receipts", map[string]any{"location_id": "21", "supplier_id": "1", "document_no": prefix + strconv.Itoa(n), "reason": "A3 retained verification fixture", "lines": []any{map[string]string{"product_id": id, "quantity": "100", "lot": "A3", "expires_on": "2099-12-31"}}}, wcsrf, 201)
		for _, dest := range []struct {
			id string
			r  *runner
			c  string
		}{{"1", a, ac}, {"2", b, bc}} {
			t := w.postA2("/transfers", map[string]any{"location_id": "21", "target_id": dest.id, "items": []any{map[string]string{"product_id": id, "quantity": "50", "unit": "unit"}}}, wcsrf, 201)
			tid := str(t, "id")
			w.postA2("/transfers/"+tid+"/send", map[string]string{"location_id": "21"}, wcsrf, 200)
			dest.r.postA2("/transfers/"+tid+"/receive", map[string]string{"location_id": dest.id}, dest.c, 200)
		}
	}
	importRX := func(external, patient, product string) map[string]any {
		return map[string]any{"location_id": "1", "prescription": map[string]any{"external_id": external, "patient_external_id": patient, "birth_date": "1990-01-01", "issued_at": started.UTC().Add(-time.Hour).Format(time.RFC3339Nano), "lines": []any{map[string]string{"prescribed_product_id": product, "prescribed_base": "30"}}}}
	}
	load := func(external, patient, product string) (string, string) {
		out := a.postA2("/prescriptions", importRX(external, patient, product), ac, 201)
		id := str(out, "id")
		line := ""
		if lines, ok := out["lines"].([]any); ok && len(lines) > 0 {
			line = str(lines[0].(map[string]any), "id")
		}
		return id, line
	}
	input := func(rx, line, p, q, loc string) map[string]any {
		return map[string]any{"location_id": loc, "prescription_id": rx, "dispense": []any{map[string]string{"prescription_line_id": line, "dispensed_product_id": p, "quantity": q, "unit": "unit"}}}
	}
	quote := func(r *runner, c string, in map[string]any) {
		q := r.postA2("/sales/quote", in, c, 200)
		in["quote"] = q["quote"]
	}
	rx, line := load(prefix+"-partial", prefix+"H1", ids[0])
	duplicate := a.postA2("/prescriptions", importRX(prefix+"-partial", prefix+"H1", ids[0]), ac, 201)
	if str(duplicate, "id") != rx {
		v.failed = true
		fmt.Println("FAIL F-3 global duplicate")
	}
	eq := a.call("GET", "/api/v1/products/"+ids[0]+"/equivalents?limit=200", nil, "", 200)
	found := false
	if rows, ok := eq["items"].([]any); ok {
		for _, row := range rows {
			if str(row.(map[string]any), "id") == ids[1] {
				found = true
			}
		}
	}
	if !found {
		v.failed = true
		fmt.Println("FAIL F-3 equivalent20/30")
	}
	in := input(rx, line, ids[1], "10", "1")
	quote(a, ac, in)
	first := a.postA2("/sales", in, ac, 201)
	if str(first, "total") != "9000" || str(first, "patient_total") != "1800" {
		v.failed = true
		fmt.Println("FAIL T-3 exact partial price")
	}
	a.call("POST", "/api/v1/sales", in, ac, 201) // same key/body as postA2: response replay.
	other := input(rx, line, ids[1], "1", "2")
	b.postA2("/sales/quote", other, bc, 403)
	in = input(rx, line, ids[1], "15", "1")
	quote(a, ac, in)
	a.postA2("/sales", in, ac, 201)
	remaining := a.call("GET", "/api/v1/prescriptions?location_id=1&q="+prefix+"-partial", nil, "", 200)
	remOK := false
	if rows, ok := remaining["items"].([]any); ok && len(rows) == 1 {
		if lines, ok := rows[0].(map[string]any)["lines"].([]any); ok && len(lines) == 1 {
			remOK = str(lines[0].(map[string]any), "remaining_base") == "5"
		}
	}
	if !remOK {
		v.failed = true
		fmt.Println("FAIL T-2 remainder5")
	}
	a.postA2("/sales/quote", input(rx, line, ids[1], "6", "1"), ac, 422)
	normal := map[string]any{"location_id": "1", "items": []any{map[string]string{"product_id": ids[2], "quantity": "1", "unit": "unit"}}}
	a.postA2("/sales/quote", normal, ac, 422)
	tech.postA2("/sales", input(rx, line, ids[1], "1", "1"), tc, 403)
	acc.call("GET", "/api/v1/prescriptions?location_id=1", nil, "", 403)
	// Exactly one owner wins with two independently quoted branch commands.
	ownership, l := load(prefix+"-ownership", prefix+"H2", ids[0])
	oa := input(ownership, l, ids[0], "1", "1")
	ob := input(ownership, l, ids[0], "1", "2")
	quote(a, ac, oa)
	quote(b, bc, ob)
	if !raceA3(a, ac, oa, b, bc, ob, 403, "PRESCRIPTION_BRANCH") {
		v.failed = true
	}
	// Different RX IDs and branches share one patient/day/group counter.
	r1, l1 := load(prefix+"-control1", prefix+"HC", ids[2])
	r2, l2 := load(prefix+"-control2", prefix+"HC", ids[2])
	ca := input(r1, l1, ids[2], "6", "1")
	cb := input(r2, l2, ids[2], "6", "2")
	quote(a, ac, ca)
	quote(b, bc, cb)
	if !raceA3(a, ac, ca, b, bc, cb, 422, "DAILY_LIMIT") {
		v.failed = true
	}
	ledgerTotal := int64(0)
	for _, p := range []struct {
		r   *runner
		loc string
	}{{a, "1"}, {b, "2"}} {
		path := "/api/v1/controlled-ledger?location_id=" + p.loc + "&limit=200"
		next := ""
		for {
			out := p.r.call("GET", path+next, nil, "", 200)
			if rows, ok := out["items"].([]any); ok {
				for _, row := range rows {
					x := row.(map[string]any)
					if str(x, "prescription_id") == r1 || str(x, "prescription_id") == r2 {
						n, _ := strconv.ParseInt(str(x, "quantity"), 10, 64)
						ledgerTotal += n
					}
				}
			}
			cursor := str(out, "next_cursor")
			if cursor == "" {
				break
			}
			next = "&cursor=" + cursor
		}
	}
	if ledgerTotal != 6 {
		fmt.Println("FAIL controlled ledger expected6 got", ledgerTotal)
		v.failed = true
	}
	if !v.failed && !a.failed && !b.failed && !w.failed && !tech.failed && !acc.failed {
		fmt.Println("PASS F-3/T-2 RX global identity; equivalent20/30; partial30->10->15->remaining5; branch ownership race1/1; patient daily race6/6->6; ledger6; role boundaries/replay")
	}
	fmt.Printf("A3 retained fixture products=%v RXpartial=%s ownership=%s control=%s/%s elapsed=%s\n", ids, rx, ownership, r1, r2, time.Since(started))
	fmt.Println("T-3 pricing/snapshot API subset exercised; refund rounding check follows in A4/full mode")
}

func raceA3(a *runner, ac string, ia any, b *runner, bc string, ib any, reject int, code string) bool {
	start := make(chan struct{})
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	codes := make([]string, 2)
	for i, p := range []struct {
		r  *runner
		c  string
		in any
	}{{a, ac, ia}, {b, bc, ib}} {
		wg.Add(1)
		go func(i int, p struct {
			r  *runner
			c  string
			in any
		}) {
			defer wg.Done()
			<-start
			raw, _ := json.Marshal(p.in)
			req, _ := http.NewRequest("POST", p.r.base+"/api/v1/sales", bytes.NewReader(raw))
			req.Header.Set("Origin", p.r.origin)
			req.Header.Set("X-CSRF-Token", p.c)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", fmt.Sprintf("a3-race-%d-%d", time.Now().UnixNano(), i))
			resp, e := p.r.client.Do(req)
			if e != nil {
				return
			}
			defer resp.Body.Close()
			statuses[i] = resp.StatusCode
			data, _ := io.ReadAll(resp.Body)
			var out struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(data, &out)
			codes[i] = out.Error.Code
		}(i, p)
	}
	close(start)
	wg.Wait()
	success, denied := 0, 0
	for i, status := range statuses {
		if status == 201 {
			success++
		}
		if status == reject && codes[i] == code {
			denied++
		}
	}
	ok := success == 1 && denied == 1
	if ok {
		fmt.Printf("PASS T-2 HTTP race success=%d reject=%d code=%s\n", success, denied, code)
	} else {
		fmt.Printf("FAIL T-2 race statuses=%v codes=%v\n", statuses, codes)
	}
	return ok
}
