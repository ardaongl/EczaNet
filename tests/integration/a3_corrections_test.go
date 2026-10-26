package integration

import (
	"bytes"
	"context"
	"eczanet/internal/auth"
	"eczanet/internal/platform"
	"eczanet/internal/stock"
	"eczanet/tests/dbtest"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type correctionReply struct {
	status int
	raw    []byte
	body   map[string]any
	err    error
}
type correctionAPI struct {
	t              *testing.T
	ctx            context.Context
	owner, runtime *pgxpool.Pool
	server         *httptest.Server
	base           string
	prefix         string
	csrf           string
	tokens         map[string]string
}

func correctionSetup(t *testing.T) *correctionAPI {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" || os.Getenv("TEST_RUNTIME_URL") == "" {
		t.Skip("real PostgreSQL required")
	}
	dbtest.Acquire(t, true)
	f := &correctionAPI{t: t, ctx: context.Background(), prefix: fmt.Sprintf("RA3-%d", time.Now().UnixNano()), tokens: map[string]string{}, csrf: auth.Token()}
	var e error
	f.owner, e = pgxpool.New(f.ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := pgxpool.ParseConfig(os.Getenv("TEST_RUNTIME_URL"))
	if e != nil {
		t.Fatal(e)
	}
	c.ConnConfig.RuntimeParams["application_name"] = f.prefix
	f.runtime, e = pgxpool.NewWithConfig(f.ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	s := platform.NewServer(platform.Config{RequestTimeout: 15 * time.Second, Origins: map[string]bool{"http://localhost:3000": true}}, auth.Postgres{Pool: f.runtime}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	s.Stock = &stock.Service{Pool: f.runtime}
	f.base = os.Getenv("TEST_CORRECTION_API_URL")
	if f.base == "" {
		f.server = httptest.NewServer(s.Handler())
		f.base = f.server.URL
	}
	for _, actor := range []string{"1", "3", "5", "6"} {
		token := auth.Token()
		f.tokens[actor] = token
		if _, e = f.owner.Exec(f.ctx, `INSERT INTO "pl-sessions"(token_hash,user_id,csrf_hash,expires_at) VALUES($1,$2,$3,clock_timestamp()+interval '1 hour')`, auth.Hash(token), actor, auth.Hash(f.csrf)); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(func() {
		if f.server != nil {
			f.server.Close()
		}
		for _, token := range f.tokens {
			if _, e := f.owner.Exec(f.ctx, `DELETE FROM "pl-sessions" WHERE token_hash=$1`, auth.Hash(token)); e != nil {
				t.Error(e)
			}
		}
		f.runtime.Close()
		f.owner.Close()
	})
	return f
}
func (f *correctionAPI) call(actor, method, path, key string, in any) correctionReply {
	var payload []byte
	if in != nil {
		payload, _ = json.Marshal(in)
	}
	req, e := http.NewRequest(method, f.base+"/api/v1"+path, bytes.NewReader(payload))
	if e != nil {
		return correctionReply{err: e}
	}
	req.AddCookie(&http.Cookie{Name: "eczanet_session", Value: f.tokens[actor]})
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("X-CSRF-Token", f.csrf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	r, e := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if e != nil {
		return correctionReply{err: e}
	}
	defer r.Body.Close()
	raw, e := io.ReadAll(r.Body)
	out := correctionReply{status: r.StatusCode, raw: raw, err: e}
	if e == nil {
		out.err = json.Unmarshal(raw, &out.body)
	}
	if r.Header.Get("X-DDO-Stamp") != "a33906" {
		out.err = fmt.Errorf("missing stamp")
	}
	return out
}
func (f *correctionAPI) require(r correctionReply, status int) map[string]any {
	f.t.Helper()
	if r.err != nil || r.status != status {
		f.t.Fatalf("HTTP want%d got%d err=%v body=%s", status, r.status, r.err, r.raw)
	}
	return r.body
}
func correctionID(m map[string]any) string { id, _ := m["id"].(string); return id }
func (f *correctionAPI) post(actor, path string, in any, status int) map[string]any {
	f.t.Helper()
	return f.require(f.call(actor, "POST", path, fmt.Sprintf("%s-%d", f.prefix, time.Now().UnixNano()), in), status)
}
func (f *correctionAPI) product() string {
	f.t.Helper()
	p := f.post("3", "/products", map[string]any{"location_id": "21", "product": stock.Product{Barcode: "00" + fmt.Sprint(time.Now().UnixNano()), Name: f.prefix, Dose: "5mg", Form: "tablet", BaseUnit: "tablet", PackSize: "10", BlisterSize: "5", SaleMultiple: "1", Ingredients: []string{"a3-correction"}, PackPrice: "1000"}}, 201)
	id := correctionID(p)
	f.post("3", "/goods-receipts", stock.Input{LocationID: "21", SupplierID: "1", DocumentNo: id + f.prefix, Reason: "A3 correction retained fixture", Lines: []stock.ReceiptLine{{ProductID: id, Lot: f.prefix, ExpiresOn: "2099-12-31", Quantity: "20"}}}, 201)
	for _, branch := range []string{"1", "2"} {
		tid := correctionID(f.post("3", "/transfers", stock.Input{LocationID: "21", TargetID: branch, Items: []stock.Item{{ProductID: id, Quantity: "10", Unit: "unit"}}}, 201))
		f.post("3", "/transfers/"+tid+"/send", stock.Input{LocationID: "21"}, 200)
		actor := "1"
		if branch == "2" {
			actor = "6"
		}
		f.post(actor, "/transfers/"+tid+"/receive", stock.Input{LocationID: branch}, 200)
	}
	return id
}
func (f *correctionAPI) rxInput(product string) stock.Input {
	return stock.Input{LocationID: "1", Prescription: &stock.PrescriptionInput{ExternalID: f.prefix, PatientID: f.prefix + "-H", BirthDate: "1990-01-01", IssuedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond), Lines: []stock.PrescriptionLineInput{{ProductID: product, Quantity: "30"}}}}
}
func (f *correctionAPI) rxSale(product string, rx map[string]any) stock.Input {
	line := rx["lines"].([]any)[0].(map[string]any)["id"].(string)
	return stock.Input{LocationID: "2", PrescriptionID: correctionID(rx), Dispense: []stock.DispenseItem{{LineID: line, ProductID: product, Quantity: "1", Unit: "unit"}}}
}
func (f *correctionAPI) auditHold() (pgx.Tx, int) {
	f.t.Helper()
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		f.t.Fatal(e)
	}
	f.t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, e = tx.Exec(f.ctx, `LOCK TABLE "pl-audit-events" IN SHARE MODE`); e != nil {
		f.t.Fatal(e)
	}
	var pid int
	if e = tx.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid); e != nil {
		f.t.Fatal(e)
	}
	return tx, pid
}

type correctionFlight struct {
	pid          int
	command, key string
	done         <-chan correctionReply
}

// Resolve a command's actual backend using a private, uncommitted idempotency
// conflict. Only this request knows that composite key. Rollback then lets the
// production INSERT proceed unchanged; no placeholder result is committed.
func (f *correctionAPI) later(actor, path, key string, in any) correctionFlight {
	f.t.Helper()
	input, ok := in.(stock.Input)
	if !ok {
		f.t.Fatal("fault command requires stock.Input")
	}
	command := map[string]string{"/sales": "sale.create:", "/prices": "price.create:", "/prescriptions": "prescription.create:"}[path]
	if command == "" {
		f.t.Fatal("unsupported fault command")
	}
	gate, e := f.owner.Begin(f.ctx)
	if e != nil {
		f.t.Fatal(e)
	}
	defer gate.Rollback(context.Background())
	var existing string
	e = gate.QueryRow(f.ctx, `SELECT key FROM "pl-idempotency" WHERE actor_id=$1 AND command=$2 AND location_id=$3 AND key=$4 FOR UPDATE`, actor, command, input.LocationID, key).Scan(&existing)
	if e == pgx.ErrNoRows {
		_, e = gate.Exec(f.ctx, `INSERT INTO "pl-idempotency"(actor_id,command,location_id,key,digest) VALUES($1,$2,$3,$4,'uncommitted-fault-gate')`, actor, command, input.LocationID, key)
	}
	if e != nil {
		f.t.Fatal(e)
	}
	var gatePID int
	if e = gate.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&gatePID); e != nil {
		f.t.Fatal(e)
	}
	done := make(chan correctionReply, 1)
	go func() { done <- f.call(actor, "POST", path, key, in) }()
	flight := correctionFlight{command: command, key: key, done: done}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		var pids []int
		if e = f.owner.QueryRow(f.ctx, `SELECT coalesce(array_agg(pid),'{}'::int[]) FROM pg_stat_activity WHERE usename='eczanet_runtime' AND $1::int=ANY(pg_blocking_pids(pid))`, gatePID).Scan(&pids); e != nil {
			f.t.Fatal(e)
		}
		if len(pids) > 1 {
			f.t.Fatal("ambiguous private command gate", pids)
		}
		if len(pids) == 1 {
			flight.pid = pids[0]
			break
		}
		select {
		case r := <-done:
			f.t.Fatalf("command ended before private PID gate: status=%d err=%v", r.status, r.err)
		default:
		}
		time.Sleep(2 * time.Millisecond)
	}
	if flight.pid == 0 {
		f.t.Fatal("private command PID not observed")
	}
	f.t.Logf("command gate command=%s key=%s runtime_pid=%d gate_pid=%d", command, key, flight.pid, gatePID)
	if e = gate.Rollback(f.ctx); e != nil {
		f.t.Fatal(e)
	}
	return flight
}

