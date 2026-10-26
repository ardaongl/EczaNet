// Diagnostic single call through the real Service, without HTTP or warm-up.
// URLs and credentials stay in the inherited tools environment and are never logged.
package main

import (
	"context"
	"eczanet/internal/stock"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"time"
)

func main() {
	ctx := context.Background()
	dsn := strings.Replace(os.Getenv("TEST_RUNTIME_URL"), "eczanet_demo", "eczanet_a5_bench17", 1)
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		panic("profile connection configuration failed")
	}
	defer pool.Close()
	started := time.Now()
	r, e := (stock.Service{Pool: pool}).Execute(ctx, "3", "suggestion.calculate", "", fmt.Sprint("PROFILE-", started.UnixNano()), "profile-service", stock.Input{LocationID: "21", AsOf: "2026-10-25"})
	out := map[string]any{"elapsed_ms": float64(time.Since(started).Microseconds()) / 1000, "status": r.Status, "commit_ms": r.CommitMS}
	if e != nil {
		out["error"] = e.Error()
	} else {
		var b any
		_ = json.Unmarshal(r.Body, &b)
		out["result"] = b
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	if e != nil {
		os.Exit(1)
	}
}
