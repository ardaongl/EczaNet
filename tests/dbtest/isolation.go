// Package dbtest coordinates test writers only; production never acquires this lock.
package dbtest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Writers hold a shared session lease for their fixture's entire lifetime.
// Audit fault tests hold the exclusive lease, so Go package parallelism cannot
// put an unrelated stock transaction behind their global audit-table barrier.
// A dedicated connection avoids tying fixture-pool shutdown to this lease.
func Acquire(t *testing.T, exclusive bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("test isolation connection failed")
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	query, mode := `SELECT pg_advisory_lock_shared(33909)`, "shared-writer"
	if exclusive {
		query, mode = `SELECT pg_advisory_lock(33909)`, "exclusive-audit-fault"
	}
	if _, err = conn.Exec(ctx, query); err != nil {
		t.Fatalf("test isolation lease %s unavailable", mode)
	}
	t.Logf("DB test lease mode=%s pid=%d key=33909", mode, conn.PgConn().PID())
}