func (f *correctionAPI) waitForBlock(flight correctionFlight, blocker int) int {
	f.t.Helper()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		var blocked bool
		if e := f.owner.QueryRow(f.ctx, `SELECT $2::int=ANY(pg_blocking_pids($1::int))`, flight.pid, blocker).Scan(&blocked); e != nil {
			f.t.Fatal(e)
		}
		if blocked {
			f.traceWait(flight, blocker)
			return flight.pid
		}
		select {
		case r := <-flight.done:
			code := ""
			if err, ok := r.body["error"].(map[string]any); ok {
				code, _ = err["code"].(string)
			}
			f.t.Fatalf("command completed before expected lock: command=%s pid=%d blocker=%d status=%d code=%s err=%v", flight.command, flight.pid, blocker, r.status, code, r.err)
		default:
		}
		time.Sleep(2 * time.Millisecond)
	}
	f.traceWait(flight, blocker)
	f.t.Fatalf("real database lock wait not observed: command=%s pid=%d blocker=%d", flight.command, flight.pid, blocker)
	return 0
}

// Classify SQL without logging SQL text, parameters, cookies or clinical bodies.
func (f *correctionAPI) traceWait(flight correctionFlight, expected int) {
	f.t.Helper()
	var state, waitType, waitEvent, class string
	var blockers []int
	e := f.runtime.QueryRow(f.ctx, `SELECT state,coalesce(wait_event_type,''),coalesce(wait_event,''),pg_blocking_pids(pid),CASE WHEN query LIKE 'INSERT INTO "pl-audit-events"%' THEN 'audit-insert' WHEN query LIKE '%"pl-products"%FOR SHARE%' THEN 'product-price-share' WHEN query LIKE '%"pl-products"%FOR UPDATE%' THEN 'product-price-exclusive' WHEN query LIKE '%"pl-prescriptions"%FOR SHARE%' THEN 'rx-owner-share' WHEN query LIKE 'INSERT INTO "pl-idempotency"%' THEN 'idempotency-insert' ELSE 'other' END FROM pg_stat_activity WHERE pid=$1`, flight.pid).Scan(&state, &waitType, &waitEvent, &blockers, &class)
	if e != nil {
		f.t.Logf("trace unavailable pid=%d", flight.pid)
		return
	}
	f.t.Logf("wait trace command=%s key=%s pid=%d expected_blocker=%d actual_blockers=%v state=%s wait=%s/%s sql_class=%s", flight.command, flight.key, flight.pid, expected, blockers, state, waitType, waitEvent, class)
}

