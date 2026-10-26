package acceptance

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/jackc/pgx/v5"
	"regexp"
)

//go:embed a3.sql
var a3 string

//go:embed a4.sql
var a4 string

//go:embed a6.sql
var a6 string

// Check evaluates every unchanged general query in a read-only transaction.
// No exception filter, temporary-table privilege or business write is required.
func Check(ctx context.Context, conn *pgx.Conn) error {
	tx, e := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	// Global reconciliation joins the entire history. Use bounded serial plans:
	// Docker's default64MiB shared memory is not a parallel-hash work area.
	if _, e = tx.Exec(ctx, `SET LOCAL jit=off; SET LOCAL work_mem='64MB'; SET LOCAL max_parallel_workers_per_gather=0`); e != nil {
		return e
	}
	pattern := regexp.MustCompile(`INSERT INTO "pl-a[34]-checks"\s*([\s\S]*?);`)
	matches := pattern.FindAllStringSubmatch(a3+"\n"+a4, -1)
	if len(matches) != 3 {
		return fmt.Errorf("invariant SQL catalog mismatch")
	}
	matches = append(matches, []string{"", a6})
	for _, match := range matches {
		rows, e := tx.Query(ctx, match[1])
		if e != nil {
			return e
		}
		for rows.Next() {
			var name string
			var count int64
			if e = rows.Scan(&name, &count); e != nil {
				rows.Close()
				return e
			}
			if count != 0 {
				rows.Close()
				return &Violation{Name: name, Count: count}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

type Violation struct {
	Name  string
	Count int64
}

func (e *Violation) Error() string { return fmt.Sprintf("%s violations=%d", e.Name, e.Count) }
