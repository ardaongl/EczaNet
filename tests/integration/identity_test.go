package integration

import (
	"context"
	"eczanet/internal/auth"
	"eczanet/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestPostgresIdentity(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	runtime := os.Getenv("TEST_RUNTIME_URL")
	if dsn == "" || runtime == "" {
		t.Skip("PostgreSQL integration requires TEST_DATABASE_URL and TEST_RUNTIME_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal("migration connection")
	}
	defer pool.Close()
	rp, e := pgxpool.New(ctx, runtime)
	if e != nil {
		t.Fatal("runtime connection")
	}
	defer rp.Close()
	var bad int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename NOT LIKE 'pl-%'`).Scan(&bad); e != nil || bad != 0 {
		t.Fatal("table prefix", bad, e)
	}
	var users, locations, first int
	var email string
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM "pl-wy-users" WHERE id BETWEEN 1 AND 7`).Scan(&users); e != nil || users != 7 {
		t.Fatal("users", users, e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM "pl-locations" WHERE id BETWEEN 1 AND 21`).Scan(&locations); e != nil || locations != 21 {
		t.Fatal("locations", locations, e)
	}
	if e = pool.QueryRow(ctx, `SELECT id,email FROM "pl-wy-users" ORDER BY id LIMIT 1`).Scan(&first, &email); e != nil || first != 1 || email != "seed.yildiz@ddo-case.invalid" {
		t.Fatal("first seed")
	}
	store := auth.Postgres{Pool: rp}
	user, hash, e := store.Credentials(ctx, email)
	if e != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(os.Getenv("DEMO_PASSWORD"))) != nil {
		t.Fatal("bcrypt credentials")
	}
	token := auth.Token()
	defer pool.Exec(context.Background(), `DELETE FROM "pl-sessions" WHERE token_hash=$1`, auth.Hash(token))
	if e = store.CreateSession(ctx, user.ID, auth.Hash(token), auth.Hash("csrf"), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Session(ctx, auth.Hash(token)); e != nil {
		t.Fatal(e)
	}
	if e = store.Revoke(ctx, auth.Hash(token)); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Session(ctx, auth.Hash(token)); e != auth.ErrUnauthenticated {
		t.Fatal("revoked session accepted")
	}
	expired := auth.Token()
	defer pool.Exec(context.Background(), `DELETE FROM "pl-sessions" WHERE token_hash=$1`, auth.Hash(expired))
	if e = store.CreateSession(ctx, user.ID, auth.Hash(expired), auth.Hash("csrf"), time.Now().Add(-time.Second)); e != nil {
		t.Fatal(e)
	}
	server := platform.NewServer(platform.Config{RequestTimeout: time.Second}, store, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: "eczanet_session", Value: expired})
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 || rec.Header().Get("X-DDO-Stamp") != "a33906" {
		t.Fatal("expired DB session HTTP acceptance")
	}
	for _, q := range []string{`UPDATE "pl-wy-users" SET role='warehouse' WHERE id=1`, `DELETE FROM "pl-wy-users" WHERE id=1`, `CREATE TABLE "pl-forbidden"(id int)`, `SELECT * FROM "pl-goose-db-version"`} {
		if _, e = rp.Exec(ctx, q); e == nil {
			t.Fatal("runtime privilege accepted", q)
		}
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `INSERT INTO "pl-user-locations" VALUES (99999,1)`); e == nil {
		t.Fatal("orphan FK")
	}
	tx.Rollback(ctx)
	tx, e = pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `INSERT INTO "pl-wy-users"(email,role,password_hash) SELECT email,role,password_hash FROM "pl-wy-users" WHERE id=1`); e == nil {
		t.Fatal("duplicate email")
	}
}
