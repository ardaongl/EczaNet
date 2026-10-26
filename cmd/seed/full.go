package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"time"
)

//go:embed full.sql
var fullSQL string

// Bulk history is an offline migrator path; no constraint/trigger is disabled.
// Opt-in preserves existing installations. A different version requires reset.
func seedFull(ctx context.Context, db *sql.DB, date, seed string) error {
	if os.Getenv("DEMO_FULL_SEED") != "true" {
		return nil
	}
	started := time.Now()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(33916)`); e != nil {
		return e
	}
	var done bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-feature-seeds" WHERE version='a6-full-v3' AND demo_date=$1 AND demo_seed=$2)`, date, seed).Scan(&done); e != nil {
		return e
	}
	if done {
		fmt.Println("full seed unchanged")
		return tx.Commit()
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM "pl-products"`).Scan(&count); e != nil {
		return e
	}
	var lastProduct, maxProduct, sales, maxSale, locations, users int64
	if e = tx.QueryRowContext(ctx, `SELECT (SELECT last_value FROM "pl-products_id_seq"),(SELECT coalesce(max(id),0) FROM "pl-products"),(SELECT count(*) FROM "pl-sales"),(SELECT coalesce(max(id),0) FROM "pl-sales"),(SELECT count(*) FROM "pl-locations"),(SELECT count(*) FROM "pl-wy-users")`).Scan(&lastProduct, &maxProduct, &sales, &maxSale, &locations, &users); e != nil {
		return e
	}
	if count != 5 || lastProduct != 5 || maxProduct != 5 || sales != 1 || maxSale != 1 || locations != 21 || users != 7 {
		return fmt.Errorf("%w: full history requires pristine A3 demo; failed partial seed requires isolated reset", errSeedMismatch)
	}
	if count != 5 {
		return fmt.Errorf("full seed requires fresh demo (five A3 products); explicit isolated reset required")
	}
	if _, e = tx.ExecContext(ctx, `SELECT set_config('eczanet.seed_date',$1,true),set_config('eczanet.seed',$2,true)`, date, seed); e != nil {
		return e
	}
	for i, statement := range strings.Split(fullSQL, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		step := time.Now()
		if _, e = tx.ExecContext(ctx, statement); e != nil {
			return fmt.Errorf("full history step%d: %w", i, e)
		}
		fmt.Printf("full_seed_step=%d duration=%s\n", i, time.Since(step))
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	fmt.Printf("full seed ready products=5000 historical_headers=270000 historical_lines=270000 duration=%s\n", time.Since(started))
	return nil
}
