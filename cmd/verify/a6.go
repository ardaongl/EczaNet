package main

import (
	"context"
	"eczanet/internal/acceptance"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"time"
)

func (v *runner) runFullT5() {
	w, c := v.a3Account("depo")
	defer w.call("POST", "/api/v1/auth/logout", nil, c, 204)
	d := time.Now().In(time.FixedZone("Istanbul", 10800)).Format("2006-01-02")
	r := w.postA2("/suggestion-runs", map[string]string{"location_id": "21", "as_of": d}, c, 201)
	elapsed, ok := r["calculation_ms"].(float64)
	if w.failed || str(r, "rows") != "100000" || !ok || !w.hasCommit || elapsed+w.commitMS >= 5000 {
		fmt.Println("FAIL T-5 full100000 requires fresh full-seed demo and <5000ms calculation+commit")
		v.failed = true
		return
	}
	fmt.Printf("PASS T-5 full100000 calculation_commit_ms=%.3f preparation_ms=%v (A5 cold+5warm evidence separate)\n", elapsed+w.commitMS, r["preparation_ms"])
}
func (v *runner) runInvariants() {
	// The full reconciliation scans the 270k-sale seed. Keep this sub-budget
	// below the 15-minute verify contract while allowing slower CI runners.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	dsn := os.Getenv("VERIFY_DATABASE_URL")
	if dsn == "" {
		fmt.Println("FAIL INVARIANTS VERIFY_DATABASE_URL required")
		v.failed = true
		return
	}
	conn, e := pgx.Connect(ctx, dsn)
	if e == nil {
		defer conn.Close(ctx)
		e = acceptance.Check(ctx, conn)
	}
	if e != nil {
		var violation *acceptance.Violation
		var pg *pgconn.PgError
		if errors.As(e, &violation) {
			fmt.Println("FAIL INVARIANTS", violation.Error())
		} else if errors.As(e, &pg) {
			fmt.Println("FAIL INVARIANTS database_code=" + pg.Code)
		} else {
			fmt.Println("FAIL INVARIANTS connection/configuration or query budget")
		}
		v.failed = true
		return
	}
	fmt.Println("PASS INVARIANTS general A3/A4/A6 full database")
}
