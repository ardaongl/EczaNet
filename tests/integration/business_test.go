package integration

import (
	"context"
	"eczanet/internal/auth"
	"eczanet/internal/platform"
	"eczanet/internal/stock"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBusinessHTTPProtection(t *testing.T) {
	if os.Getenv("TEST_RUNTIME_URL") == "" {
		t.Skip("PostgreSQL required")
	}
	ctx := context.Background()
	rp, e := pgxpool.New(ctx, os.Getenv("TEST_RUNTIME_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer rp.Close()
	owner, e := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	server := platform.NewServer(platform.Config{RequestTimeout: 15 * time.Second, Origins: map[string]bool{"http://localhost:3000": true}}, auth.Postgres{Pool: rp}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	server.Stock = &stock.Service{Pool: rp}
	handler := server.Handler()
	token := auth.Token()
	csrf := "a2-test-csrf"
	_, e = owner.Exec(ctx, `INSERT INTO "pl-sessions"(token_hash,user_id,csrf_hash,expires_at) VALUES($1,1,$2,clock_timestamp()+interval '1 hour')`, auth.Hash(token), auth.Hash(csrf))
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Exec(ctx, `DELETE FROM "pl-sessions" WHERE token_hash=$1`, auth.Hash(token))
	paths := []string{"/products", "/products/1/disable", "/goods-receipts", "/transfers", "/transfers/1/send", "/transfers/1/receive", "/sales/quote", "/sales", "/prescriptions", "/prices"}
	call := func(method, path, body, cookie, origin, security string) int {
		req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "eczanet_session", Value: cookie})
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", security)
		req.Header.Set("Idempotency-Key", "http-protection")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Header().Get("X-DDO-Stamp") != "a33906" {
			t.Fatal("stamp")
		}
		if w.Code >= 400 {
			var b map[string]any
			if json.Unmarshal(w.Body.Bytes(), &b) != nil || b["error"] == nil {
				t.Fatal("error shape")
			}
		}
		return w.Code
	}
	for _, p := range paths {
		if n := call("POST", p, `{"location_id":"1"}`, "", "http://localhost:3000", csrf); n != 401 {
			t.Fatal(p, n)
		}
		for _, v := range []struct{ origin, csrf string }{{"http://localhost:3000", "bad"}, {"https://blocked.invalid", csrf}} {
			if n := call("POST", p, `{"location_id":"1"}`, token, v.origin, v.csrf); n != 403 {
				t.Fatal(p, n)
			}
		}
	}
	for _, p := range []string{"/products", "/inventory?location_id=1", "/inventory/movements?location_id=1", "/sales?location_id=1", "/transfers?location_id=1", "/suppliers", "/transfer-targets", "/sales/1", "/prescriptions?location_id=1", "/prices", "/controlled-ledger?location_id=1", "/products/1/equivalents"} {
		if n := call("GET", p, "", "", "", ""); n != 401 {
			t.Fatal(p, n)
		}
	}
	for _, p := range []string{"/products", "/products/1/disable", "/goods-receipts", "/transfers", "/transfers/1/send", "/prices"} {
		if n := call("POST", p, `{"location_id":"1"}`, token, "http://localhost:3000", csrf); n != 403 {
			t.Fatal("role", p, n)
		}
	}
	for _, p := range []string{"/sales?location_id=2", "/inventory?location_id=2", "/inventory/movements?location_id=2", "/transfers?location_id=2", "/prescriptions?location_id=2", "/controlled-ledger?location_id=2"} {
		if n := call("GET", p, "", token, "", ""); n != 403 {
			t.Fatal("location", p, n)
		}
	}
	if n := call("POST", "/sales", `{"location_id":"1","effective_at":"2026-01-01T00:00:00Z"}`, token, "http://localhost:3000", csrf); n != 400 {
		t.Fatal("client clock override", n)
	}
	if n := call("POST", "/sales", `{"location_id":"1","total":"1"}`, token, "http://localhost:3000", csrf); n != 400 {
		t.Fatal("client total", n)
	}
}