func (f *correctionAPI) transition() time.Time {
	f.t.Helper()
	var at time.Time
	if e := f.owner.QueryRow(f.ctx, `SELECT clock_timestamp()+interval '1 second'`).Scan(&at); e != nil {
		f.t.Fatal(e)
	}
	return at
}
func (f *correctionAPI) waitAt(at time.Time) {
	f.t.Helper()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		var ready bool
		if e := f.owner.QueryRow(f.ctx, `SELECT clock_timestamp()>=$1`, at).Scan(&ready); e != nil {
			f.t.Fatal(e)
		}
		if ready {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	f.t.Fatal("clock did not reach price boundary")
}
func (f *correctionAPI) salePriceInvariant(id string) {
	f.t.Helper()
	var valid bool
	if e := f.owner.QueryRow(f.ctx, `SELECT bool_and(s.effective_at>=p.valid_from AND s.effective_at<p.valid_to) FROM "pl-sales" s JOIN "pl-sale-lines" l ON l.sale_id=s.id JOIN "pl-price-periods" p ON p.id=l.price_id WHERE s.id=$1`, id).Scan(&valid); e != nil || !valid {
		f.t.Fatal("sale price/effective_at invariant", id, valid, e)
	}
}
func TestA3CorrectionPricePublisherBeforeSale(t *testing.T) {
	f := correctionSetup(t)
	p := f.product()
	in := stock.Input{LocationID: "1", Items: []stock.Item{{ProductID: p, Quantity: "1", Unit: "unit"}}}
	q := f.post("1", "/sales/quote", in, 200)
	in.Quote = q["quote"].(string)
	holder, holderPID := f.auditHold()
	at := f.transition()
	publishing := f.later("5", "/prices", f.prefix+"-pub", stock.Input{LocationID: "1", Price: &stock.PriceInput{ProductID: p, ValidFrom: at, PackPrice: "2000"}})
	publisherPID := f.waitForBlock(publishing, holderPID)
	f.waitAt(at)
	selling := f.later("1", "/sales", f.prefix+"-old-sale", in)
	_ = f.waitForBlock(selling, publisherPID) // shared price lock, rather than the former late FK wait
	if e := holder.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	f.require(<-publishing.done, 201)
	rejected := f.require(<-selling.done, 409)
	if rejected["error"].(map[string]any)["code"] != "PRICE_CHANGED" || rejected["new_quote"].(map[string]any)["total"] != "200" {
		t.Fatal(rejected)
	}
	var effects int
	if e := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-idempotency" WHERE key=$1`, f.prefix+"-old-sale").Scan(&effects); e != nil || effects != 0 {
		t.Fatal("stale sale left effects", effects, e)
	}
	in.Quote = f.post("1", "/sales/quote", stock.Input{LocationID: "1", Items: in.Items}, 200)["quote"].(string)
	sold := f.post("1", "/sales", in, 201)
	if sold["total"] != "200" {
		t.Fatal(sold)
	}
	f.salePriceInvariant(correctionID(sold))
	t.Logf("product=%s sale=%s auditWait=true priceWait=true publisher201 stale409/PRICE_CHANGED explicitNewIntent201 total200 validInterval=true", p, correctionID(sold))
}
func TestA3CorrectionPricePublisherWaitsPastStart(t *testing.T) {
	f := correctionSetup(t)
	p := f.product()
	in := stock.Input{LocationID: "1", Items: []stock.Item{{ProductID: p, Quantity: "1", Unit: "unit"}}}
	in.Quote = f.post("1", "/sales/quote", in, 200)["quote"].(string)
	holder, holderPID := f.auditHold()
	selling := f.later("1", "/sales", f.prefix+"-sale", in)
	sellerPID := f.waitForBlock(selling, holderPID)
	at := f.transition()
	publishing := f.later("5", "/prices", f.prefix+"-late-pub", stock.Input{LocationID: "1", Price: &stock.PriceInput{ProductID: p, ValidFrom: at, PackPrice: "2000"}})
	_ = f.waitForBlock(publishing, sellerPID)
	f.waitAt(at)
	if e := holder.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	sold := f.require(<-selling.done, 201)
	denied := f.require(<-publishing.done, 422)
	if denied["error"].(map[string]any)["code"] != "FUTURE_PRICE_REQUIRED" || sold["total"] != "100" {
		t.Fatal(sold, denied)
	}
	f.salePriceInvariant(correctionID(sold))
	var n int
	if e := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM "pl-price-periods" WHERE product_id=$1`, p).Scan(&n); e != nil || n != 1 {
		t.Fatal("late publisher did not roll back", n, e)
	}
	t.Logf("product=%s sale=%s auditWait=true publisherWait=true sale201/100 latePublisher422/FUTURE_PRICE_REQUIRED validInterval=true", p, correctionID(sold))
}
func TestA3CorrectionReplayChecksCurrentRXOwner(t *testing.T) {
	f := correctionSetup(t)
	p := f.product()
	in := f.rxInput(p)
	key := f.prefix + "-import"
	first := f.call("1", "POST", "/prescriptions", key, in)
	rx := f.require(first, 201)
	if replay := f.call("1", "POST", "/prescriptions", key, in); !bytes.Equal(first.raw, replay.raw) || replay.status != 201 {
		t.Fatal("authorized import replay changed")
	}
	sale := f.rxSale(p, rx)
	sale.Quote = f.post("6", "/sales/quote", sale, 200)["quote"].(string)
	holder, holderPID := f.auditHold()
	selling := f.later("6", "/sales", f.prefix+"-rxsale", sale)
	sellerPID := f.waitForBlock(selling, holderPID)
	replaying := f.later("1", "/prescriptions", key, in)
	_ = f.waitForBlock(replaying, sellerPID)
	if e := holder.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	sold := f.require(<-selling.done, 201)
	for _, r := range []correctionReply{<-replaying.done, f.call("1", "POST", "/prescriptions", key, in), f.call("1", "POST", "/prescriptions", key+"-new", in)} {
		body := f.require(r, 403)
		if len(body) != 1 || len(body["error"].(map[string]any)) != 2 || body["error"].(map[string]any)["code"] != "PRESCRIPTION_BRANCH" {
			t.Fatal("clinical data leaked", body)
		}
	}
	read := f.require(f.call("1", "GET", "/prescriptions?location_id=1&q="+f.prefix, "", nil), 200)
	if len(read["items"].([]any)) != 0 {
		t.Fatal("B1 can still read B2 RX")
	}
	in.LocationID = "2"
	ownKey := key + "-b2"
	own := f.call("6", "POST", "/prescriptions", ownKey, in)
	f.require(own, 201)
	if again := f.call("6", "POST", "/prescriptions", ownKey, in); again.status != 201 || !bytes.Equal(own.raw, again.raw) {
		t.Fatal("authorized owner replay changed")
	}
	var cached []byte
	if e := f.owner.QueryRow(f.ctx, `SELECT result FROM "pl-idempotency" WHERE actor_id=1 AND command='prescription.create:' AND location_id=1 AND key=$1`, key).Scan(&cached); e != nil {
		t.Fatal(e)
	}
	var original map[string]any
	_ = json.Unmarshal(cached, &original)
	if original["owner_id"] != nil {
		t.Fatal("old cache rewritten")
	}
	f.salePriceInvariant(correctionID(sold))
	t.Logf("RX=%s product=%s owner=2 concurrentReplayWait=true B1/read=empty B1/new=403 B1/sameKey=403 errorOnly=true oldCacheUnchanged=true B2/replay201", correctionID(rx), p)
}

func TestA3CorrectionMultipleProductRXPriceLocks(t *testing.T) {
	f := correctionSetup(t)
	a, b := f.product(), f.product()
	in := f.rxInput(a)
	in.Prescription.Lines = append(in.Prescription.Lines, stock.PrescriptionLineInput{ProductID: b, Quantity: "30"})
	rx := f.post("1", "/prescriptions", in, 201)
	lines := rx["lines"].([]any)
	order := stock.Input{LocationID: "1", PrescriptionID: correctionID(rx)}
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i].(map[string]any)
		order.Dispense = append(order.Dispense, stock.DispenseItem{LineID: correctionID(l), ProductID: l["prescribed_product_id"].(string), Quantity: "1", Unit: "unit"})
	}
	order.Quote = f.post("1", "/sales/quote", order, 200)["quote"].(string)
	sold := f.post("1", "/sales", order, 201)
	f.salePriceInvariant(correctionID(sold))
	if sold["total"] != "180" {
		t.Fatal("RX basket pricing", sold)
	}
	var fulfilled string
	if e := f.owner.QueryRow(f.ctx, `SELECT string_agg(fulfilled_base::text,',' ORDER BY id) FROM "pl-prescription-lines" WHERE prescription_id=$1`, correctionID(rx)).Scan(&fulfilled); e != nil || fulfilled != "1,1" {
		t.Fatal(fulfilled, e)
	}

	t.Logf("RX=%s products=%s/%s reverse basket order; two price locks before guards/effective_at; sale=%s/180 fulfilled=1,1", correctionID(rx), a, b, correctionID(sold))
}

func TestA3CorrectionBlockObservationIgnoresOtherRuntimeCommand(t *testing.T) {
	f := correctionSetup(t)
	// Open the unrelated writer before the in-process API pool. This reproduces
	// the legacy external selector's earliest-PID ambiguity deterministically.
	noise, e := pgx.Connect(f.ctx, os.Getenv("TEST_RUNTIME_URL"))
	if e != nil {
		t.Fatal("noise fixture connection failed")
	}
	defer noise.Close(context.Background())
	noisePID := int(noise.PgConn().PID())
	p := f.product()
	in := stock.Input{LocationID: "1", Items: []stock.Item{{ProductID: p, Quantity: "1", Unit: "unit"}}}
	in.Quote = f.post("1", "/sales/quote", in, 200)["quote"].(string)
	holder, holderPID := f.auditHold()
	noiseTx, e := noise.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer noiseTx.Rollback(context.Background())
	noiseDone := make(chan error, 1)
	go func() {
		_, e := noiseTx.Exec(f.ctx, `INSERT INTO "pl-audit-events"(actor_id,command,object_id,request_id,new_value) VALUES(1,'test.unrelated-writer','noise','test-noise','{}')`)
		noiseDone <- e
	}()
	f.waitForBlock(correctionFlight{pid: noisePID, command: "unrelated-runtime-audit", key: "noise"}, holderPID)
	selling := f.later("1", "/sales", f.prefix+"-scoped-sale", in)
	f.waitForBlock(selling, holderPID)
	var candidates []int
	if e = f.owner.QueryRow(f.ctx, `SELECT array_agg(pid ORDER BY pid) FROM pg_stat_activity WHERE usename='eczanet_runtime' AND $1::int=ANY(pg_blocking_pids(pid))`, holderPID).Scan(&candidates); e != nil {
		t.Fatal(e)
	}
	seenNoise, seenOwn := false, false
	for _, pid := range candidates {
		seenNoise = seenNoise || pid == noisePID
		seenOwn = seenOwn || pid == selling.pid
	}
	if !seenNoise || !seenOwn || selling.pid == noisePID {
		t.Fatal("noise/own command distinction lost", candidates)
	}
	f.t.Logf("legacy external selector candidates=%v legacy_first=%d own_command_pid=%d unrelated_pid=%d legacy_selected_own=%t; exact private-key mapping selected own command", candidates, candidates[0], selling.pid, noisePID, candidates[0] == selling.pid)
	if e = holder.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-noiseDone; e != nil {
		t.Fatal(e)
	}
	if e = noiseTx.Rollback(f.ctx); e != nil {
		t.Fatal(e)
	} // only synthetic uncommitted audit row
	sold := f.require(<-selling.done, 201)
	f.salePriceInvariant(correctionID(sold))
}
